// Package store 定义持久化端口与内存实现，后续可替换为 SQLite/Postgres 适配器。
package store

import "time"

// APIKey 是访问平台数据面的密钥及其策略。
type APIKey struct {
	ID                string
	Key               string
	TenantID          string
	Enabled           bool
	RequestsPerMinute int      // 0 表示继承租户/全局默认
	Models            []string // 空表示不限制
}

// Tenant 是资源与配额的隔离单元。
type Tenant struct {
	ID                string
	Name              string
	RequestsPerMinute int
	BudgetUSD         float64
}

// UsageRecord 是一次模型调用的计量记录。
type UsageRecord struct {
	At               time.Time
	TenantID         string
	KeyID            string
	Model            string
	Upstream         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	LatencyMS        int64
	Stream           bool
	Success          bool
}

// Store 是存储端口。方法接收 context 以便未来的数据库实现。
type Store interface {
	FindAPIKey(rawKey string) (*APIKey, bool)
	Tenant(id string) (*Tenant, bool)
	AddUsage(rec UsageRecord)
	Usage(tenantID string, since time.Time) []UsageRecord
}
