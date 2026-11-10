// Package trace 实现轻量分布式追踪：W3C Trace Context 传播、跨度树、
// context 注入与可插拔导出器。零第三方依赖，可与标准追踪系统经 exporter 互操作。
package trace

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// TraceID 是 16 字节的全局追踪标识。
type TraceID [16]byte

// SpanID 是 8 字节的跨度标识。
type SpanID [8]byte

func (t TraceID) String() string { return hex.EncodeToString(t[:]) }
func (s SpanID) String() string  { return hex.EncodeToString(s[:]) }

// SpanContext 携带跨进程传播所需的标识与采样标志。
type SpanContext struct {
	TraceID TraceID
	SpanID  SpanID
	Sampled bool
}

// IsValid 判断追踪标识是否有效（非全零）。
func (c SpanContext) IsValid() bool {
	var zero TraceID
	return c.TraceID != zero
}

// SpanKind 描述跨度角色。
type SpanKind int

// 跨度类型。
const (
	SpanInternal SpanKind = iota
	SpanServer
	SpanClient
)

func (k SpanKind) String() string {
	switch k {
	case SpanServer:
		return "server"
	case SpanClient:
		return "client"
	default:
		return "internal"
	}
}

// SpanData 是导出时的不可变快照。
type SpanData struct {
	Name         string
	SpanContext  SpanContext
	ParentSpanID SpanID
	Kind         SpanKind
	Start        time.Time
	End          time.Time
	Attributes   map[string]string
	StatusCode   int
}

func newTraceID() TraceID {
	var t TraceID
	_, _ = rand.Read(t[:])
	return t
}

func newSpanID() SpanID {
	var s SpanID
	_, _ = rand.Read(s[:])
	return s
}
