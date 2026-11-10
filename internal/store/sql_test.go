package store

import (
	"path/filepath"
	"testing"
	"time"

	"nexus/internal/config"

	_ "modernc.org/sqlite"
)

func TestSQLStore_ImplementsStore(t *testing.T) {
	var _ Store = (*SQLStore)(nil)
}

func newTestSQLStore(t *testing.T) *SQLStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewSQLStore("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedConfig() *config.Config {
	return &config.Config{
		Tenants: []config.TenantConfig{{ID: "t1", Name: "Tenant1"}},
		Auth: config.AuthConfig{Keys: []config.KeyConfig{{
			Key:    "sk-nexus-long-secret-12345678",
			Tenant: "t1",
			Models: []string{"model-a"},
		}}},
	}
}

func TestSQLStore_SeedAndFind(t *testing.T) {
	s := newTestSQLStore(t)
	if err := s.Seed(seedConfig()); err != nil {
		t.Fatal(err)
	}

	k, ok := s.FindAPIKey("sk-nexus-long-secret-12345678")
	if !ok {
		t.Fatal("key not found")
	}
	if k.TenantID != "t1" || len(k.Models) != 1 || k.Models[0] != "model-a" {
		t.Fatalf("key fields wrong: %+v", k)
	}
	if _, ok := s.FindAPIKey("missing"); ok {
		t.Fatal("missing key should not resolve")
	}
	tn, ok := s.Tenant("t1")
	if !ok || tn.Name != "Tenant1" {
		t.Fatal("tenant not found")
	}
}

func TestSQLStore_Usage(t *testing.T) {
	s := newTestSQLStore(t)
	now := time.Now()
	s.AddUsage(UsageRecord{At: now, TenantID: "t1", KeyID: "k1", Model: "m",
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Success: true})
	s.AddUsage(UsageRecord{At: now, TenantID: "other", Model: "m"})

	recs := s.Usage("t1", time.Time{})
	if len(recs) != 1 {
		t.Fatalf("got %d records want 1", len(recs))
	}
	if recs[0].TotalTokens != 15 || !recs[0].Success {
		t.Fatal("usage fields wrong")
	}
	if len(s.Usage("t1", now.Add(time.Hour))) != 0 {
		t.Fatal("since filter should exclude all")
	}
}

func TestSQLStore_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")

	s1, err := NewSQLStore("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Seed(seedConfig()); err != nil {
		t.Fatal(err)
	}
	s1.AddUsage(UsageRecord{At: time.Now(), TenantID: "t1", TotalTokens: 42})
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// 重新打开同一文件，租户、密钥与计量都应保留。
	s2, err := NewSQLStore("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	if _, ok := s2.FindAPIKey("sk-nexus-long-secret-12345678"); !ok {
		t.Fatal("key lost after reopen")
	}
	recs := s2.Usage("t1", time.Time{})
	if len(recs) != 1 || recs[0].TotalTokens != 42 {
		t.Fatalf("usage lost after reopen: %+v", recs)
	}
}
