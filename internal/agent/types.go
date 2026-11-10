// Package agent 实现 Router/Worker 双层自主 Agent 运行时：事件驱动、单一写入者、
// 持久副作用与插件化工具。本文件定义与具体来源、策略、执行器无关的领域模型。
package agent

import "time"

// ActorRef 是事件源自身词汇表中的参与者身份；策略决定哪些参与者可在该来源发起工作。
type ActorRef struct {
	Source string
	ID     string
}

// ThreadRef 是线程引用；从原生事件推导 ID 的规则属于来源插件。
type ThreadRef struct {
	Source string
	ID     string
}

// ChannelRef 是线程上的只写回复地址，地址格式属于来源。
type ChannelRef struct {
	Source  string
	Thread  string
	Address map[string]string
}

// ThreadKey 返回线程在整个领域内的稳定键。
func ThreadKey(ref ThreadRef) string { return ref.Source + ":" + ref.ID }

// Addressed 表示事件是否点名/私聊 Agent。
type Addressed string

const (
	AddressedNone    Addressed = ""
	AddressedMention Addressed = "mention"
	AddressedDirect  Addressed = "direct"
)

// Trigger 是来源产生的一条不可变事件；Text 与 Meta 是不可信的外部数据。
type Trigger struct {
	ID        string
	Source    string
	Threads   []ThreadRef // 第一条为主线程，承载其评估
	Actor     ActorRef
	Human     bool      // 来源声明：由人而非机器人/CI/Agent 写出
	Addressed Addressed // 来源声明：发送者点名或私聊 Agent
	At        time.Time
	Text      string
	Channel   *ChannelRef
	URL       string
	Meta      map[string]any
}

// Strength 是授权强度：显式请求或推断候选。
type Strength string

const (
	StrengthExplicit Strength = "explicit"
	StrengthInferred Strength = "inferred"
)

// Mandate 是 accept 决策产生的不可变授权；执行受其约束，且一经产生不再改变。
type Mandate struct {
	Strength      Strength
	PolicyVersion string
	Actor         ActorRef
	Scope         string
}

// DecisionKind 是一次评估的结果类型。
type DecisionKind string

const (
	DecisionIgnore DecisionKind = "ignore"
	DecisionAccept DecisionKind = "accept"
	DecisionStop   DecisionKind = "stop"
)

// Decision 记录一次评估的结果，挂载在线程上。
type Decision struct {
	ID            string
	TriggerID     string
	ThreadKey     string
	Kind          DecisionKind
	Reason        string
	Strength      Strength
	PolicyVersion string
	At            time.Time
	TicketID      string
}

// AssignmentState 是派工状态。
type AssignmentState string

const (
	AssignmentPending AssignmentState = "pending"
	AssignmentRunning AssignmentState = "running"
	AssignmentEnded   AssignmentState = "ended"
)

// ReportState 是 Worker 上报的工作状态。
type ReportState string

const (
	ReportProgress ReportState = "progress"
	ReportWaiting  ReportState = "waiting"
	ReportReady    ReportState = "ready"
	ReportBlocked  ReportState = "blocked"
	ReportFailed   ReportState = "failed"
	ReportDropped  ReportState = "dropped"
)

// Report 是一次工作上报。
type Report struct {
	State   ReportState
	Summary string
	At      time.Time
}

// Assignment 是工单与一个执行器实例之间的绑定。
type Assignment struct {
	ID        string
	State     AssignmentState
	CreatedAt time.Time
	StartedAt time.Time
	EndedAt   time.Time
	Report    *Report
}

// TicketState 是工单状态。
type TicketState string

const (
	TicketOpen   TicketState = "open"
	TicketClosed TicketState = "closed"
)

// Ticket 是工单聚合：一个已授权工作项，含派工、渠道、线程绑定与输入。
type Ticket struct {
	ID          string
	Title       string
	State       TicketState
	Mandate     Mandate
	Channels    []ChannelRef
	Assignments []*Assignment
	TriggerIDs  []string
	Outcome     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Live 判断工单是否仍开放。
func (t *Ticket) Live() bool { return t.State == TicketOpen }

// LatestAssignment 返回最新的派工。
func (t *Ticket) LatestAssignment() *Assignment {
	if len(t.Assignments) == 0 {
		return nil
	}
	return t.Assignments[len(t.Assignments)-1]
}

// Thread 是线程聚合：评估的一致性边界，持有订阅与待处理事件。
type Thread struct {
	Key           string
	Ref           ThreadRef
	CreatedAt     time.Time
	LastAt        time.Time
	Subscriptions []string // 订阅该线程的工单 ID
	Pending       []string // 等待下一次评估的 Trigger ID
	Meta          map[string]any
}
