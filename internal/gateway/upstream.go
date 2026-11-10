package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nexus/internal/config"
)

// 哨兵错误，用于区分熔断与容量拒绝。
var (
	ErrBreakerOpen = errors.New("circuit breaker open")
	ErrAtCapacity  = errors.New("upstream at connection capacity")
)

// Upstream 是一个 OpenAI 兼容的模型上游。
type Upstream struct {
	name           string
	baseURL        string
	apiKey         string
	weight         int
	models         []string
	maxConnections int
	healthPath     string
	client         *http.Client
	breaker        *Breaker
	active         int64

	mu      sync.RWMutex
	healthy bool
}

// NewUpstream 依据配置构建上游。
func NewUpstream(cfg config.UpstreamConfig, client *http.Client, breaker *Breaker) *Upstream {
	return &Upstream{
		name:           cfg.Name,
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:         cfg.APIKey,
		weight:         cfg.Weight,
		models:         append([]string(nil), cfg.Models...),
		maxConnections: cfg.MaxConnections,
		healthPath:     cfg.HealthCheckPath,
		client:         client,
		breaker:        breaker,
		healthy:        true,
	}
}

// Name 返回上游名。
func (u *Upstream) Name() string { return u.name }

// Models 返回该上游声明支持的模型。
func (u *Upstream) Models() []string { return u.models }

// Weight 返回负载均衡权重。
func (u *Upstream) Weight() int { return u.weight }

// Active 返回当前在途请求数。
func (u *Upstream) Active() int { return int(atomic.LoadInt64(&u.active)) }

// Healthy 返回最近一次主动健康检查结果。
func (u *Upstream) Healthy() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.healthy
}

func (u *Upstream) setHealthy(v bool) {
	u.mu.Lock()
	u.healthy = v
	u.mu.Unlock()
}

// Breaker 暴露熔断器，供指标采集。
func (u *Upstream) Breaker() *Breaker { return u.breaker }

// Chat 发起一次对话调用。
func (u *Upstream) Chat(ctx context.Context, body []byte) (*RawResponse, error) {
	now := time.Now()
	if !u.breaker.Allow(now) {
		return nil, &UpstreamError{Upstream: u.name, err: ErrBreakerOpen}
	}
	if u.maxConnections > 0 && u.Active() >= u.maxConnections {
		return nil, &UpstreamError{Upstream: u.name, err: ErrAtCapacity}
	}
	atomic.AddInt64(&u.active, 1)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		u.release()
		u.breaker.Record(false, time.Now())
		return nil, &UpstreamError{Upstream: u.name, err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if u.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiKey)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		u.release()
		u.breaker.Record(false, time.Now())
		return nil, &UpstreamError{Upstream: u.name, err: err}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		u.breaker.Record(true, time.Now())
		return &RawResponse{
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Body:       resp.Body,
			upstream:   u.name,
			release:    u.release,
		}, nil
	}

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	_ = resp.Body.Close()
	u.release()
	u.breaker.Record(false, time.Now())
	return nil, &UpstreamError{Upstream: u.name, StatusCode: resp.StatusCode, Body: string(data)}
}

func (u *Upstream) release() {
	atomic.AddInt64(&u.active, -1)
}

// Ping 对上游做一次主动健康探测，默认探测 /models。
func (u *Upstream) Ping(ctx context.Context) bool {
	path := u.healthPath
	if path == "" {
		path = "/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.baseURL+path, nil)
	if err != nil {
		u.setHealthy(false)
		return false
	}
	if u.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+u.apiKey)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		u.setHealthy(false)
		return false
	}
	defer resp.Body.Close()
	ok := resp.StatusCode < 500
	u.setHealthy(ok)
	u.breaker.Record(ok, time.Now())
	return ok
}
