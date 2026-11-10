package trace

import (
	"net/http"
	"strconv"
)

// HTTPMiddleware 服务端埋点：提取远程父跨度或新建根 server span，请求结束后导出。
func (t *Tracer) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := Extract(r.Context(), r.Header)
		ctx, span := t.Start(ctx, r.Method+" "+r.URL.Path, WithKind(SpanServer))
		span.SetAttribute("http.method", r.Method)
		span.SetAttribute("http.route", r.URL.Path)

		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		span.SetStatus(rec.status)
		span.SetAttribute("http.status_code", strconv.Itoa(rec.status))
		span.End()
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush 透传底层 Flusher，保证 SSE 流式响应不受影响。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// tracingTransport 客户端埋点：为上游请求创建 client span 并注入 traceparent。
type tracingTransport struct {
	base   http.RoundTripper
	tracer *Tracer
}

// WrapTransport 包装 RoundTripper，nil 时使用默认传输。
func (t *Tracer) WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &tracingTransport{base: base, tracer: t}
}

func (tr *tracingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx, span := tr.tracer.Start(r.Context(), "HTTP "+r.Method+" "+r.URL.Host, WithKind(SpanClient))
	span.SetAttribute("http.url", r.URL.String())
	span.SetAttribute("net.peer.name", r.URL.Host)
	Inject(ctx, r.Header)

	resp, err := tr.base.RoundTrip(r.WithContext(ctx))
	if err != nil {
		span.End()
		return resp, err
	}
	span.SetStatus(resp.StatusCode)
	span.SetAttribute("http.status_code", strconv.Itoa(resp.StatusCode))
	span.End()
	return resp, err
}
