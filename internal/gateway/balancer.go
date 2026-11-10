package gateway

import (
	"errors"
	"math/rand"
	"sync"
	"time"
)

// ErrNoAvailableUpstream 表示一个组内当前没有健康且熔断关闭的上游。
var ErrNoAvailableUpstream = errors.New("no available upstream")

// peer 是组内一个上游及其 SWRR 动态权重。
type peer struct {
	up     *Upstream
	weight int
	cw     int
}

// Group 是一组等价上游，对外提供负载均衡选择。
type Group struct {
	strategy string
	mu       sync.Mutex
	peers    []*peer
}

// NewGroup 创建上游组，strategy 为 weighted_round_robin / least_connections / random。
func NewGroup(strategy string, ups ...*Upstream) *Group {
	g := &Group{strategy: strategy}
	for _, u := range ups {
		w := u.Weight()
		if w <= 0 {
			w = 1
		}
		g.peers = append(g.peers, &peer{up: u, weight: w})
	}
	return g
}

// Size 返回组成员数量。
func (g *Group) Size() int { return len(g.peers) }

func (g *Group) usable(u *Upstream, now time.Time) bool {
	return u.Healthy() && u.Breaker().State() != StateOpen
}

// Pick 按策略选择一个可用上游。
func (g *Group) Pick() (*Upstream, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	switch g.strategy {
	case "least_connections":
		return g.pickLeast(now)
	case "random":
		return g.pickRandom(now)
	default:
		return g.pickSWRR(now)
	}
}

// pickSWRR 实现 nginx 平滑加权轮询，避免权重节点连续命中。
func (g *Group) pickSWRR(now time.Time) (*Upstream, error) {
	var best *peer
	total := 0
	for _, p := range g.peers {
		if !g.usable(p.up, now) {
			continue
		}
		p.cw += p.weight
		total += p.weight
		if best == nil || p.cw > best.cw {
			best = p
		}
	}
	if best == nil {
		return nil, ErrNoAvailableUpstream
	}
	best.cw -= total
	return best.up, nil
}

func (g *Group) pickLeast(now time.Time) (*Upstream, error) {
	var best *Upstream
	bestActive := int(^uint(0) >> 1)
	for _, p := range g.peers {
		if !g.usable(p.up, now) {
			continue
		}
		if a := p.up.Active(); a < bestActive {
			bestActive = a
			best = p.up
		}
	}
	if best == nil {
		return nil, ErrNoAvailableUpstream
	}
	return best, nil
}

func (g *Group) pickRandom(now time.Time) (*Upstream, error) {
	type candidate struct {
		up     *Upstream
		weight int
	}
	var candidates []candidate
	total := 0
	for _, p := range g.peers {
		if !g.usable(p.up, now) {
			continue
		}
		candidates = append(candidates, candidate{p.up, p.weight})
		total += p.weight
	}
	if len(candidates) == 0 {
		return nil, ErrNoAvailableUpstream
	}
	roll := rand.Intn(total)
	for _, c := range candidates {
		roll -= c.weight
		if roll < 0 {
			return c.up, nil
		}
	}
	return candidates[0].up, nil
}
