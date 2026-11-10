package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// errMaxSteps 表示 Worker 达到最大步数仍未给出最终结果，需要人工介入。
var errMaxSteps = errors.New("worker: exceeded maximum steps, needs human")

type workerOutput struct {
	Thought string         `json:"thought"`
	Action  string         `json:"action"`
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Result  string         `json:"result"`
}

// Worker 是执行会话（强模型）：上下文隔离、挂全套工具，不直接对用户说话。
// 它在 launch 副作用触发时为工单创建派工，循环调用工具直到产出最终结果。
type Worker struct {
	engine   *Engine
	llm      LLM
	model    string
	tools    map[string]Tool
	maxSteps int
	logger   *slog.Logger
}

// NewWorker 构造 Worker。
func NewWorker(engine *Engine, llm LLM, model string, tools []Tool, maxSteps int, logger *slog.Logger) *Worker {
	w := &Worker{
		engine:   engine,
		llm:      llm,
		model:    model,
		tools:    map[string]Tool{},
		maxSteps: maxSteps,
		logger:   logger,
	}
	for _, t := range tools {
		w.tools[t.Name()] = t
	}
	return w
}

// Handle 启动派工并执行工单；按结果上报 ready/blocked/failed。
func (w *Worker) Handle(ctx context.Context, ticketID string) error {
	if _, err := w.engine.StartAssignment(ctx, ticketID); err != nil {
		return err
	}
	result, err := w.run(ctx, ticketID)
	if err != nil {
		state := ReportFailed
		if errors.Is(err, errMaxSteps) {
			state = ReportBlocked
		}
		return w.engine.RecordReport(ctx, ticketID, &Report{State: state, Summary: err.Error()})
	}
	return w.engine.RecordReport(ctx, ticketID, &Report{State: ReportReady, Summary: result})
}

func (w *Worker) run(ctx context.Context, ticketID string) (string, error) {
	ticket, err := w.engine.repo.GetTicket(ctx, ticketID)
	if err != nil {
		return "", err
	}
	var taskText string
	if len(ticket.TriggerIDs) > 0 {
		if tr, err := w.engine.repo.GetTrigger(ctx, ticket.TriggerIDs[0]); err == nil {
			taskText = tr.Text
		}
	}
	messages := []Message{
		{Role: "system", Content: w.systemPrompt()},
		{Role: "user", Content: fmt.Sprintf("Task: %s\nRequest: %s", ticket.Title, taskText)},
	}

	for step := 0; step < w.maxSteps; step++ {
		out, err := w.llm.Complete(ctx, w.model, messages)
		if err != nil {
			return "", err
		}
		var action workerOutput
		if err := json.Unmarshal([]byte(extractJSON(out)), &action); err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidOutput, err)
		}
		switch action.Action {
		case "final":
			return action.Result, nil
		case "call_tool":
			tool, ok := w.tools[action.Tool]
			if !ok {
				messages = append(messages, Message{Role: "user", Content: "tool not available: " + action.Tool})
				continue
			}
			res, toolErr := tool.Execute(ctx, action.Args)
			if toolErr != nil {
				messages = append(messages, Message{Role: "user", Content: "tool error: " + toolErr.Error()})
				continue
			}
			encoded, _ := json.Marshal(res)
			messages = append(messages,
				Message{Role: "assistant", Content: out},
				Message{Role: "user", Content: "tool result: " + string(encoded)},
			)
		default:
			return "", fmt.Errorf("%w: unknown action %q", ErrInvalidOutput, action.Action)
		}
	}
	return "", errMaxSteps
}

func (w *Worker) systemPrompt() string {
	var b strings.Builder
	b.WriteString(`You are a worker agent executing an authorized task in an isolated session. You do not talk to the user directly.
Use the available tools to complete the task, then return the final result.
Every response must be ONLY a JSON object:
- To call a tool: {"thought":"...","action":"call_tool","tool":"NAME","args":{...}}
- To finish: {"thought":"...","action":"final","result":"summary of what was done"}

Available tools:
`)
	for name, t := range w.tools {
		fmt.Fprintf(&b, "- %s: %s\n", name, t.Description())
	}
	return b.String()
}
