package trace

import (
	"context"
	"sync"
	"time"
)

type contextKey int

const spanKey contextKey = iota

// SpanOption 配置新跨度。
type SpanOption func(*Span)

// WithKind 设置跨度类型。
func WithKind(k SpanKind) SpanOption {
	return func(s *Span) { s.data.Kind = k }
}

// Tracer 创建跨度并经导出器上报。
type Tracer struct {
	exporter SpanExporter
	now      func() time.Time
}

// NewTracer 构造追踪器。
func NewTracer(exp SpanExporter, now func() time.Time) *Tracer {
	return &Tracer{exporter: exp, now: now}
}

// Span 是一个活动跨度。
type Span struct {
	mu       sync.Mutex
	data     SpanData
	exporter SpanExporter
	now      func() time.Time
}

// Start 创建跨度：ctx 含父跨度则挂为子节点，否则新建根跨度，并把新跨度写入返回的 ctx。
func (t *Tracer) Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, *Span) {
	var traceID TraceID
	var parentID SpanID
	if p, ok := spanFromContext(ctx); ok {
		traceID = p.data.SpanContext.TraceID
		parentID = p.data.SpanContext.SpanID
	} else if rc, ok := remoteParent(ctx); ok {
		traceID = rc.TraceID
		parentID = rc.SpanID
	} else {
		traceID = newTraceID()
	}

	span := &Span{
		data: SpanData{
			Name: name,
			SpanContext: SpanContext{
				TraceID: traceID,
				SpanID:  newSpanID(),
				Sampled: true,
			},
			ParentSpanID: parentID,
			Kind:         SpanInternal,
			Start:        t.now(),
			Attributes:   map[string]string{},
		},
		exporter: t.exporter,
		now:      t.now,
	}
	for _, o := range opts {
		o(span)
	}
	return context.WithValue(ctx, spanKey, span), span
}

// End 结束跨度并导出快照。
func (s *Span) End() {
	s.mu.Lock()
	if s.data.End.IsZero() {
		s.data.End = s.now()
	}
	data := s.snapshotLocked()
	s.mu.Unlock()
	_ = s.exporter.ExportSpans(context.Background(), []SpanData{data})
}

// SetAttribute 记录跨度属性。
func (s *Span) SetAttribute(k, v string) {
	s.mu.Lock()
	s.data.Attributes[k] = v
	s.mu.Unlock()
}

// SetStatus 记录状态码（如 HTTP 状态）。
func (s *Span) SetStatus(code int) {
	s.mu.Lock()
	s.data.StatusCode = code
	s.mu.Unlock()
}

// Context 返回跨度上下文。
func (s *Span) Context() SpanContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.SpanContext
}

func (s *Span) snapshotLocked() SpanData {
	attrs := make(map[string]string, len(s.data.Attributes))
	for k, v := range s.data.Attributes {
		attrs[k] = v
	}
	d := s.data
	d.Attributes = attrs
	return d
}

func spanFromContext(ctx context.Context) (*Span, bool) {
	s, ok := ctx.Value(spanKey).(*Span)
	return s, ok
}

// SpanFromContext 从 context 取出活动跨度。
func SpanFromContext(ctx context.Context) (*Span, bool) { return spanFromContext(ctx) }

// Shutdown 关闭底层导出器。
func (t *Tracer) Shutdown(ctx context.Context) error { return t.exporter.Shutdown(ctx) }
