package agent

import (
	"context"
	"sync"
	"time"
)

// MemoryEffectQueue 是 EffectQueue 的内存实现。它统一了原型中 Effects 与 Outbox 的职责：
// 命令事务内入队、提交后执行、按 Kind+Key 幂等去重、失败指数退避重试（至少一次）。
type MemoryEffectQueue struct {
	mu       sync.Mutex
	rows     map[string]*SideEffect
	seq      []string
	handlers map[string]func(context.Context, SideEffect) error
	wake     chan struct{}
	now      func() time.Time
}

// NewMemoryEffectQueue 返回内存副作用队列。
func NewMemoryEffectQueue(now func() time.Time) *MemoryEffectQueue {
	return &MemoryEffectQueue{
		rows:     map[string]*SideEffect{},
		handlers: map[string]func(context.Context, SideEffect) error{},
		wake:     make(chan struct{}, 1),
		now:      now,
	}
}

func effectKey(kind, key string) string { return kind + ":" + key }

// Enqueue 幂等入队：相同 Kind+Key 已存在时不重复添加。
func (q *MemoryEffectQueue) Enqueue(_ context.Context, e SideEffect) error {
	q.mu.Lock()
	pk := effectKey(e.Kind, e.Key)
	if _, exists := q.rows[pk]; exists {
		q.mu.Unlock()
		return nil
	}
	if e.EnqueuedAt.IsZero() {
		e.EnqueuedAt = q.now()
	}
	if e.RunAt.IsZero() {
		e.RunAt = q.now()
	}
	q.rows[pk] = &e
	q.seq = append(q.seq, pk)
	q.mu.Unlock()
	q.notify()
	return nil
}

func (q *MemoryEffectQueue) RegisterHandler(kind string, handler func(context.Context, SideEffect) error) {
	q.mu.Lock()
	q.handlers[kind] = handler
	q.mu.Unlock()
	q.notify()
}

// Pending 返回队列中副作用总数。
func (q *MemoryEffectQueue) Pending(_ context.Context) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.rows)
}

func (q *MemoryEffectQueue) handler(kind string) (func(context.Context, SideEffect) error, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	h, ok := q.handlers[kind]
	return h, ok
}

func (q *MemoryEffectQueue) remove(pk string) {
	if _, ok := q.rows[pk]; !ok {
		return
	}
	delete(q.rows, pk)
	for i, v := range q.seq {
		if v == pk {
			q.seq = append(q.seq[:i], q.seq[i+1:]...)
			break
		}
	}
}

func (q *MemoryEffectQueue) notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Run 阻塞处理副作用直到 ctx 取消；被唤醒或定时触发时扫描到期项。
func (q *MemoryEffectQueue) Run(ctx context.Context) {
	for {
		q.process(ctx)
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (q *MemoryEffectQueue) process(ctx context.Context) {
	q.mu.Lock()
	due := make([]*SideEffect, 0)
	for _, pk := range q.seq {
		e := q.rows[pk]
		if !e.RunAt.After(q.now()) {
			due = append(due, e)
		}
	}
	q.mu.Unlock()

	for _, e := range due {
		if ctx.Err() != nil {
			return
		}
		h, ok := q.handler(e.Kind)
		if !ok {
			continue
		}
		err := h(ctx, *e)
		q.mu.Lock()
		pk := effectKey(e.Kind, e.Key)
		if err == nil {
			q.remove(pk)
		} else if cur := q.rows[pk]; cur != nil {
			cur.Attempts++
			cur.RunAt = q.now().Add(effectBackoff(cur.Attempts))
		}
		q.mu.Unlock()
	}
}

// effectBackoff 是指数退避：100ms 起翻倍，上限 30s。
func effectBackoff(attempts int) time.Duration {
	const base = 100 * time.Millisecond
	d := base
	if attempts > 1 {
		shift := attempts - 1
		if shift > 9 {
			shift = 9
		}
		d = base << uint(shift)
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
