package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"nexus/internal/config"
	"nexus/internal/metrics"
	"nexus/internal/store"
	"nexus/internal/trace"
)

// GatewayMetrics 聚合网关相关指标。
type GatewayMetrics struct {
	requests *metrics.CounterVec
	duration *metrics.HistogramVec
	tokens   *metrics.CounterVec
	active   *metrics.GaugeVec
	healthy  *metrics.GaugeVec
	brkState *metrics.GaugeVec
}

// Gateway 是统一 LLM 网关的核心，持有路由、上游与治理组件。
type Gateway struct {
	cfg       *config.Config
	logger    *slog.Logger
	store     store.Store
	upstreams []*Upstream
	router    *ModelRouter
	limiter   *RateLimiter
	client    *http.Client
	metrics   GatewayMetrics
}

// New 依据配置组装网关并把指标注册到 reg。
func New(cfg *config.Config, logger *slog.Logger, st store.Store, reg *metrics.Registry) *Gateway {
	client := &http.Client{Transport: newTransport()}

	g := &Gateway{
		cfg:     cfg,
		logger:  logger,
		store:   st,
		router:  NewModelRouter(),
		limiter: NewRateLimiter(),
		client:  client,
	}
	g.buildUpstreams(reg)
	g.metrics = registerMetrics(reg)
	return g
}

// EnableTracing 用追踪器包装上游 HTTP 传输，使每个上游调用产生 client span。
func (g *Gateway) EnableTracing(tr *trace.Tracer) {
	g.client.Transport = tr.WrapTransport(g.client.Transport)
}

func newTransport() *http.Transport {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          2048,
		MaxIdleConnsPerHost:   256,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return t
}

func (g *Gateway) buildUpstreams(reg *metrics.Registry) {
	byName := map[string]*Upstream{}
	for _, uc := range g.cfg.Gateway.Upstreams {
		brk := NewBreaker(
			g.cfg.Gateway.Breaker.FailureThreshold,
			g.cfg.Gateway.Breaker.OpenDuration.Duration,
			g.cfg.Gateway.Breaker.HalfOpenProbes,
		)
		u := NewUpstream(uc, g.client, brk)
		g.upstreams = append(g.upstreams, u)
		byName[u.Name()] = u
	}

	explicit := map[string]bool{}
	// 显式路由
	for _, rc := range g.cfg.Gateway.Routes {
		ups := make([]*Upstream, 0, len(rc.Upstreams))
		for _, name := range rc.Upstreams {
			if u, ok := byName[name]; ok {
				ups = append(ups, u)
			}
		}
		strategy := rc.Strategy
		if strategy == "" {
			strategy = "weighted_round_robin"
		}
		g.router.Set(rc.Model, NewGroup(strategy, ups...))
		explicit[rc.Model] = true
	}

	// 自动路由：按上游声明的 models 聚合等价上游。
	modelPeers := map[string][]*Upstream{}
	for _, u := range g.upstreams {
		for _, m := range u.Models() {
			modelPeers[m] = append(modelPeers[m], u)
		}
	}
	for m, ups := range modelPeers {
		if explicit[m] {
			continue
		}
		g.router.Set(m, NewGroup("weighted_round_robin", ups...))
	}
}

func registerMetrics(reg *metrics.Registry) GatewayMetrics {
	gm := GatewayMetrics{
		requests: metrics.NewCounterVec("nexus_requests_total", "Total chat requests", "tenant", "model", "upstream", "status"),
		duration: metrics.NewHistogramVec("nexus_request_duration_seconds", "Request latency",
			[]float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}, "tenant", "model", "upstream"),
		tokens:   metrics.NewCounterVec("nexus_tokens_total", "Tokens processed", "tenant", "model", "kind"),
		active:   metrics.NewGaugeVec("nexus_upstream_active_requests", "In-flight requests per upstream", "upstream"),
		healthy:  metrics.NewGaugeVec("nexus_upstream_healthy", "Upstream health from active probe", "upstream"),
		brkState: metrics.NewGaugeVec("nexus_breaker_state", "Circuit breaker state (1 = current)", "upstream", "state"),
	}
	reg.MustRegister(gm.requests)
	reg.MustRegister(gm.duration)
	reg.MustRegister(gm.tokens)
	reg.MustRegister(gm.active)
	reg.MustRegister(gm.healthy)
	reg.MustRegister(gm.brkState)
	return gm
}

// StartHealthChecks 启动周期性主动健康探测，直到 ctx 取消。
func (g *Gateway) StartHealthChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	probe := func() {
		for _, u := range g.upstreams {
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = u.Ping(pingCtx)
			cancel()
		}
	}
	probe()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probe()
		}
	}
}

// Router 暴露模型路由表，供 handler 使用。
func (g *Gateway) Router() *ModelRouter { return g.router }

// Store 暴露存储。
func (g *Gateway) Store() store.Store { return g.store }

// Metrics 暴露指标容器。
func (g *Gateway) Metrics() GatewayMetrics { return g.metrics }

// Upstreams 暴露所有上游。
func (g *Gateway) Upstreams() []*Upstream { return g.upstreams }
