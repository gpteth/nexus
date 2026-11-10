package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"

	"nexus/internal/id"
)

const routerSystemPrompt = `You are the routing layer of an enterprise agent platform. You only triage events; you never execute tasks.
Decide whether the event requires executing a task ("accept"), can be safely ignored ("ignore"), or asks to stop existing work ("stop").
Respond with ONLY a JSON object: {"decision":"accept|ignore|stop","reason":"one short sentence","title":"short task title if accept"}`

type routerOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Title    string `json:"title"`
}

// Router 是主会话（轻量模型）：常驻、只做交互与分派，不陷入任务细节。
// 它订阅事件，经 Fast Path 确定性过滤、Slow Path 模型研判后写入决策。
type Router struct {
	engine *Engine
	llm    LLM
	model  string
	bus    EventBus
	jobs   chan string
	logger *slog.Logger
}

// NewRouter 构造 Router。
func NewRouter(engine *Engine, llm LLM, model string, bus EventBus, logger *slog.Logger) *Router {
	return &Router{
		engine: engine,
		llm:    llm,
		model:  model,
		bus:    bus,
		jobs:   make(chan string, 256),
		logger: logger,
	}
}

// Run 订阅事件并启动工作池，直到 ctx 取消。
func (r *Router) Run(ctx context.Context) {
	cancel := r.bus.Subscribe(EventTriggerReceived, func(ev Event) {
		if triggerID, ok := ev.Payload["trigger_id"].(string); ok {
			select {
			case r.jobs <- triggerID:
			default:
			}
		}
	})
	defer cancel()

	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.consume(ctx)
		}()
	}
	wg.Wait()
}

func (r *Router) consume(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case triggerID := <-r.jobs:
			r.handle(ctx, triggerID)
		}
	}
}

func (r *Router) handle(ctx context.Context, triggerID string) {
	trigger, err := r.engine.repo.GetTrigger(ctx, triggerID)
	if err != nil {
		return
	}
	decision := r.fastPath(trigger)
	if decision == nil {
		decision = r.slowPath(ctx, trigger)
	}
	if _, err := r.engine.ApplyDecision(ctx, decision); err != nil {
		r.logger.Warn("router: apply decision failed", "trigger_id", triggerID, "error", err.Error())
	}
}

// fastPath 用确定性规则过滤大部分噪音，不消耗模型调用。
func (r *Router) fastPath(t *Trigger) *Decision {
	if !t.Human && t.Addressed == AddressedNone {
		return r.makeDecision(t, DecisionIgnore, "filtered non-human noise")
	}
	if t.Addressed != AddressedNone && containsStopWord(t.Text) {
		return r.makeDecision(t, DecisionStop, "explicit stop request")
	}
	return nil
}

// slowPath 调用轻量模型研判候选。
func (r *Router) slowPath(ctx context.Context, t *Trigger) *Decision {
	messages := []Message{
		{Role: "system", Content: routerSystemPrompt},
		{Role: "user", Content: describeTrigger(t)},
	}
	out, err := r.llm.Complete(ctx, r.model, messages)
	if err != nil {
		return r.makeDecision(t, DecisionIgnore, "llm unavailable")
	}
	var parsed routerOutput
	if err := json.Unmarshal([]byte(extractJSON(out)), &parsed); err != nil {
		return r.makeDecision(t, DecisionIgnore, "unparseable model output")
	}
	kind := DecisionKind(strings.ToLower(strings.TrimSpace(parsed.Decision)))
	if kind != DecisionAccept && kind != DecisionIgnore && kind != DecisionStop {
		kind = DecisionIgnore
	}
	reason := parsed.Reason
	if kind == DecisionAccept && parsed.Title != "" {
		reason = parsed.Title
	}
	return r.makeDecision(t, kind, reason)
}

func (r *Router) makeDecision(t *Trigger, kind DecisionKind, reason string) *Decision {
	return &Decision{
		ID:        "decision-" + id.Short(),
		TriggerID: t.ID,
		ThreadKey: ThreadKey(t.Threads[0]),
		Kind:      kind,
		Reason:    reason,
	}
}

func describeTrigger(t *Trigger) string {
	return fmt.Sprintf("source=%s actor=%s human=%t addressed=%s\ntext: %s",
		t.Source, t.Actor.ID, t.Human, string(t.Addressed), t.Text)
}

// containsStopWord 判断是否为明确的停止/取消指令。
func containsStopWord(text string) bool {
	lower := strings.ToLower(text)
	for _, w := range []string{"stop", "cancel", "abort", "停止", "取消", "中止"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// extractJSON 从可能含 markdown 代码块的模型输出中提取首个 JSON 对象。
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return strings.TrimSpace(s)
}
