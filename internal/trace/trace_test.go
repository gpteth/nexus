package trace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- 内核与父子关系 ----

func TestTracer_ParentChild(t *testing.T) {
	exp := NewMemoryExporter()
	tr := NewTracer(exp, time.Now)

	ctx, root := tr.Start(context.Background(), "root")
	root.SetAttribute("k", "v")
	if !root.Context().IsValid() {
		t.Fatal("root span should be valid")
	}
	_, child := tr.Start(ctx, "child")
	child.SetStatus(200)
	child.End()
	root.End()

	spans := exp.Spans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans want 2", len(spans))
	}
	gotChild, gotRoot := spans[0], spans[1] // child 先结束
	if gotChild.Name != "child" || gotRoot.Name != "root" {
		t.Fatal("export order or names wrong")
	}
	if gotChild.SpanContext.TraceID != gotRoot.SpanContext.TraceID {
		t.Fatal("child must share root trace id")
	}
	if gotChild.ParentSpanID != gotRoot.SpanContext.SpanID {
		t.Fatal("child parent must point to root span id")
	}
	if gotChild.Attributes["k"] != "" || gotRoot.Attributes["k"] != "v" {
		t.Fatal("attributes should be isolated per span")
	}
}

// ---- W3C 传播 ----

func TestParseTraceparent(t *testing.T) {
	exp := NewMemoryExporter()
	tr := NewTracer(exp, time.Now)
	ctx, span := tr.Start(context.Background(), "x")
	h := http.Header{}
	Inject(ctx, h)
	tp := h.Get("Traceparent")
	if !strings.HasPrefix(tp, "00-") || len(strings.Split(tp, "-")) != 4 {
		t.Fatalf("bad traceparent %q", tp)
	}

	got, ok := parseTraceparent(tp)
	if !ok {
		t.Fatal("valid traceparent should parse")
	}
	if got.TraceID != span.Context().TraceID || got.SpanID != span.Context().SpanID || !got.Sampled {
		t.Fatal("parsed context should match injected span")
	}

	bad := []string{
		"", "garbage", "00-aa-bb", "00-zz-zz-01",
		"00-00000000000000000000000000000000-0000000000000001-01", // 全零 trace id
		"00-00000000000000000000000000000001-0000000000000000-01", // 全零 span id
	}
	for _, b := range bad {
		if _, ok := parseTraceparent(b); ok {
			t.Fatalf("should reject %q", b)
		}
	}

	// 无活动跨度时不应注入。
	empty := http.Header{}
	Inject(context.Background(), empty)
	if empty.Get("Traceparent") != "" {
		t.Fatal("no span should produce no header")
	}
}

// ---- 服务端中间件 ----

func TestHTTPMiddleware(t *testing.T) {
	exp := NewMemoryExporter()
	tr := NewTracer(exp, time.Now)
	h := tr.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Post(srv.URL, "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("got %d want 418", resp.StatusCode)
	}
	spans := exp.Spans()
	if len(spans) != 1 || spans[0].Kind != SpanServer || spans[0].StatusCode != 418 {
		t.Fatalf("server span wrong: %+v", spans)
	}
}

// ---- 跨进程连续性（核心互操作） ----

func TestCrossProcess_Propagation(t *testing.T) {
	serverExp := NewMemoryExporter()
	serverTracer := NewTracer(serverExp, time.Now)
	srv := httptest.NewServer(serverTracer.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	clientExp := NewMemoryExporter()
	clientTracer := NewTracer(clientExp, time.Now)
	client := &http.Client{Transport: clientTracer.WrapTransport(http.DefaultTransport)}

	ctx, root := clientTracer.Start(context.Background(), "root")
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatal("upstream call failed")
	}
	root.End()

	var clientSpan, serverSpan SpanData
	for _, s := range clientExp.Spans() {
		if s.Kind == SpanClient {
			clientSpan = s
		}
	}
	serverSpan = serverExp.Spans()[0]

	if clientSpan.SpanContext.TraceID != serverSpan.SpanContext.TraceID {
		t.Fatal("client and server must share one trace id")
	}
	if serverSpan.ParentSpanID != clientSpan.SpanContext.SpanID {
		t.Fatal("server parent must be the client span")
	}
}
