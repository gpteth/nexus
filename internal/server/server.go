// Package server 组装 HTTP 路由、中间件与优雅关停。
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"nexus/internal/config"
	"nexus/internal/gateway"
	"nexus/internal/metrics"
	"nexus/internal/trace"
)

// Server 封装 http.Server 与平台配置。
type Server struct {
	cfg  config.ServerConfig
	http *http.Server
	log  *slog.Logger
}

// New 创建服务器并注册全部路由与中间件。
func New(cfg *config.Config, gh *gateway.Handler, reg *metrics.Registry, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	registerRoutes(mux, cfg, gh, reg)

	handler := chain(mux, logger, cfg.Server.CORSAllowOrigins)
	return &Server{
		cfg: cfg.Server,
		log: logger,
		http: &http.Server{
			Addr:         cfg.Server.Listen,
			Handler:      handler,
			ReadTimeout:  cfg.Server.ReadTimeout.Duration,
			WriteTimeout: cfg.Server.WriteTimeout.Duration,
			IdleTimeout:  cfg.Server.IdleTimeout.Duration,
		},
	}
}

// EnableTracing 在最外层接入服务端追踪中间件，为每个请求建立根 span。
func (s *Server) EnableTracing(tr *trace.Tracer) {
	s.http.Handler = tr.HTTPMiddleware(s.http.Handler)
}

func registerRoutes(mux *http.ServeMux, cfg *config.Config, gh *gateway.Handler, reg *metrics.Registry) {
	mux.HandleFunc("POST /v1/chat/completions", gh.Chat)
	mux.HandleFunc("GET /v1/models", gh.Models)
	mux.HandleFunc("GET /healthz", gh.Liveness)
	mux.HandleFunc("GET /readyz", gh.Readiness)
	if cfg.Metrics.Enabled {
		path := cfg.Metrics.Path
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = reg.WriteTo(w)
		})
	}
}

// Run 启动监听，返回 ErrServerClosed 之外的错误。
func (s *Server) Run() error {
	s.log.Info("nexus listening", "addr", s.http.Addr)
	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown 在配置的超时内优雅关停。
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout.Duration)
	defer cancel()
	return s.http.Shutdown(ctx)
}
