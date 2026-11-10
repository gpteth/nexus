// Package backoff 提供指数退避与抖动，用于重试与副作用队列。
package backoff

import (
	"math/rand"
	"time"
)

// Exponential 返回第 attempt 次（从 0 开始）的退避时长：base * 2^attempt，封顶 max。
func Exponential(attempt int, base, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := base << attempt
	// 移位溢出或超过上限时收敛到 max。
	if d <= 0 || d > max {
		d = max
	}
	return d
}

// WithJitter 在 [d/2, d) 区间内加入随机抖动，避免重试风暴。
func WithJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := int64(d / 2)
	return time.Duration(half + rand.Int63n(half))
}

// ExponentialJitter 组合指数退避与抖动。
func ExponentialJitter(attempt int, base, max time.Duration) time.Duration {
	return WithJitter(Exponential(attempt, base, max))
}
