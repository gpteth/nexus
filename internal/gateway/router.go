package gateway

import (
	"sort"
	"sync"
)

// ModelRouter 维护逻辑模型到上游组的映射。
type ModelRouter struct {
	mu     sync.RWMutex
	groups map[string]*Group
}

// NewModelRouter 创建空路由表。
func NewModelRouter() *ModelRouter {
	return &ModelRouter{groups: map[string]*Group{}}
}

// Set 注册模型到组的映射。
func (r *ModelRouter) Set(model string, g *Group) {
	r.mu.Lock()
	r.groups[model] = g
	r.mu.Unlock()
}

// GroupFor 返回模型对应的上游组。
func (r *ModelRouter) GroupFor(model string) (*Group, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.groups[model]
	return g, ok
}

// Models 返回所有已注册模型，按字母序。
func (r *ModelRouter) Models() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.groups))
	for m := range r.groups {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
