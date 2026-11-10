package store

import (
	"sync"
	"time"

	"nexus/internal/config"
)

// MemoryStore 是线程安全的内存存储，适合单实例运行与测试。
type MemoryStore struct {
	mu      sync.RWMutex
	keys    map[string]*APIKey
	tenants map[string]*Tenant
	usage   []UsageRecord
}

// NewMemoryStore 创建空内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{keys: map[string]*APIKey{}, tenants: map[string]*Tenant{}}
}

// BuildFromConfig 依据配置填充租户与密钥。
func BuildFromConfig(cfg *config.Config) *MemoryStore {
	m := NewMemoryStore()
	for _, t := range cfg.Tenants {
		m.tenants[t.ID] = &Tenant{
			ID:                t.ID,
			Name:              t.Name,
			RequestsPerMinute: t.RequestsPerMinute,
			BudgetUSD:         t.BudgetUSD,
		}
	}
	for i, k := range cfg.Auth.Keys {
		enabled := true
		if k.Enabled != nil {
			enabled = *k.Enabled
		}
		id := k.Key
		if len(id) > 12 {
			// 用密钥自身的稳定片段作为内部 ID，避免在日志中暴露完整密钥。
			id = "key_" + k.Key[len(k.Key)-8:]
		}
		_ = i
		m.keys[k.Key] = &APIKey{
			ID:                id,
			Key:               k.Key,
			TenantID:          k.Tenant,
			Enabled:           enabled,
			RequestsPerMinute: k.RequestsPerMinute,
			Models:            append([]string(nil), k.Models...),
		}
	}
	return m
}

// FindAPIKey 按原始密钥查找，未启用或不存在返回 false。
func (m *MemoryStore) FindAPIKey(rawKey string) (*APIKey, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k, ok := m.keys[rawKey]
	if !ok || !k.Enabled {
		return nil, false
	}
	cp := *k
	cp.Models = append([]string(nil), k.Models...)
	return &cp, true
}

// Tenant 返回租户。
func (m *MemoryStore) Tenant(id string) (*Tenant, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tenants[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// AddUsage 追加一条用量记录。
func (m *MemoryStore) AddUsage(rec UsageRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage = append(m.usage, rec)
}

// Usage 返回指定租户在 since 之后的用量，按时间顺序。
func (m *MemoryStore) Usage(tenantID string, since time.Time) []UsageRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]UsageRecord, 0, len(m.usage))
	for _, r := range m.usage {
		if r.TenantID == tenantID && (since.IsZero() || !r.At.Before(since)) {
			out = append(out, r)
		}
	}
	return out
}
