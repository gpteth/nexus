package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"nexus/internal/id"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// statusRecorder 捕获状态码与字节数，同时透传 Flusher 以支持 SSE。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func newRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush 透传底层 Flusher，保证流式响应边收边发。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// chain 从外到内依次为 CORS、恢复、请求 ID、访问日志，
// 确保访问日志执行时请求 ID 已写入 context。
func chain(h http.Handler, logger *slog.Logger, origins []string) http.Handler {
	h = accessLog(h, logger)
	h = requestID(h)
	h = recovery(h, logger)
	h = cors(h, origins)
	return h
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = id.Short()
		}
		w.Header().Set("X-Request-ID", rid)
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func accessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := newRecorder(w)
		next.ServeHTTP(rec, r)
		rid, _ := r.Context().Value(requestIDKey).(string)
		logger.LogAttrs(r.Context(), slog.LevelInfo, "http",
			slog.String("request_id", rid),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)),
			slog.String("remote", r.RemoteAddr),
		)
	})
}

func recovery(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				rid, _ := r.Context().Value(requestIDKey).(string)
				logger.Error("panic recovered", "request_id", rid, "error", rec)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"internal server error","type":"nexus_error"}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func cors(next http.Handler, origins []string) http.Handler {
	allowAll := len(origins) == 0 || containsString(origins, "*")
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}
