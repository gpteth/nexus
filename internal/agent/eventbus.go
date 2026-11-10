package agent

import (
	"context"
	"sync"
)

type subscription struct {
	id      int
	handler func(Event)
}

// memoryEventBus 是 EventBus 的内存实现，支持按类型订阅与通配符 "*"。
// 处理器在发布者的 goroutine 内同步执行，订阅者应自行控制耗时。
type memoryEventBus struct {
	mu       sync.RWMutex
	nextID   int
	handlers map[string][]subscription
}

// NewEventBus 返回内存事件总线。
func NewEventBus() EventBus {
	return &memoryEventBus{handlers: map[string][]subscription{}}
}

func (b *memoryEventBus) Publish(_ context.Context, e Event) {
	b.mu.RLock()
	subs := make([]subscription, 0, len(b.handlers[e.Type])+len(b.handlers["*"]))
	subs = append(subs, b.handlers[e.Type]...)
	subs = append(subs, b.handlers["*"]...)
	b.mu.RUnlock()
	for _, s := range subs {
		s.handler(e)
	}
}

func (b *memoryEventBus) Subscribe(eventType string, handler func(Event)) func() {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.handlers[eventType] = append(b.handlers[eventType], subscription{id: id, handler: handler})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		hs := b.handlers[eventType]
		for i, s := range hs {
			if s.id == id {
				b.handlers[eventType] = append(hs[:i], hs[i+1:]...)
				return
			}
		}
	}
}
