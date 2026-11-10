package gateway

import (
	"testing"
	"time"
)

func TestTokenBucketBurstAndRefill(t *testing.T) {
	b := newTokenBucket(60)
	now := time.Now()

	allowed := 0
	for i := 0; i < 60; i++ {
		if b.allow(now) {
			allowed++
		}
	}
	if allowed != 60 {
		t.Fatalf("burst should allow 60, got %d", allowed)
	}
	if b.allow(now) {
		t.Fatal("61st immediate request should be rejected")
	}
	// 1 秒后按 1 token/s 补充。
	if !b.allow(now.Add(time.Second)) {
		t.Fatal("bucket should refill over time")
	}
}

func TestRateLimiterRegistry(t *testing.T) {
	r := NewRateLimiter()
	if !r.Allow("k", 0) {
		t.Fatal("rpm 0 should mean unlimited")
	}
	accepted := 0
	for i := 0; i < 100; i++ {
		if r.Allow("k", 10) {
			accepted++
		}
	}
	if accepted != 10 {
		t.Fatalf("expected 10 accepted, got %d", accepted)
	}
	// 不同键互不影响。
	if !r.Allow("other", 10) {
		t.Fatal("different key should have its own bucket")
	}
}
