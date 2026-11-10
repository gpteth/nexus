package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nexus/internal/auth"
	"nexus/internal/backoff"
	"nexus/internal/sse"
	"nexus/internal/store"
)

const maxBodyBytes = 10 << 20 // 10 MiB

// Handler 处理 OpenAI 兼容的 HTTP 请求。
type Handler struct {
	gw   *Gateway
	auth *auth.Authenticator
	log  *slog.Logger
}

// NewHandler 创建网关 handler。
func NewHandler(gw *Gateway, logger *slog.Logger) *Handler {
	return &Handler{gw: gw, auth: auth.New(gw.Store()), log: logger}
}

// Chat 处理 POST /v1/chat/completions。
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	// 1. 鉴权
	apiKey, ok := h.auth.Authenticate(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing API key", "unauthorized")
		return
	}

	// 2. 读取并解析请求
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read request body", "bad_request")
		return
	}
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid request: model is required", "bad_request")
		return
	}

	// 3. 模型路由
	group, exists := h.gw.Router().GroupFor(req.Model)
	if !exists {
		writeJSONError(w, http.StatusNotFound, "no upstream configured for model "+req.Model, "model_not_found")
		return
	}
	if len(apiKey.Models) > 0 && !contains(apiKey.Models, req.Model) {
		writeJSONError(w, http.StatusForbidden, "model not allowed for this API key", "forbidden")
		return
	}

	// 4. 限流（key > 租户 > 全局）
	if !h.gw.limiter.Allow(apiKey.ID, h.resolveRPM(apiKey)) {
		writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded", "rate_limit_exceeded")
		return
	}

	// 流式请求注入 include_usage 以便计量。
	outbound := body
	if req.Stream {
		outbound = ensureStreamUsage(body)
	}

	// 5. 带重试地选择并调用上游（尚未向客户端写字节，重试安全）
	raw, err := h.invokeWithRetry(ctx, group, outbound)
	if err != nil {
		status, code := http.StatusBadGateway, "upstream_error"
		var ue *UpstreamError
		if errors.As(err, &ue) {
			switch {
			case errors.Is(ue.err, ErrBreakerOpen), errors.Is(ue.err, ErrAtCapacity), errors.Is(err, ErrNoAvailableUpstream):
				status, code = http.StatusServiceUnavailable, "no_capacity"
			case ue.StatusCode == http.StatusTooManyRequests:
				status, code = http.StatusTooManyRequests, "upstream_rate_limited"
			}
		}
		h.record(apiKey, req.Model, "", Usage{}, req.Stream, false, start)
		writeJSONError(w, status, err.Error(), code)
		return
	}
	defer raw.Release()

	// 6. 处理响应（此时才开始写客户端）
	var usage Usage
	if req.Stream {
		usage = h.serveStream(w, raw)
	} else {
		usage = h.serveBuffered(w, raw)
	}
	h.record(apiKey, req.Model, raw.UpstreamName(), usage, req.Stream, true, start)
}

// invokeWithRetry 在组内选择上游并对可重试错误进行退避重试。
func (h *Handler) invokeWithRetry(ctx context.Context, group *Group, body []byte) (*RawResponse, error) {
	cfg := h.gw.cfg.Gateway
	var lastErr error
	for attempt := 0; attempt < cfg.Retry.MaxAttempts; attempt++ {
		up, pickErr := group.Pick()
		if pickErr == nil {
			resp, callErr := up.Chat(ctx, body)
			if callErr == nil {
				return resp, nil
			}
			lastErr = callErr
			var ue *UpstreamError
			if !errors.As(callErr, &ue) || !ue.Retryable() {
				return nil, callErr
			}
		} else {
			lastErr = pickErr
		}
		if attempt < cfg.Retry.MaxAttempts-1 {
			d := backoff.ExponentialJitter(attempt, cfg.Retry.BaseDelay.Duration, cfg.Retry.MaxDelay.Duration)
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}

// serveBuffered 处理非流式响应，整体回传并提取用量。
func (h *Handler) serveBuffered(w http.ResponseWriter, raw *RawResponse) Usage {
	data, err := io.ReadAll(raw.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed reading upstream response", "upstream_error")
		return Usage{}
	}
	var chat ChatResponse
	_ = json.Unmarshal(data, &chat)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(raw.StatusCode)
	_, _ = w.Write(data)
	if chat.Usage != nil {
		return *chat.Usage
	}
	return Usage{}
}

// serveStream 透传 SSE 事件并在流中提取用量。
func (h *Handler) serveStream(w http.ResponseWriter, raw *RawResponse) Usage {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported by server", "internal")
		return Usage{}
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	reader := sse.NewReader(raw.Body)
	writer := sse.NewWriter(w, flusher)
	var usage Usage
	for {
		block, has, err := reader.Next()
		if err != nil || !has {
			break
		}
		if strings.HasPrefix(block, "data:") && !sse.IsDone(block) {
			payload := strings.TrimSpace(strings.TrimPrefix(block, "data:"))
			var chunk StreamChunk
			if json.Unmarshal([]byte(payload), &chunk) == nil && chunk.Usage != nil {
				usage = *chunk.Usage
			}
		}
		if err := writer.WriteEvent(block); err != nil {
			// 客户端断开，停止透传。
			break
		}
	}
	return usage
}

// Models 处理 GET /v1/models。
func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	data := make([]ModelInfo, 0)
	for _, m := range h.gw.Router().Models() {
		data = append(data, ModelInfo{ID: m, Object: "model", Created: now, OwnedBy: "nexus"})
	}
	writeJSON(w, http.StatusOK, ModelsResponse{Object: "list", Data: data})
}

// Liveness 处理存活探针。
func (h *Handler) Liveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readiness 处理就绪探针：至少一个上游健康才就绪。
func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	for _, u := range h.gw.Upstreams() {
		if u.Healthy() {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
}

// CollectGauges 刷新上游并发、健康与熔断状态仪表盘。
func (h *Handler) CollectGauges() {
	gm := h.gw.Metrics()
	for _, u := range h.gw.Upstreams() {
		name := u.Name()
		gm.active.Set(float64(u.Active()), name)
		if u.Healthy() {
			gm.healthy.Set(1, name)
		} else {
			gm.healthy.Set(0, name)
		}
		state := u.Breaker().State().String()
		for _, s := range []string{"closed", "open", "half_open"} {
			val := 0
			if s == state {
				val = 1
			}
			gm.brkState.Set(float64(val), name, s)
		}
	}
}

func (h *Handler) resolveRPM(k *store.APIKey) int {
	if k.RequestsPerMinute > 0 {
		return k.RequestsPerMinute
	}
	if k.TenantID != "" {
		if t, ok := h.gw.Store().Tenant(k.TenantID); ok && t.RequestsPerMinute > 0 {
			return t.RequestsPerMinute
		}
	}
	return h.gw.cfg.Gateway.RateLimit.RequestsPerMinute
}

func (h *Handler) record(k *store.APIKey, model, upstream string, usage Usage, stream, success bool, start time.Time) {
	latency := time.Since(start)
	status := "success"
	if !success {
		status = "failure"
	}
	gm := h.gw.Metrics()
	gm.requests.Inc(k.TenantID, model, upstream, status)
	gm.duration.Observe(latency.Seconds(), k.TenantID, model, upstream)
	if usage.PromptTokens > 0 {
		gm.tokens.Add(float64(usage.PromptTokens), k.TenantID, model, "prompt")
	}
	if usage.CompletionTokens > 0 {
		gm.tokens.Add(float64(usage.CompletionTokens), k.TenantID, model, "completion")
	}
	h.gw.Store().AddUsage(store.UsageRecord{
		At:               time.Now(),
		TenantID:         k.TenantID,
		KeyID:            k.ID,
		Model:            model,
		Upstream:         upstream,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		LatencyMS:        latency.Milliseconds(),
		Stream:           stream,
		Success:          success,
	})
}

func ensureStreamUsage(body []byte) []byte {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	if _, ok := m["stream_options"]; !ok {
		m["stream_options"] = map[string]bool{"include_usage": true}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, newErrorResponse(message, code))
}

func contains(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}
