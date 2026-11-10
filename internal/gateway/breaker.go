package gateway

import (
	"sync"
	"time"
)

// BreakerState 是熔断器三态。
type BreakerState int

const (
	// StateClosed 正常放行。
	StateClosed BreakerState = iota
	// StateOpen 直接拒绝，不发起调用。
	StateOpen
	// StateHalfOpen 放行少量探针以探测恢复。
	StateHalfOpen
)

func (s BreakerState) String() string {
	switch s {
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "closed"
	}
}

// Breaker 实现基于连续失败的三态熔断器。
type Breaker struct {
	mu            sync.Mutex
	state         BreakerState
	failures      int
	threshold     int
	openDuration  time.Duration
	probes        int
	probeInFlight int
	openUntil     time.Time
}

// NewBreaker 创建熔断器。
func NewBreaker(threshold int, openDuration time.Duration, probes int) *Breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if probes <= 0 {
		probes = 1
	}
	return &Breaker{state: StateClosed, threshold: threshold, openDuration: openDuration, probes: probes}
}

// Allow 判断是否允许发起调用，必要时进行 open -> half_open 转换。
func (b *Breaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Before(b.openUntil) {
			return false
		}
		b.state = StateHalfOpen
		b.probeInFlight = 0
		return b.reserveProbeLocked()
	default: // half-open
		return b.reserveProbeLocked()
	}
}

func (b *Breaker) reserveProbeLocked() bool {
	if b.probeInFlight >= b.probes {
		return false
	}
	b.probeInFlight++
	return true
}

// Record 根据一次调用结果更新状态。
func (b *Breaker) Record(success bool, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		b.state = StateClosed
		b.failures = 0
		b.probeInFlight = 0
		return
	}
	b.failures++
	if b.state == StateHalfOpen || b.failures >= b.threshold {
		b.tripLocked(now)
	}
}

func (b *Breaker) tripLocked(now time.Time) {
	b.state = StateOpen
	b.probeInFlight = 0
	b.openUntil = now.Add(b.openDuration)
}

// State 返回当前状态快照。
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
