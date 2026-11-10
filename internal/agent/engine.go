package agent

import (
	"context"
	"strings"
	"time"

	"nexus/internal/id"
)

// engineCmd 是排队到单一写入者 goroutine 的命令。
type engineCmd struct {
	fn   func() error
	done chan error
}

// Engine 是领域的单一写入者：所有改变 Trigger/Thread/Decision/Ticket 的命令都
// 串行进入同一个 goroutine 执行，保证每个聚合只有一个 writer，且命令之间无数据竞争。
type Engine struct {
	repo          Repository
	bus           EventBus
	effects       EffectQueue
	now           func() time.Time
	policyVersion string
	cmds          chan engineCmd
}

// NewEngine 构造引擎。
func NewEngine(repo Repository, bus EventBus, effects EffectQueue, now func() time.Time) *Engine {
	return &Engine{
		repo:          repo,
		bus:           bus,
		effects:       effects,
		now:           now,
		policyVersion: "policy-1",
		cmds:          make(chan engineCmd),
	}
}

// Run 启动单一写入者循环，直到 ctx 取消。
func (e *Engine) Run(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-e.cmds:
				c.done <- c.fn()
			}
		}
	}()
}

func (e *Engine) submit(ctx context.Context, fn func() error) error {
	c := engineCmd{fn: fn, done: make(chan error, 1)}
	select {
	case e.cmds <- c:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-c.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Receive 接收一个 Trigger：去重、登记到所属线程、发布事件。
// 重复事件返回 ErrDuplicate。
func (e *Engine) Receive(ctx context.Context, t *Trigger) error {
	return e.submit(ctx, func() error {
		if _, err := e.repo.GetTrigger(ctx, t.ID); err == nil {
			return ErrDuplicate
		}
		if err := e.repo.SaveTrigger(ctx, t); err != nil {
			return err
		}
		for _, ref := range t.Threads {
			key := ThreadKey(ref)
			th, err := e.repo.GetThread(ctx, key)
			if err == ErrNotFound {
				th = &Thread{Key: key, Ref: ref, CreatedAt: e.now(), Meta: map[string]any{}}
			} else if err != nil {
				return err
			}
			if th.LastAt.Before(t.At) {
				th.LastAt = t.At
			}
			th.Pending = appendUnique(th.Pending, t.ID)
			if err := e.repo.SaveThread(ctx, th); err != nil {
				return err
			}
		}
		e.bus.Publish(ctx, Event{
			Type: EventTriggerReceived,
			At:   e.now(),
			Payload: map[string]any{
				"trigger_id": t.ID,
				"source":     t.Source,
				"text":       t.Text,
			},
		})
		return nil
	})
}

// ApplyDecision 保存决策。accept 时创建工单并入队 launch 副作用；ignore/stop 时把事件移出待处理。
func (e *Engine) ApplyDecision(ctx context.Context, d *Decision) (*Ticket, error) {
	var ticket *Ticket
	err := e.submit(ctx, func() error {
		d.At = e.now()
		d.PolicyVersion = e.policyVersion
		trigger, err := e.repo.GetTrigger(ctx, d.TriggerID)
		if err != nil {
			return err
		}
		if d.Kind == DecisionAccept {
			tk := e.buildTicket(ctx, trigger, d)
			if err := e.repo.SaveTicket(ctx, tk); err != nil {
				return err
			}
			e.removePending(ctx, trigger, d)
			e.bus.Publish(ctx, Event{Type: EventTicketCreated, At: e.now(), Payload: map[string]any{"ticket_id": tk.ID}})
			if err := e.effects.Enqueue(ctx, SideEffect{
				Kind:    EffectLaunch,
				Key:     tk.ID,
				Payload: map[string]any{"ticket_id": tk.ID},
			}); err != nil {
				return err
			}
			ticket = tk
			return nil
		}
		e.removePending(ctx, trigger, d)
		e.bus.Publish(ctx, Event{Type: EventDecisionMade, At: e.now(), Payload: map[string]any{"kind": string(d.Kind)}})
		return nil
	})
	return ticket, err
}

// buildTicket 依据触发事件与决策构造工单及不可变授权。
func (e *Engine) buildTicket(ctx context.Context, trigger *Trigger, d *Decision) *Ticket {
	now := e.now()
	strength := StrengthInferred
	if trigger.Human || trigger.Addressed != AddressedNone {
		strength = StrengthExplicit
	}
	channels := make([]ChannelRef, 0, 1)
	if trigger.Channel != nil {
		channels = append(channels, *trigger.Channel)
	}
	title := strings.TrimSpace(d.Reason)
	if title == "" {
		title = truncate(trigger.Text, 60)
	}
	return &Ticket{
		ID:         "ticket-" + id.Short(),
		Title:      title,
		State:      TicketOpen,
		Mandate:    Mandate{Strength: strength, PolicyVersion: e.policyVersion, Actor: trigger.Actor, Scope: ThreadKey(trigger.Threads[0])},
		Channels:   channels,
		TriggerIDs: []string{trigger.ID},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// StartAssignment 为工单创建一个运行中的派工并发布事件。
func (e *Engine) StartAssignment(ctx context.Context, ticketID string) (*Assignment, error) {
	var assignment *Assignment
	err := e.submit(ctx, func() error {
		tk, err := e.repo.GetTicket(ctx, ticketID)
		if err != nil {
			return err
		}
		if !tk.Live() {
			return ErrNoTicket
		}
		now := e.now()
		assignment = &Assignment{ID: "asg-" + id.Short(), State: AssignmentRunning, CreatedAt: now, StartedAt: now}
		tk.Assignments = append(tk.Assignments, assignment)
		tk.UpdatedAt = now
		if err := e.repo.SaveTicket(ctx, tk); err != nil {
			return err
		}
		e.bus.Publish(ctx, Event{Type: EventAssignmentRun, At: now, Payload: map[string]any{"ticket_id": tk.ID}})
		return nil
	})
	return assignment, err
}

// RecordReport 记录派工上报；ready/dropped 关闭工单。
func (e *Engine) RecordReport(ctx context.Context, ticketID string, r *Report) error {
	return e.submit(ctx, func() error {
		tk, err := e.repo.GetTicket(ctx, ticketID)
		if err != nil {
			return err
		}
		now := e.now()
		r.At = now
		if asg := tk.LatestAssignment(); asg != nil {
			asg.Report = r
		}
		if r.State == ReportReady || r.State == ReportDropped {
			tk.State = TicketClosed
			tk.Outcome = string(r.State)
			if a := tk.LatestAssignment(); a != nil {
				a.State = AssignmentEnded
				a.EndedAt = now
			}
			e.bus.Publish(ctx, Event{Type: EventTicketClosed, At: now, Payload: map[string]any{"ticket_id": tk.ID, "outcome": tk.Outcome}})
		}
		tk.UpdatedAt = now
		return e.repo.SaveTicket(ctx, tk)
	})
}

// removePending 把触发事件从所有相关线程的待处理列表移除。
func (e *Engine) removePending(ctx context.Context, trigger *Trigger, _ *Decision) {
	for _, ref := range trigger.Threads {
		th, err := e.repo.GetThread(ctx, ThreadKey(ref))
		if err != nil {
			continue
		}
		th.Pending = removeValue(th.Pending, trigger.ID)
		_ = e.repo.SaveThread(ctx, th)
	}
}

// ---- 切片辅助 ----

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func removeValue(xs []string, v string) []string {
	for i, x := range xs {
		if x == v {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
