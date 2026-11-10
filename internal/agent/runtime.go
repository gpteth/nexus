package agent

import (
	"context"
	"log/slog"
	"time"
)

// RuntimeConfig 是 Agent 运行时配置。
type RuntimeConfig struct {
	RouterModel string // 轻量模型
	WorkerModel string // 强模型
	MaxSteps    int    // Worker 单工单最大步数
}

// Runtime 组装 Engine、事件总线、副作用队列与 Router/Worker，
// 对外提供单一的事件提交入口与生命周期管理。
type Runtime struct {
	repo    Repository
	bus     EventBus
	effects *MemoryEffectQueue
	engine  *Engine
	router  *Router
	worker  *Worker
	now     func() time.Time
	logger  *slog.Logger
	cancel  context.CancelFunc
}

// New 构造运行时。
func New(cfg RuntimeConfig, llm LLM, tools []Tool, logger *slog.Logger) *Runtime {
	now := time.Now
	repo := NewMemoryRepository()
	bus := NewEventBus()
	effects := NewMemoryEffectQueue(now)
	engine := NewEngine(repo, bus, effects, now)
	router := NewRouter(engine, llm, cfg.RouterModel, bus, logger)
	worker := NewWorker(engine, llm, cfg.WorkerModel, tools, cfg.MaxSteps, logger)
	return &Runtime{
		repo:    repo,
		bus:     bus,
		effects: effects,
		engine:  engine,
		router:  router,
		worker:  worker,
		now:     now,
		logger:  logger,
	}
}

// Start 启动引擎、副作用队列与 Router，并注册 launch 处理器驱动 Worker。
func (rt *Runtime) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	rt.cancel = cancel

	rt.engine.Run(ctx)
	rt.effects.RegisterHandler(EffectLaunch, func(ctx context.Context, e SideEffect) error {
		ticketID, _ := e.Payload["ticket_id"].(string)
		return rt.worker.Handle(ctx, ticketID)
	})
	go rt.effects.Run(ctx)
	go rt.router.Run(ctx)
}

// Submit 接收一个外部事件进入运行时。
func (rt *Runtime) Submit(ctx context.Context, t *Trigger) error {
	return rt.engine.Receive(ctx, t)
}

// Close 停止所有后台循环。
func (rt *Runtime) Close() {
	if rt.cancel != nil {
		rt.cancel()
	}
}

// GetTicket 按 ID 返回工单，供测试与控制台读取。
func (rt *Runtime) GetTicket(ctx context.Context, id string) (*Ticket, error) {
	return rt.repo.GetTicket(ctx, id)
}

// OpenTickets 返回所有开放工单。
func (rt *Runtime) OpenTickets(ctx context.Context) ([]*Ticket, error) {
	return rt.repo.ListOpenTickets(ctx)
}

// AllTickets 返回全部工单（含已关闭），用于测试与控制台。
func (rt *Runtime) AllTickets() []*Ticket {
	if mr, ok := rt.repo.(*memoryRepository); ok {
		return mr.ListAllTickets()
	}
	return nil
}
