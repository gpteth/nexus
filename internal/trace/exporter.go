package trace

import (
	"context"
	"encoding/json"
	"io"
	"sync"
)

// SpanExporter 是跨度导出端口：真实环境可实现 OTLP/HTTP 对接 Jaeger/Tempo/Collector。
type SpanExporter interface {
	ExportSpans(ctx context.Context, spans []SpanData) error
	Shutdown(ctx context.Context) error
}

// StdoutExporter 以 JSON Lines 输出跨度，线程安全，便于本地调试与日志采集。
type StdoutExporter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewStdoutExporter 构造标准输出导出器。
func NewStdoutExporter(w io.Writer) *StdoutExporter { return &StdoutExporter{w: w} }

// ExportSpans 逐行写出跨度。
func (e *StdoutExporter) ExportSpans(_ context.Context, spans []SpanData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	enc := json.NewEncoder(e.w)
	for i := range spans {
		if err := enc.Encode(spans[i]); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown 无缓冲需要刷新。
func (e *StdoutExporter) Shutdown(_ context.Context) error { return nil }

// MemoryExporter 在内存累积跨度，主要用于测试断言。
type MemoryExporter struct {
	mu    sync.Mutex
	spans []SpanData
}

// NewMemoryExporter 构造内存导出器。
func NewMemoryExporter() *MemoryExporter { return &MemoryExporter{} }

// ExportSpans 追加跨度快照。
func (e *MemoryExporter) ExportSpans(_ context.Context, spans []SpanData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, cloneSpans(spans)...)
	return nil
}

// Shutdown 空实现。
func (e *MemoryExporter) Shutdown(_ context.Context) error { return nil }

// Spans 返回已导出跨度快照。
func (e *MemoryExporter) Spans() []SpanData {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneSpans(e.spans)
}

func cloneSpans(in []SpanData) []SpanData {
	out := make([]SpanData, len(in))
	for i, s := range in {
		attrs := make(map[string]string, len(s.Attributes))
		for k, v := range s.Attributes {
			attrs[k] = v
		}
		s.Attributes = attrs
		out[i] = s
	}
	return out
}
