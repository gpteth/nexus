package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nexus/internal/config"
	"nexus/internal/metrics"
	"nexus/internal/store"
)

func dur(d time.Duration) config.Duration { return config.Duration{Duration: d} }

func testConfig(upstreamURLs ...string) *config.Config {
	enabled := true
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			DefaultTimeout: dur(10 * time.Second),
			Retry: config.RetryConfig{
				MaxAttempts: 3,
				BaseDelay:   dur(10 * time.Millisecond),
				MaxDelay:    dur(50 * time.Millisecond),
			},
			Breaker: config.BreakerConfig{
				FailureThreshold: 1, // 便于故障转移测试：一次失败即熔断
				OpenDuration:     dur(time.Second),
				HalfOpenProbes:   1,
			},
		},
		Auth: config.AuthConfig{Keys: []config.KeyConfig{
			{Key: "sk-nexus-test", Tenant: "default", Enabled: &enabled},
		}},
		Tenants: []config.TenantConfig{{ID: "default", Name: "Default"}},
	}
	for i, u := range upstreamURLs {
		cfg.Gateway.Upstreams = append(cfg.Gateway.Upstreams, config.UpstreamConfig{
			Name:    fmt.Sprintf("up%d", i),
			Type:    "openai",
			BaseURL: u,
			APIKey:  "up-secret",
			Weight:  1,
			Models:  []string{"test-model"},
		})
	}
	return cfg
}

func newTestHandler(cfg *config.Config) (*Handler, *Gateway) {
	reg := metrics.NewRegistry()
	st := store.BuildFromConfig(cfg)
	gw := New(cfg, slog.Default(), st, reg)
	return NewHandler(gw, slog.Default()), gw
}

func chatBody() string {
	return `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":%t}`
}

func doChat(h *Handler, body, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.Chat(rec, req)
	return rec
}

func TestChatNonStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer up-secret" {
			t.Errorf("upstream auth header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"test-model","usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`))
	}))
	defer upstream.Close()

	h, gw := newTestHandler(testConfig(upstream.URL))
	rec := doChat(h, fmt.Sprintf(chatBody(), false), "Bearer sk-nexus-test")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 30 {
		t.Fatalf("usage not extracted: %+v", resp.Usage)
	}
	// 计量应已落库
	records := gw.Store().Usage("default", time.Time{})
	if len(records) != 1 || records[0].TotalTokens != 30 {
		t.Fatalf("usage not recorded: %+v", records)
	}
}

func TestChatStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"c1\",\"choices\":[]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"c1\",\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7,\"total_tokens\":12}}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	h, _ := newTestHandler(testConfig(upstream.URL))
	rec := doChat(h, fmt.Sprintf(chatBody(), true), "Bearer sk-nexus-test")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("not SSE: %s", rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("missing DONE marker:\n%s", body)
	}
	if !strings.Contains(body, `"total_tokens":12`) {
		t.Fatalf("stream usage not passed through:\n%s", body)
	}
}

func TestAuthMissing(t *testing.T) {
	h, _ := newTestHandler(testConfig("http://127.0.0.1:1"))
	rec := doChat(h, fmt.Sprintf(chatBody(), false), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestModelNotFound(t *testing.T) {
	cfg := testConfig("http://127.0.0.1:1")
	h, _ := newTestHandler(cfg)
	body := `{"model":"unknown","messages":[]}`
	rec := doChat(h, body, "Bearer sk-nexus-test")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestFailoverToHealthyUpstream(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c2","object":"chat.completion","model":"test-model","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer bad.Close()
	defer good.Close()

	h, _ := newTestHandler(testConfig(bad.URL, good.URL))
	rec := doChat(h, fmt.Sprintf(chatBody(), false), "Bearer sk-nexus-test")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected failover to 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"c2"`) {
		t.Fatalf("response did not come from healthy upstream: %s", rec.Body.String())
	}
}

func TestModelsEndpoint(t *testing.T) {
	h, _ := newTestHandler(testConfig("http://127.0.0.1:1"))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.Models(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var mr ModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mr); err != nil {
		t.Fatal(err)
	}
	if len(mr.Data) != 1 || mr.Data[0].ID != "test-model" {
		t.Fatalf("unexpected models: %+v", mr.Data)
	}
}
