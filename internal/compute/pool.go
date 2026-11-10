package compute

import (
	"sync"
	"time"
)

// Pool 是算力池：并发安全地管理可调度节点的注册、心跳、健康与查询。
type Pool struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	now   func() time.Time
}

// NewPool 构造算力池。
func NewPool(now func() time.Time) *Pool {
	return &Pool{nodes: map[string]*Node{}, now: now}
}

// Register 注册节点；ID 已存在时返回 ErrDuplicate。
func (p *Pool) Register(n *Node) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.nodes[n.ID]; ok {
		return ErrDuplicate
	}
	if n.LastBeat.IsZero() {
		n.LastBeat = p.now()
	}
	if n.Labels == nil {
		n.Labels = map[string]string{}
	}
	p.nodes[n.ID] = n
	return nil
}

// Deregister 移除节点。
func (p *Pool) Deregister(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.nodes[id]; !ok {
		return ErrNotFound
	}
	delete(p.nodes, id)
	return nil
}

// Heartbeat 更新节点占用、状态与心跳时间。
func (p *Pool) Heartbeat(id string, used Resources, state NodeState) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.nodes[id]
	if !ok {
		return ErrNotFound
	}
	n.Used = used
	n.State = state
	n.LastBeat = p.now()
	return nil
}

// Drain 标记节点优雅下线，不再接收新任务。
func (p *Pool) Drain(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.nodes[id]
	if !ok {
		return ErrNotFound
	}
	n.State = NodeDraining
	return nil
}

// Get 返回节点。
func (p *Pool) Get(id string) (*Node, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n, ok := p.nodes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return n, nil
}

// Nodes 返回全部节点快照。
func (p *Pool) Nodes() []*Node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Node, 0, len(p.nodes))
	for _, n := range p.nodes {
		out = append(out, n)
	}
	return out
}

// Schedulable 返回当前可承载新任务的健康节点。
func (p *Pool) Schedulable() []*Node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Node, 0, len(p.nodes))
	for _, n := range p.nodes {
		if n.schedulable() {
			out = append(out, n)
		}
	}
	return out
}

// ReapUnhealthy 把心跳超过 ttl 的健康节点标记为不健康，返回受影响数量。
func (p *Pool) ReapUnhealthy(ttl time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := p.now().Add(-ttl)
	reaped := 0
	for _, n := range p.nodes {
		if n.State == NodeHealthy && n.LastBeat.Before(cutoff) {
			n.State = NodeUnhealthy
			reaped++
		}
	}
	return reaped
}
