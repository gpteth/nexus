package gateway

import (
	"errors"
	"testing"
	"time"

	"nexus/internal/config"
)

func makeUpstream(name string, weight int) *Upstream {
	uc := config.UpstreamConfig{Name: name, BaseURL: "http://example.com", Weight: weight, Models: []string{"m"}}
	return NewUpstream(uc, nil, NewBreaker(5, time.Second, 1))
}

func TestSWRRWeightedDistribution(t *testing.T) {
	g := NewGroup("weighted_round_robin", makeUpstream("a", 3), makeUpstream("b", 1))
	counts := map[string]int{}
	for i := 0; i < 400; i++ {
		u, err := g.Pick()
		if err != nil {
			t.Fatal(err)
		}
		counts[u.Name()]++
	}
	if counts["a"] != 300 || counts["b"] != 100 {
		t.Fatalf("expected 300/100, got %d/%d", counts["a"], counts["b"])
	}
}

func TestPickWhenAllUnavailable(t *testing.T) {
	u := makeUpstream("a", 1)
	u.breaker = NewBreaker(1, time.Second, 1)
	u.breaker.Record(false, time.Now()) // 阈值 1，一次失败即熔断打开
	g := NewGroup("weighted_round_robin", u)
	_, err := g.Pick()
	if !errors.Is(err, ErrNoAvailableUpstream) {
		t.Fatalf("expected ErrNoAvailableUpstream, got %v", err)
	}
}

func TestLeastConnections(t *testing.T) {
	a := makeUpstream("a", 1)
	b := makeUpstream("b", 1)
	a.active = 5
	g := NewGroup("least_connections", a, b)
	pick, err := g.Pick()
	if err != nil {
		t.Fatal(err)
	}
	if pick.Name() != "b" {
		t.Fatalf("expected least-loaded b, got %s", pick.Name())
	}
}
