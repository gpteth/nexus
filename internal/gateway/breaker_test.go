package gateway

import (
	"testing"
	"time"
)

func TestBreakerTripAndRecover(t *testing.T) {
	b := NewBreaker(3, 100*time.Millisecond, 1)
	now := time.Now()

	if b.State() != StateClosed {
		t.Fatal("should start closed")
	}
	for i := 0; i < 2; i++ {
		b.Record(false, now)
	}
	if b.State() != StateClosed {
		t.Fatal("two failures should not trip threshold 3")
	}
	b.Record(false, now)
	if b.State() != StateOpen {
		t.Fatal("third failure should trip open")
	}
	if b.Allow(now) {
		t.Fatal("should reject while open")
	}

	later := now.Add(200 * time.Millisecond)
	if !b.Allow(later) {
		t.Fatal("should allow probe after open duration")
	}
	if b.State() != StateHalfOpen {
		t.Fatal("should transition to half-open")
	}
	b.Record(true, later)
	if b.State() != StateClosed {
		t.Fatal("successful probe should close breaker")
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	b := NewBreaker(1, 50*time.Millisecond, 1)
	now := time.Now()
	b.Record(false, now)
	if b.State() != StateOpen {
		t.Fatal("should be open")
	}
	later := now.Add(60 * time.Millisecond)
	if !b.Allow(later) {
		t.Fatal("probe expected")
	}
	b.Record(false, later)
	if b.State() != StateOpen {
		t.Fatal("failed probe should reopen")
	}
}
