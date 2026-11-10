// Package auth 从请求头解析并校验平台 API Key。
package auth

import (
	"net/http"
	"strings"

	"nexus/internal/store"
)

// Authenticator 基于存储校验数据面密钥。
type Authenticator struct {
	store store.Store
}

// New 创建鉴权器。
func New(s store.Store) *Authenticator { return &Authenticator{store: s} }

// Authenticate 校验 Authorization: Bearer <key>，返回对应密钥实体。
func (a *Authenticator) Authenticate(r *http.Request) (*store.APIKey, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return nil, false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if raw == "" {
		return nil, false
	}
	return a.store.FindAPIKey(raw)
}
