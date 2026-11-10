package admin

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"nexus/internal/config"
	"nexus/internal/security"
)

// Handler 实现管理面 API 与静态控制台托管。
type Handler struct {
	platform *Platform
	jwt      *security.JWTManager
	rbac     *security.RBAC
	users    map[string]config.AdminUser
	logger   *slog.Logger
}

// NewHandler 构造管理面处理器，初始化令牌管理器、RBAC 与登录凭据索引。
func NewHandler(platform *Platform, cfg *config.AdminConfig, logger *slog.Logger) *Handler {
	users := map[string]config.AdminUser{}
	for _, u := range cfg.Users {
		users[u.Username] = u
	}
	return &Handler{
		platform: platform,
		jwt:      security.NewJWTManager([]byte(cfg.JWTSecret), "nexus-admin", cfg.TokenTTL.Duration, time.Now),
		rbac:     security.NewRBAC(),
		users:    users,
		logger:   logger,
	}
}

// Route 在主 mux 上注册登录端点、受 JWT/RBAC 保护的管理 API 与静态控制台。
func (h *Handler) Route(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/login", h.login)

	api := http.NewServeMux()
	api.Handle("GET /api/admin/overview", h.require(security.PermTenantsRead, h.handleOverview))
	api.Handle("GET /api/admin/tenants", h.require(security.PermTenantsRead, h.handleTenants))
	api.Handle("GET /api/admin/keys", h.require(security.PermKeysRead, h.handleKeys))
	api.Handle("GET /api/admin/tickets", h.require(security.PermTicketsRead, h.handleTickets))
	api.Handle("GET /api/admin/nodes", h.require(security.PermNodesRead, h.handleNodes))
	api.Handle("GET /api/admin/jobs", h.require(security.PermNodesRead, h.handleJobs))
	mux.Handle("/api/admin/", h.jwt.Middleware(api))

	h.routeStatic(mux)
}

// require 把 RBAC 授权与具体处理函数组合。
func (h *Handler) require(p security.Permission, fn http.HandlerFunc) http.Handler {
	return h.rbac.RequirePermission(p)(fn)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	user, ok := h.users[req.Username]
	// 常量时间比较，避免计时侧信道；用户不存在时也走一次比较。
	expected := []byte(user.Password)
	if !ok || subtle.ConstantTimeCompare(expected, []byte(req.Password)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := h.jwt.Issue(security.Claims{Subject: user.Username, Roles: user.Roles})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (h *Handler) handleOverview(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.platform.overview())
}

func (h *Handler) handleTenants(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.platform.tenants())
}

func (h *Handler) handleKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.platform.keys())
}

func (h *Handler) handleTickets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.platform.tickets())
}

func (h *Handler) handleNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.platform.nodes())
}

func (h *Handler) handleJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.platform.jobs())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
