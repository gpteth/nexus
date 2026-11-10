package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// scriptedLLM 是按调用次数返回预设响应的 LLM 端口 mock。
type scriptedLLM struct {
	mu        sync.Mutex
	responses []string
	calls     int
}

func (s *scriptedLLM) Complete(_ context.Context, _ string, _ []Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls >= len(s.responses) {
		return "", errors.New("scripted LLM exhausted")
	}
	out := s.responses[s.calls]
	s.calls++
	return out, nil
}

func (s *scriptedLLM) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testRuntime(t *testing.T, llm LLM, tools []Tool, maxSteps int) *Runtime {
	t.Helper()
	rt := New(RuntimeConfig{RouterModel: "router-model", WorkerModel: "worker-model", MaxSteps: maxSteps},
		llm, tools, discardLogger())
	rt.Start(context.Background())
	t.Cleanup(rt.Close)
	return rt
}

func sampleTrigger(id string, human bool, addressed Addressed, text string) *Trigger {
	ref := ThreadRef{Source: "github", ID: "thread-1"}
	return &Trigger{
		ID:        id,
		Source:    "github",
		Threads:   []ThreadRef{ref},
		Actor:     ActorRef{Source: "github", ID: "actor-1"},
		Human:     human,
		Addressed: addressed,
		At:        time.Now(),
		Text:      text,
	}
}

func waitForTicket(t *testing.T, rt *Runtime, closed bool) *Ticket {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tickets := rt.AllTickets(); len(tickets) > 0 {
			tk := tickets[0]
			if !closed || !tk.Live() {
				return tk
			}
		}
		time.Sleep(8 * time.Millisecond)
	}
	t.Fatal("timed out waiting for ticket")
	return nil
}

// 完整闭环：Router accept → Worker 调用工具 → final，工单 ready 关闭。
func TestRuntime_AcceptAndComplete(t *testing.T) {
	llm := &scriptedLLM{responses: []string{
		`{"decision":"accept","reason":"Do the thing","title":"Do the thing"}`,
		`{"thought":"plan","action":"call_tool","tool":"echo","args":{"text":"hi"}}`,
		`{"thought":"done","action":"final","result":"echoed hi"}`,
	}}
	rt := testRuntime(t, llm, []Tool{EchoTool{}}, 8)

	if err := rt.Submit(context.Background(),
		sampleTrigger("trg-1", true, AddressedMention, "please do the thing")); err != nil {
		t.Fatal(err)
	}

	tk := waitForTicket(t, rt, true)
	if tk.Live() {
		t.Fatal("ticket should be closed")
	}
	if tk.Outcome != "ready" {
		t.Fatalf("outcome=%q want ready", tk.Outcome)
	}
	asg := tk.LatestAssignment()
	if asg == nil || asg.Report == nil || asg.Report.State != ReportReady {
		t.Fatal("missing ready report")
	}
	if got := llm.callCount(); got != 3 {
		t.Fatalf("llm calls=%d want 3", got)
	}
}

// 非人类、未点名的噪音被 Fast Path 忽略：不调用模型、不产生工单。
func TestRuntime_FastPathIgnoreNoise(t *testing.T) {
	llm := &scriptedLLM{}
	rt := testRuntime(t, llm, nil, 8)

	if err := rt.Submit(context.Background(),
		sampleTrigger("trg-bot", false, AddressedNone, "CI build finished")); err != nil {
		t.Fatal(err)
	}

	time.Sleep(120 * time.Millisecond)
	if got := llm.callCount(); got != 0 {
		t.Fatalf("llm calls=%d want 0", got)
	}
	if tickets := rt.AllTickets(); len(tickets) != 0 {
		t.Fatalf("expected no tickets, got %d", len(tickets))
	}
}

// 点名的停止指令走 Fast Path，不调用模型、不产生工单。
func TestRouter_FastPathStop(t *testing.T) {
	llm := &scriptedLLM{}
	rt := testRuntime(t, llm, nil, 8)

	if err := rt.Submit(context.Background(),
		sampleTrigger("trg-stop", true, AddressedMention, "please stop the work now")); err != nil {
		t.Fatal(err)
	}

	time.Sleep(120 * time.Millisecond)
	if got := llm.callCount(); got != 0 {
		t.Fatalf("fast path should not call LLM, calls=%d", got)
	}
	if tickets := rt.AllTickets(); len(tickets) != 0 {
		t.Fatal("stop should not create a ticket")
	}
}

// Worker 达到最大步数仍未 final：上报 blocked，工单保持开放等待人工 retry。
func TestWorker_MaxStepsBlocked(t *testing.T) {
	llm := &scriptedLLM{responses: []string{
		`{"decision":"accept","title":"loop"}`,
		`{"action":"call_tool","tool":"echo","args":{}}`,
		`{"action":"call_tool","tool":"echo","args":{}}`,
		`{"action":"call_tool","tool":"echo","args":{}}`,
	}}
	rt := testRuntime(t, llm, []Tool{EchoTool{}}, 3)

	if err := rt.Submit(context.Background(),
		sampleTrigger("trg-loop", true, AddressedMention, "do a looping task")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tickets := rt.AllTickets(); len(tickets) > 0 {
			tk := tickets[0]
			asg := tk.LatestAssignment()
			if asg != nil && asg.Report != nil && asg.Report.State == ReportBlocked {
				if !tk.Live() {
					t.Fatal("blocked ticket should stay open for human retry")
				}
				if got := llm.callCount(); got != 4 {
					t.Fatalf("llm calls=%d want 4", got)
				}
				return
			}
		}
		time.Sleep(8 * time.Millisecond)
	}
	t.Fatal("timed out waiting for blocked report")
}

// 重复事件返回 ErrDuplicate。
func TestEngine_DuplicateTrigger(t *testing.T) {
	llm := &scriptedLLM{}
	rt := testRuntime(t, llm, nil, 8)
	tr := sampleTrigger("trg-dup", false, AddressedNone, "x")

	if err := rt.Submit(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	if err := rt.Submit(context.Background(), tr); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err=%v want ErrDuplicate", err)
	}
}

// 副作用队列幂等入队与退避计算。
func TestEffectQueue_IdempotentAndBackoff(t *testing.T) {
	q := NewMemoryEffectQueue(time.Now)
	if err := q.Enqueue(context.Background(), SideEffect{Kind: "flaky", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(context.Background(), SideEffect{Kind: "flaky", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	if got := q.Pending(context.Background()); got != 1 {
		t.Fatalf("pending=%d want 1", got)
	}
	if got := effectBackoff(1); got != 100*time.Millisecond {
		t.Fatalf("backoff(1)=%v want 100ms", got)
	}
	if got := effectBackoff(3); got != 400*time.Millisecond {
		t.Fatalf("backoff(3)=%v want 400ms", got)
	}
	if got := effectBackoff(20); got != 30*time.Second {
		t.Fatalf("backoff(20)=%v want cap 30s", got)
	}
}

// extractJSON 能从 markdown 代码块中提取 JSON。
func TestExtractJSON(t *testing.T) {
	in := "```json\n{\"decision\":\"ignore\"}\n```"
	if got := extractJSON(in); got != `{"decision":"ignore"}` {
		t.Fatalf("got=%q", got)
	}
}
