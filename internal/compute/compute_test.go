package compute

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- fakes ----

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

type mockScaler struct {
	mu    sync.Mutex
	ups   int
	downs [][]string
}

func (m *mockScaler) ScaleUp(_ context.Context, count int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ups += count
	return nil
}

func (m *mockScaler) ScaleDown(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.downs = append(m.downs, append([]string{}, ids...))
	return nil
}

func newNode(id string, cap Resources, labels map[string]string) *Node {
	return &Node{ID: id, Kind: NodeGPU, Endpoint: "http://" + id, State: NodeHealthy, Capacity: cap, Labels: labels}
}

func newJob(id, tenant string, req Resources, pri Priority) *Job {
	return &Job{ID: id, TenantID: tenant, Request: req, Priority: pri}
}

// ---- 资源模型 ----

func TestResources_Fits(t *testing.T) {
	cap := Resources{MemoryMB: 8000, Slots: 4, GPUs: 1}
	if !(&Resources{MemoryMB: 2000, Slots: 1, GPUs: 1}).FitsIn(cap) {
		t.Fatal("should fit")
	}
	if (&Resources{MemoryMB: 9000, Slots: 1, GPUs: 1}).FitsIn(cap) {
		t.Fatal("memory should not fit")
	}
}

// ---- 节点池 ----

func TestPool_RegisterAndHeartbeat(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	if err := pool.Register(newNode("n1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, nil)); err != nil {
		t.Fatal(err)
	}
	if err := pool.Register(newNode("n1", Resources{}, nil)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err=%v want duplicate", err)
	}
	if err := pool.Heartbeat("n1", Resources{MemoryMB: 500, Slots: 1, GPUs: 1}, NodeHealthy); err != nil {
		t.Fatal(err)
	}
	n, _ := pool.Get("n1")
	if n.Used.MemoryMB != 500 {
		t.Fatal("heartbeat usage")
	}
	if err := pool.Deregister("n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Get("n1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("should be removed")
	}
}

func TestPool_ReapUnhealthy(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	n := newNode("n1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, nil)
	if err := pool.Register(n); err != nil {
		t.Fatal(err)
	}
	n.LastBeat = now().Add(-2 * time.Minute)
	if reaped := pool.ReapUnhealthy(time.Minute); reaped != 1 {
		t.Fatalf("reaped=%d want 1", reaped)
	}
	if n.State != NodeUnhealthy {
		t.Fatal("node should be unhealthy")
	}
}

// ---- 调度放置 ----

func TestScheduler_PlaceJob(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("n1", Resources{MemoryMB: 8000, Slots: 4, GPUs: 1}, nil))
	sched := NewScheduler(pool, now, testLogger())

	job := newJob("j1", "t1", Resources{MemoryMB: 2000, Slots: 1, GPUs: 1}, PriorityNormal)
	if err := sched.Submit(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if job.State != JobRunning || job.NodeID != "n1" {
		t.Fatalf("job not placed: state=%s node=%s", job.State, job.NodeID)
	}
	n, _ := pool.Get("n1")
	if n.Used.MemoryMB != 2000 {
		t.Fatal("usage not recorded")
	}
}

func TestScheduler_BinPacking(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("small", Resources{MemoryMB: 2000, Slots: 2, GPUs: 1}, nil))
	pool.Register(newNode("big", Resources{MemoryMB: 8000, Slots: 4, GPUs: 1}, nil))
	sched := NewScheduler(pool, now, testLogger())

	job := newJob("j1", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	sched.Submit(context.Background(), job)
	if job.NodeID != "small" {
		t.Fatalf("should bin-pack to small, got %s", job.NodeID)
	}
}

func TestScheduler_Affinity(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("n1", Resources{MemoryMB: 8000, Slots: 4, GPUs: 1}, map[string]string{"zone": "a"}))
	pool.Register(newNode("n2", Resources{MemoryMB: 8000, Slots: 4, GPUs: 1}, map[string]string{"zone": "b"}))
	sched := NewScheduler(pool, now, testLogger())

	job := newJob("j1", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	job.Affinity = map[string]string{"zone": "b"}
	sched.Submit(context.Background(), job)
	if job.NodeID != "n2" {
		t.Fatalf("affinity should land on n2, got %s", job.NodeID)
	}
}

// ---- 排队与完成回收 ----

func TestScheduler_QueueThenComplete(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("n1", Resources{MemoryMB: 4000, Slots: 1, GPUs: 1}, nil))
	sched := NewScheduler(pool, now, testLogger())

	j1 := newJob("j1", "t1", Resources{MemoryMB: 3000, Slots: 1, GPUs: 1}, PriorityNormal)
	j2 := newJob("j2", "t1", Resources{MemoryMB: 3000, Slots: 1, GPUs: 1}, PriorityNormal)
	sched.Submit(context.Background(), j1)
	sched.Submit(context.Background(), j2)

	if j2.State != JobQueued || sched.Queued() != 1 {
		t.Fatal("j2 should be queued")
	}
	if err := sched.Complete(context.Background(), "j1", JobSucceeded); err != nil {
		t.Fatal(err)
	}
	if j2.State != JobRunning {
		t.Fatal("j2 should run after completion")
	}
	if j1.State != JobSucceeded {
		t.Fatal("j1 final state")
	}
}

// ---- 配额 ----

func TestScheduler_ConcurrentQuota(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("n1", Resources{MemoryMB: 10000, Slots: 8, GPUs: 2}, nil))
	sched := NewScheduler(pool, now, testLogger())
	sched.SetQuota(&Quota{TenantID: "t1", MaxConcurrent: 1})

	j1 := newJob("j1", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	if err := sched.Submit(context.Background(), j1); err != nil {
		t.Fatal(err)
	}
	j2 := newJob("j2", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	if err := sched.Submit(context.Background(), j2); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("err=%v want quota exceeded", err)
	}
}

func TestScheduler_MaxNodesQuota(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("n1", Resources{MemoryMB: 8000, Slots: 4, GPUs: 2}, nil))
	pool.Register(newNode("n2", Resources{MemoryMB: 8000, Slots: 4, GPUs: 2}, nil))
	sched := NewScheduler(pool, now, testLogger())
	sched.SetQuota(&Quota{TenantID: "t1", MaxNodes: 1})

	j1 := newJob("j1", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	sched.Submit(context.Background(), j1)
	j2 := newJob("j2", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	sched.Submit(context.Background(), j2)

	if j1.State != JobRunning {
		t.Fatal("j1 should run")
	}
	if j2.NodeID != j1.NodeID {
		t.Fatalf("j2 should reuse j1 node %q within quota, got %q", j1.NodeID, j2.NodeID)
	}
}

// ---- 抢占 ----

func TestScheduler_Preemption(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("n1", Resources{MemoryMB: 4000, Slots: 1, GPUs: 1}, nil))
	sched := NewScheduler(pool, now, testLogger())

	low := newJob("low", "t1", Resources{MemoryMB: 3000, Slots: 1, GPUs: 1}, PriorityLow)
	sched.Submit(context.Background(), low)
	high := newJob("high", "t1", Resources{MemoryMB: 3000, Slots: 1, GPUs: 1}, PriorityCritical)
	sched.Submit(context.Background(), high)

	if low.State != JobPreempted {
		t.Fatalf("low should be preempted, state=%s", low.State)
	}
	if high.State != JobRunning || high.NodeID != "n1" {
		t.Fatal("high should take the node")
	}
}

// ---- 弹性伸缩 ----

func TestAutoScaler_ScaleUp(t *testing.T) {
	now := time.Now
	pool := NewPool(now)
	pool.Register(newNode("n1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, nil))
	sched := NewScheduler(pool, now, testLogger())
	ms := &mockScaler{}
	as := NewAutoScaler(pool, sched, ms, AutoscaleConfig{
		TargetQueueDepth: 0, ScaleUpStep: 2, MaxNodes: 5, ScaleDownIdleTTL: time.Minute,
	}, now)

	j1 := newJob("j1", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	sched.Submit(context.Background(), j1)
	j2 := newJob("j2", "t1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, PriorityNormal)
	sched.Submit(context.Background(), j2) // 排队

	if err := as.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ms.ups != 2 {
		t.Fatalf("scale up=%d want 2", ms.ups)
	}
}

func TestAutoScaler_ScaleDownIdle(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	pool := NewPool(clock.Now)
	pool.Register(newNode("n1", Resources{MemoryMB: 1000, Slots: 1, GPUs: 1}, nil))
	sched := NewScheduler(pool, clock.Now, testLogger())
	ms := &mockScaler{}
	as := NewAutoScaler(pool, sched, ms, AutoscaleConfig{
		TargetQueueDepth: 1, ScaleUpStep: 1, MaxNodes: 10, ScaleDownIdleTTL: time.Minute,
	}, clock.Now)

	if err := as.Reconcile(context.Background()); err != nil { // 记录节点年龄
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	if err := as.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ms.downs) != 1 || ms.downs[0][0] != "n1" {
		t.Fatalf("expected scale down of n1, got %v", ms.downs)
	}
}
