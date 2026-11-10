package security

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const claimsKey ctxKey = iota

// Middleware 校验 Authorization: Bearer <jwt>，通过后把 Claims 注入请求 context。
func (m *JWTManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := m.Verify(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
	})
}

// RequirePermission 返回授权中间件：要求 Middleware 已注入 Claims 且角色具备该权限。
func (r *RBAC) RequirePermission(p Permission) func(http.Handler) http.Handler {
	rbac := r
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			claims, ok := ClaimsFromContext(req.Context())
			if !ok || !rbac.Allowed(claims.Roles, p) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// ClaimsFromContext 从 context 取出 Claims。
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}
