package agent

import (
	"context"
	"sync"
)

// memoryRepository 是 Repository 的并发安全内存实现，对应阶段一的内存存储风格。
type memoryRepository struct {
	mu       sync.RWMutex
	triggers map[string]*Trigger
	threads  map[string]*Thread
	tickets  map[string]*Ticket
}

// NewMemoryRepository 返回内存 Repository。
func NewMemoryRepository() Repository {
	return &memoryRepository{
		triggers: map[string]*Trigger{},
		threads:  map[string]*Thread{},
		tickets:  map[string]*Ticket{},
	}
}

func (r *memoryRepository) SaveTrigger(_ context.Context, t *Trigger) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.triggers[t.ID] = t
	return nil
}

func (r *memoryRepository) GetTrigger(_ context.Context, id string) (*Trigger, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.triggers[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

func (r *memoryRepository) SaveThread(_ context.Context, th *Thread) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threads[th.Key] = th
	return nil
}

func (r *memoryRepository) GetThread(_ context.Context, key string) (*Thread, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	th, ok := r.threads[key]
	if !ok {
		return nil, ErrNotFound
	}
	return th, nil
}

func (r *memoryRepository) SaveTicket(_ context.Context, tk *Ticket) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tickets[tk.ID] = tk
	return nil
}

func (r *memoryRepository) GetTicket(_ context.Context, id string) (*Ticket, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tk, ok := r.tickets[id]
	if !ok {
		return nil, ErrNotFound
	}
	return tk, nil
}

func (r *memoryRepository) ListOpenTickets(_ context.Context) ([]*Ticket, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Ticket, 0, len(r.tickets))
	for _, tk := range r.tickets {
		if tk.Live() {
			out = append(out, tk)
		}
	}
	return out, nil
}

// ListAllTickets 返回全部工单（含已关闭），主要用于测试与控制台。
func (r *memoryRepository) ListAllTickets() []*Ticket {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Ticket, 0, len(r.tickets))
	for _, tk := range r.tickets {
		out = append(out, tk)
	}
	return out
}
