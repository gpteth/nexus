package trace

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// W3C Trace Context 常量。
const (
	traceparentHeader = "Traceparent"
	version00         = "00"
	flagSampled       = 0x01
)

type remoteKey struct{}

// Inject 把当前跨度上下文写入 HTTP header（W3C traceparent）。
func Inject(ctx context.Context, h http.Header) {
	var sc SpanContext
	if span, ok := spanFromContext(ctx); ok {
		sc = span.Context()
	} else if c, ok := ctx.Value(remoteKey{}).(SpanContext); ok {
		sc = c
	}
	if !sc.IsValid() {
		return
	}
	flags := byte(0)
	if sc.Sampled {
		flags = flagSampled
	}
	h.Set(traceparentHeader, fmt.Sprintf("%s-%s-%s-%02x",
		version00, sc.TraceID.String(), sc.SpanID.String(), flags))
}

// Extract 从 HTTP header 解析远程父跨度并放入 context。
func Extract(ctx context.Context, h http.Header) context.Context {
	sc, ok := parseTraceparent(h.Get(traceparentHeader))
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, remoteKey{}, sc)
}

// parseTraceparent 解析并校验 "00-<traceid>-<spanid>-<flags>"。
func parseTraceparent(tp string) (SpanContext, bool) {
	parts := strings.Split(tp, "-")
	if len(parts) != 4 || parts[0] != version00 {
		return SpanContext{}, false
	}
	tid, e1 := hex.DecodeString(parts[1])
	sid, e2 := hex.DecodeString(parts[2])
	flags, e3 := hex.DecodeString(parts[3])
	if e1 != nil || e2 != nil || e3 != nil || len(tid) != 16 || len(sid) != 8 || len(flags) != 1 {
		return SpanContext{}, false
	}
	var sc SpanContext
	copy(sc.TraceID[:], tid)
	copy(sc.SpanID[:], sid)
	sc.Sampled = flags[0]&flagSampled == flagSampled

	var zeroTrace TraceID
	var zeroSpan SpanID
	if sc.TraceID == zeroTrace || sc.SpanID == zeroSpan {
		return SpanContext{}, false
	}
	return sc, true
}

// remoteParent 从 context 取出经 Extract 注入的远程父上下文。
func remoteParent(ctx context.Context) (SpanContext, bool) {
	c, ok := ctx.Value(remoteKey{}).(SpanContext)
	return c, ok
}
