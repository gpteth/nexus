package store

import (
	"testing"
	"time"

	"nexus/internal/config"
)

func TestMemoryStore_CRUD(t *testing.T) {
	m := BuildFromConfig(seedConfig())

	k, ok := m.FindAPIKey("sk-nexus-long-secret-12345678")
	if !ok {
		t.Fatal("key not found")
	}
	if k.TenantID != "t1" || len(k.Models) != 1 {
		t.Fatalf("key fields wrong: %+v", k)
	}
	if _, ok := m.FindAPIKey("missing"); ok {
		t.Fatal("missing key should not resolve")
	}
	tn, ok := m.Tenant("t1")
	if !ok || tn.Name != "Tenant1" {
		t.Fatal("tenant not found")
	}

	now := time.Now()
	m.AddUsage(UsageRecord{At: now, TenantID: "t1", TotalTokens: 15, Success: true})
	m.AddUsage(UsageRecord{At: now, TenantID: "other"})
	if recs := m.Usage("t1", time.Time{}); len(recs) != 1 || recs[0].TotalTokens != 15 {
		t.Fatalf("memory usage wrong: %+v", recs)
	}
	if len(m.Usage("t1", now.Add(time.Hour))) != 0 {
		t.Fatal("since filter should exclude all")
	}
}

func TestMemoryStore_DisabledKey(t *testing.T) {
	enabled := false
	cfg := &config.Config{
		Auth: config.AuthConfig{Keys: []config.KeyConfig{{
			Key: "sk-disabled-12345678", Tenant: "t1", Enabled: &enabled,
		}}},
	}
	m := BuildFromConfig(cfg)
	if _, ok := m.FindAPIKey("sk-disabled-12345678"); ok {
		t.Fatal("disabled key should not authenticate")
	}
}
