package gateway

import (
	"sync"
	"time"
)

// tokenBucket 是经典令牌桶：按固定速率补充，桶容量为 burst。
type tokenBucket struct {
	rate   float64 // 每秒补充令牌数
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rpm int) *tokenBucket {
	rate := float64(rpm) / 60.0
	return &tokenBucket{rate: rate, burst: float64(rpm), tokens: float64(rpm), last: time.Now()}
}

// allow 判断当前是否可取用一个令牌。
func (b *tokenBucket) allow(now time.Time) bool {
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// RateLimiter 按逻辑键维护独立的令牌桶。
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

// NewRateLimiter 创建限流器。
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: map[string]*tokenBucket{}}
}

// Allow 对给定键按 rpm 限流；rpm<=0 表示不限流。
func (r *RateLimiter) Allow(key string, rpm int) bool {
	if rpm <= 0 {
		return true
	}
	r.mu.Lock()
	b, ok := r.buckets[key]
	if !ok {
		b = newTokenBucket(rpm)
		r.buckets[key] = b
	}
	r.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	return b.allow(time.Now())
}
