package backoff

import (
	"testing"
	"time"
)

func TestExponential(t *testing.T) {
	if got := Exponential(0, time.Second, 10*time.Second); got != time.Second {
		t.Fatalf("attempt 0 should be base, got %v", got)
	}
	if got := Exponential(1, time.Second, 10*time.Second); got != 2*time.Second {
		t.Fatalf("attempt 1 should be 2x, got %v", got)
	}
	if got := Exponential(100, time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("should cap at max, got %v", got)
	}
}

func TestJitterBounds(t *testing.T) {
	for i := 0; i < 50; i++ {
		j := WithJitter(time.Second)
		if j < 500*time.Millisecond || j >= time.Second {
			t.Fatalf("jitter out of [d/2,d): %v", j)
		}
	}
}
