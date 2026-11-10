// Package compute 实现算力调度：把 GPU 节点、推理引擎实例、Serverless 端点
// 抽象为可调度单元，统一进行注册、健康管理、配额、排队、放置、抢占与弹性伸缩。
package compute

import "time"

// Resources 描述一个算力单元的资源画像与占用。
type Resources struct {
	MemoryMB int // 显存/内存（MB）
	Slots    int // 并发槽位
	GPUs     int // GPU 数量
}

// Add 资源相加。
func (r Resources) Add(o Resources) Resources {
	return Resources{MemoryMB: r.MemoryMB + o.MemoryMB, Slots: r.Slots + o.Slots, GPUs: r.GPUs + o.GPUs}
}

// Sub 资源相减。
func (r Resources) Sub(o Resources) Resources {
	return Resources{MemoryMB: r.MemoryMB - o.MemoryMB, Slots: r.Slots - o.Slots, GPUs: r.GPUs - o.GPUs}
}

// FitsIn 判断 r 是否不超过容量 c。
func (r Resources) FitsIn(c Resources) bool {
	return r.MemoryMB <= c.MemoryMB && r.Slots <= c.Slots && r.GPUs <= c.GPUs
}

// NodeKind 是算力单元类型。
type NodeKind string

// 节点类型。
const (
	NodeGPU        NodeKind = "gpu"        // GPU 节点
	NodeInference  NodeKind = "inference"  // 推理引擎实例（vLLM 等）
	NodeServerless NodeKind = "serverless" // Serverless 端点
)

// NodeState 是节点健康状态。
type NodeState string

// 节点状态。
const (
	NodeHealthy   NodeState = "healthy"
	NodeUnhealthy NodeState = "unhealthy"
	NodeDraining  NodeState = "draining" // 优雅下线，不再接收新任务
)

// Node 是一个可调度算力单元。
type Node struct {
	ID       string
	Kind     NodeKind
	Endpoint string
	State    NodeState
	Capacity Resources
	Used     Resources
	Labels   map[string]string
	LastBeat time.Time
}

// Available 返回节点剩余资源。
func (n *Node) Available() Resources { return n.Capacity.Sub(n.Used) }

// schedulable 判断节点是否可承载新任务。
func (n *Node) schedulable() bool { return n.State == NodeHealthy }

// JobState 是任务状态。
type JobState string

// 任务状态。
const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobPreempted JobState = "preempted"
)

// Priority 是任务优先级，数值越大越优先。
type Priority int

// 常用优先级。
const (
	PriorityLow      Priority = 10
	PriorityNormal   Priority = 50
	PriorityHigh     Priority = 90
	PriorityCritical Priority = 100
)

// Job 是一个待调度的推理任务。
type Job struct {
	ID         string
	TenantID   string
	Request    Resources
	Priority   Priority
	Affinity   map[string]string // 要求节点 Labels 全部匹配
	State      JobState
	NodeID     string
	EnqueuedAt time.Time
	StartedAt  time.Time
	EndedAt    time.Time
}

// Quota 是租户级配额。
type Quota struct {
	TenantID      string
	MaxConcurrent int   // 最大并发运行任务数，0 表示不限
	MaxNodes      int   // 最多占用节点数，0 表示不限
	TokenBudget   int64 // token 预算，0 表示不限
	MaxQPS        int   // 每秒请求上限，0 表示不限
}

// QuotaUsage 是租户当前用量。
type QuotaUsage struct {
	Running int
	Nodes   map[string]struct{}
}

// NewQuotaUsage 构造空用量。
func NewQuotaUsage() *QuotaUsage {
	return &QuotaUsage{Nodes: map[string]struct{}{}}
}
