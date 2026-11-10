package agent

import (
	"context"
	"time"
)

// Message 是一次对话消息。
type Message struct {
	Role    string
	Content string
}

// LLM 是 Agent 调用模型的端口。Router 用轻量模型、Worker 用强模型，
// 真实环境可由统一网关的 HTTP 客户端实现，测试中可注入脚本化 mock。
type LLM interface {
	Complete(ctx context.Context, model string, messages []Message) (string, error)
}

// Event 是事件总线上的一条事件。
type Event struct {
	Type    string
	At      time.Time
	Payload map[string]any
}

// 领域事件类型。
const (
	EventTriggerReceived = "trigger.received"
	EventDecisionMade    = "decision.made"
	EventTicketCreated   = "ticket.created"
	EventAssignmentRun   = "assignment.running"
	EventTicketClosed    = "ticket.closed"
)

// EventBus 是事件总线端口：发布订阅，解耦来源、Router 与 Worker。
type EventBus interface {
	Publish(ctx context.Context, event Event)
	Subscribe(eventType string, handler func(Event)) (cancel func())
}

// Tool 是工具插件端口。Worker 在隔离会话中调用，Router 不直接挂工具。
type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, args map[string]any) (map[string]any, error)
}

// Repository 是 Agent 自身聚合的存储端口。
type Repository interface {
	SaveTrigger(ctx context.Context, t *Trigger) error
	GetTrigger(ctx context.Context, id string) (*Trigger, error)

	SaveThread(ctx context.Context, th *Thread) error
	GetThread(ctx context.Context, key string) (*Thread, error)

	SaveTicket(ctx context.Context, tk *Ticket) error
	GetTicket(ctx context.Context, id string) (*Ticket, error)
	ListOpenTickets(ctx context.Context) ([]*Ticket, error)
}

// SideEffect 是需要在事务提交后执行的副作用。
type SideEffect struct {
	ID         string
	Kind       string
	Key        string // 幂等键：同 Kind+Key 只执行一次
	Payload    map[string]any
	Attempts   int
	EnqueuedAt time.Time
	RunAt      time.Time
}

// 副作用类型。
const (
	EffectLaunch   = "launch"   // 启动一个待处理派工
	EffectDelivery = "delivery" // 把工单输入投递给运行中的派工
	EffectReply    = "reply"    // 通过来源渠道发送消息
)

// EffectQueue 是持久副作用队列端口：事务内入队、提交后执行、退避重试、幂等去重。
type EffectQueue interface {
	Enqueue(ctx context.Context, effect SideEffect) error
	// RegisterHandler 注册某类副作用的处理器。
	RegisterHandler(kind string, handler func(ctx context.Context, e SideEffect) error)
	// Pending 返回当前待处理（含到期）副作用数量。
	Pending(ctx context.Context) int
}
