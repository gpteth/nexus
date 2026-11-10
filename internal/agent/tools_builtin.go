package agent

import (
	"context"
	"time"
)

// EchoTool 是最小的内置工具，用于演示与测试工具协议。
type EchoTool struct{}

// Name 返回工具名。
func (EchoTool) Name() string { return "echo" }

// Description 返回工具说明。
func (EchoTool) Description() string { return "Echo back the provided text." }

// Execute 原样返回输入文本。
func (EchoTool) Execute(_ context.Context, args map[string]any) (map[string]any, error) {
	text, _ := args["text"].(string)
	return map[string]any{"echo": text}, nil
}

// NowTool 返回当前时间，演示无参数工具。
type NowTool struct {
	now func() time.Time
}

// NewNowTool 构造时间工具。
func NewNowTool(now func() time.Time) *NowTool { return &NowTool{now: now} }

// Name 返回工具名。
func (NowTool) Name() string { return "now" }

// Description 返回工具说明。
func (NowTool) Description() string { return "Return the current UTC time." }

// Execute 返回 RFC3339 时间。
func (t NowTool) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{"now": t.now().UTC().Format(time.RFC3339)}, nil
}
