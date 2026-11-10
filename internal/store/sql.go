package store

import (
	"database/sql"
	"strings"
	"time"

	"nexus/internal/config"
)

// SQLStore 基于 database/sql 实现 Store，驱动无关，可用于 SQLite/Postgres。
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore 打开数据库、迁移 schema 并返回存储。
func NewSQLStore(driver, dsn string) (*SQLStore, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" {
		// SQLite 单写入者：串行化连接，避免 SQLITE_BUSY。
		db.SetMaxOpenConns(1)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &SQLStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			requests_per_minute INTEGER NOT NULL DEFAULT 0,
			budget_usd REAL NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			key TEXT NOT NULL UNIQUE,
			tenant_id TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			requests_per_minute INTEGER NOT NULL DEFAULT 0,
			models TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS usage_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			tenant_id TEXT NOT NULL,
			key_id TEXT NOT NULL,
			model TEXT NOT NULL,
			upstream TEXT NOT NULL,
			prompt_tokens INTEGER NOT NULL,
			completion_tokens INTEGER NOT NULL,
			total_tokens INTEGER NOT NULL,
			latency_ms INTEGER NOT NULL,
			stream INTEGER NOT NULL,
			success INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_tenant_time ON usage_records(tenant_id, at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Seed 用配置引导租户与密钥，已存在的记录跳过，便于首次启动。
func (s *SQLStore) Seed(cfg *config.Config) error {
	for _, t := range cfg.Tenants {
		if _, err := s.db.Exec(
			`INSERT OR IGNORE INTO tenants (id, name, requests_per_minute, budget_usd) VALUES (?, ?, ?, ?)`,
			t.ID, t.Name, t.RequestsPerMinute, t.BudgetUSD); err != nil {
			return err
		}
	}
	for _, k := range cfg.Auth.Keys {
		enabled := true
		if k.Enabled != nil {
			enabled = *k.Enabled
		}
		id := k.Key
		if len(id) > 12 {
			id = "key_" + k.Key[len(k.Key)-8:]
		}
		if _, err := s.db.Exec(
			`INSERT OR IGNORE INTO api_keys (id, key, tenant_id, enabled, requests_per_minute, models)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, k.Key, k.Tenant, boolInt(enabled), k.RequestsPerMinute, strings.Join(k.Models, ",")); err != nil {
			return err
		}
	}
	return nil
}

// FindAPIKey 按原始密钥查找，未启用或不存在返回 false。
func (s *SQLStore) FindAPIKey(rawKey string) (*APIKey, bool) {
	row := s.db.QueryRow(
		`SELECT id, key, tenant_id, enabled, requests_per_minute, models FROM api_keys WHERE key = ?`,
		rawKey)
	var (
		k       APIKey
		enabled int
		models  string
	)
	if err := row.Scan(&k.ID, &k.Key, &k.TenantID, &enabled, &k.RequestsPerMinute, &models); err != nil {
		return nil, false
	}
	k.Enabled = enabled == 1
	if !k.Enabled {
		return nil, false
	}
	k.Models = splitModels(models)
	return &k, true
}

// Tenant 返回租户。
func (s *SQLStore) Tenant(id string) (*Tenant, bool) {
	var t Tenant
	err := s.db.QueryRow(
		`SELECT id, name, requests_per_minute, budget_usd FROM tenants WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.RequestsPerMinute, &t.BudgetUSD)
	if err != nil {
		return nil, false
	}
	return &t, true
}

// AddUsage 持久化一条计量记录。
func (s *SQLStore) AddUsage(rec UsageRecord) {
	_, _ = s.db.Exec(
		`INSERT INTO usage_records
		 (at, tenant_id, key_id, model, upstream, prompt_tokens, completion_tokens,
		  total_tokens, latency_ms, stream, success)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.At.Unix(), rec.TenantID, rec.KeyID, rec.Model, rec.Upstream,
		rec.PromptTokens, rec.CompletionTokens, rec.TotalTokens, rec.LatencyMS,
		boolInt(rec.Stream), boolInt(rec.Success))
}

// Usage 返回指定租户在 since 之后的用量，按时间顺序。
func (s *SQLStore) Usage(tenantID string, since time.Time) []UsageRecord {
	query := `SELECT at, tenant_id, key_id, model, upstream, prompt_tokens,
		completion_tokens, total_tokens, latency_ms, stream, success
		FROM usage_records WHERE tenant_id = ?`
	args := []any{tenantID}
	if !since.IsZero() {
		query += ` AND at >= ?`
		args = append(args, since.Unix())
	}
	query += ` ORDER BY id ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]UsageRecord, 0)
	for rows.Next() {
		var (
			r               UsageRecord
			at              int64
			stream, success int
		)
		if err := rows.Scan(&at, &r.TenantID, &r.KeyID, &r.Model, &r.Upstream,
			&r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &r.LatencyMS,
			&stream, &success); err != nil {
			continue
		}
		r.At = time.Unix(at, 0)
		r.Stream = stream == 1
		r.Success = success == 1
		out = append(out, r)
	}
	return out
}

// ListTenants 返回全部租户。
func (s *SQLStore) ListTenants() []Tenant {
	rows, err := s.db.Query(`SELECT id, name, requests_per_minute, budget_usd FROM tenants ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]Tenant, 0)
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.RequestsPerMinute, &t.BudgetUSD); err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ListAPIKeys 返回全部密钥。
func (s *SQLStore) ListAPIKeys() []APIKey {
	rows, err := s.db.Query(
		`SELECT id, key, tenant_id, enabled, requests_per_minute, models FROM api_keys ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]APIKey, 0)
	for rows.Next() {
		var (
			k       APIKey
			enabled int
			models  string
		)
		if err := rows.Scan(&k.ID, &k.Key, &k.TenantID, &enabled, &k.RequestsPerMinute, &models); err != nil {
			continue
		}
		k.Enabled = enabled == 1
		k.Models = splitModels(models)
		out = append(out, k)
	}
	return out
}

// Close 关闭底层连接。
func (s *SQLStore) Close() error { return s.db.Close() }

func splitModels(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
