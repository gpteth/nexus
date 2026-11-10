package compute

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Scheduler 是算力调度器：维护优先级队列与配额，基于 filter/score 做放置，
// 资源不足时支持高优先级抢占，并在任务完成后回收资源、继续调度排队任务。
type Scheduler struct {
	pool   *Pool
	mu     sync.Mutex
	queue  []*Job
	jobs   map[string]*Job
	quotas map[string]*Quota
	usage  map[string]*QuotaUsage
	now    func() time.Time
	logger *slog.Logger
}

// NewScheduler 构造调度器。
func NewScheduler(pool *Pool, now func() time.Time, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		pool:   pool,
		jobs:   map[string]*Job{},
		quotas: map[string]*Quota{},
		usage:  map[string]*QuotaUsage{},
		now:    now,
		logger: logger,
	}
}

// SetQuota 设置租户配额。
func (s *Scheduler) SetQuota(q *Quota) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quotas[q.TenantID] = q
}

func (s *Scheduler) usageFor(tenantID string) *QuotaUsage {
	u, ok := s.usage[tenantID]
	if !ok {
		u = NewQuotaUsage()
		s.usage[tenantID] = u
	}
	return u
}

// Submit 校验并发配额后将任务入队并立即尝试调度。
func (s *Scheduler) Submit(ctx context.Context, j *Job) error {
	s.mu.Lock()
	if _, ok := s.jobs[j.ID]; ok {
		s.mu.Unlock()
		return ErrDuplicate
	}
	if err := s.checkConcurrent(j); err != nil {
		s.mu.Unlock()
		return err
	}
	if j.EnqueuedAt.IsZero() {
		j.EnqueuedAt = s.now()
	}
	j.State = JobQueued
	s.jobs[j.ID] = j
	s.queue = append(s.queue, j)
	s.mu.Unlock()

	s.Schedule(ctx)
	return nil
}

func (s *Scheduler) checkConcurrent(j *Job) error {
	q, ok := s.quotas[j.TenantID]
	if !ok || q.MaxConcurrent == 0 {
		return nil
	}
	if s.usageFor(j.TenantID).Running >= q.MaxConcurrent {
		return ErrQuotaExceeded
	}
	return nil
}

// Schedule 按优先级顺序尝试放置队列中的任务，返回新放置数量。
func (s *Scheduler) Schedule(ctx context.Context) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	sort.SliceStable(s.queue, func(i, j int) bool {
		if s.queue[i].Priority != s.queue[j].Priority {
			return s.queue[i].Priority > s.queue[j].Priority
		}
		return s.queue[i].EnqueuedAt.Before(s.queue[j].EnqueuedAt)
	})

	placed := 0
	remaining := make([]*Job, 0, len(s.queue))
	for _, job := range s.queue {
		if s.place(job) {
			placed++
		} else if s.preempt(job) {
			placed++
		} else {
			remaining = append(remaining, job)
		}
	}
	s.queue = remaining
	return placed
}

// place 用 filter+score 选择节点并绑定；失败返回 false。
func (s *Scheduler) place(job *Job) bool {
	candidates := make([]*Node, 0)
	for _, n := range s.pool.Schedulable() {
		if !job.Request.FitsIn(n.Available()) {
			continue
		}
		if !affinityMatch(job.Affinity, n.Labels) {
			continue
		}
		if !s.nodeAllowedByQuota(job, n) {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) == 0 {
		return false
	}
	// bin-packing：优先选剩余资源最少的节点，提高利用率、为缩容创造条件。
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Available().MemoryMB < candidates[j].Available().MemoryMB
	})
	s.bind(job, candidates[0])
	return true
}

// preempt 让高优先级任务抢占同节点上更低优先级的运行任务。
func (s *Scheduler) preempt(job *Job) bool {
	for _, n := range s.pool.Schedulable() {
		if !affinityMatch(job.Affinity, n.Labels) || !s.nodeAllowedByQuota(job, n) {
			continue
		}
		victim := s.lowestVictim(n.ID, job.Priority)
		if victim == nil {
			continue
		}
		if !job.Request.FitsIn(n.Available().Add(victim.Request)) {
			continue
		}
		s.evict(victim)
		s.bind(job, n)
		return true
	}
	return false
}

// lowestVictim 返回节点上优先级低于阈值的最低优先级运行任务。
func (s *Scheduler) lowestVictim(nodeID string, threshold Priority) *Job {
	var victim *Job
	for _, j := range s.jobs {
		if j.State != JobRunning || j.NodeID != nodeID || j.Priority >= threshold {
			continue
		}
		if victim == nil || j.Priority < victim.Priority {
			victim = j
		}
	}
	return victim
}

func (s *Scheduler) nodeAllowedByQuota(job *Job, n *Node) bool {
	q, ok := s.quotas[job.TenantID]
	if !ok || q.MaxNodes == 0 {
		return true
	}
	u := s.usageFor(job.TenantID)
	if _, used := u.Nodes[n.ID]; used {
		return true
	}
	return len(u.Nodes) < q.MaxNodes
}

func (s *Scheduler) bind(job *Job, n *Node) {
	n.Used = n.Used.Add(job.Request)
	job.State = JobRunning
	job.NodeID = n.ID
	job.StartedAt = s.now()
	u := s.usageFor(job.TenantID)
	u.Running++
	u.Nodes[n.ID] = struct{}{}
}

func (s *Scheduler) evict(job *Job) {
	if n, err := s.pool.Get(job.NodeID); err == nil {
		n.Used = n.Used.Sub(job.Request)
	}
	job.State = JobPreempted
	job.NodeID = ""
	job.EndedAt = s.now()
	s.usageFor(job.TenantID).Running--
}

// Complete 回收运行任务占用的资源，并继续调度排队任务。
func (s *Scheduler) Complete(ctx context.Context, jobID string, state JobState) error {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	if job.State != JobRunning {
		s.mu.Unlock()
		return ErrInvalidState
	}
	if n, err := s.pool.Get(job.NodeID); err == nil {
		n.Used = n.Used.Sub(job.Request)
	}
	job.State = state
	job.EndedAt = s.now()
	s.usageFor(job.TenantID).Running--
	s.mu.Unlock()

	s.Schedule(ctx)
	return nil
}

// GetJob 返回任务。
func (s *Scheduler) GetJob(id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return j, nil
}

// Queued 返回当前排队任务数。
func (s *Scheduler) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Jobs 返回全部任务快照（含排队/运行/终态），供管理控制台读取。
func (s *Scheduler) Jobs() []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j)
	}
	return out
}

func affinityMatch(required, labels map[string]string) bool {
	for k, v := range required {
		if labels[k] != v {
			return false
		}
	}
	return true
}
