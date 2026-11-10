package admin

import "time"

// 本文件定义管理面 API 的响应结构，避免直接暴露内部领域类型，
// 并对密钥做掩码、对时间采用标准 RFC3339 序列化。

type overviewDTO struct {
	Tenants      int `json:"tenants"`
	Keys         int `json:"keys"`
	Tickets      int `json:"tickets"`
	OpenTickets  int `json:"open_tickets"`
	Nodes        int `json:"nodes"`
	HealthyNodes int `json:"healthy_nodes"`
	Jobs         int `json:"jobs"`
	RunningJobs  int `json:"running_jobs"`
	QueuedJobs   int `json:"queued_jobs"`
}

type tenantDTO struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	RPM       int     `json:"rpm"`
	BudgetUSD float64 `json:"budget_usd"`
}

type keyDTO struct {
	ID        string   `json:"id"`
	MaskedKey string   `json:"masked_key"`
	TenantID  string   `json:"tenant_id"`
	Enabled   bool     `json:"enabled"`
	RPM       int      `json:"rpm"`
	Models    []string `json:"models"`
}

type ticketDTO struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	Assignments  int       `json:"assignments"`
	LatestReport string    `json:"latest_report"`
	Outcome      string    `json:"outcome"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type resourcesDTO struct {
	MemoryMB int `json:"memory_mb"`
	Slots    int `json:"slots"`
	GPUs     int `json:"gpus"`
}

type nodeDTO struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Endpoint  string            `json:"endpoint"`
	State     string            `json:"state"`
	Capacity  resourcesDTO      `json:"capacity"`
	Used      resourcesDTO      `json:"used"`
	Available resourcesDTO      `json:"available"`
	Labels    map[string]string `json:"labels"`
	LastBeat  time.Time         `json:"last_beat"`
}

type jobDTO struct {
	ID         string       `json:"id"`
	TenantID   string       `json:"tenant_id"`
	Priority   int          `json:"priority"`
	State      string       `json:"state"`
	NodeID     string       `json:"node_id"`
	Request    resourcesDTO `json:"request"`
	EnqueuedAt time.Time    `json:"enqueued_at"`
	StartedAt  time.Time    `json:"started_at"`
	EndedAt    time.Time    `json:"ended_at"`
}
