package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nexus/internal/admin"
	"nexus/internal/agent"
	"nexus/internal/compute"
	"nexus/internal/config"
	"nexus/internal/store"
)

// scriptedLLM 是控制台演示用的确定性模型：根据会话角色返回路由研判或执行结果，
// 不依赖真实 LLM，保证端到端可复现。
type scriptedLLM struct{}

func (scriptedLLM) Complete(_ context.Context, _ string, messages []agent.Message) (string, error) {
	sys := messages[0].Content
	if strings.Contains(sys, "routing layer") {
		return `{"decision":"accept","reason":"演示任务","title":"演示任务"}`, nil
	}
	// 初始请求固定在 messages[1]；含 block 标记的任务持续调用工具，
	// 直到达到最大步数触发 blocked，工单保持开放等人工。
	request := messages[1].Content
	if strings.Contains(request, "block") {
		return `{"thought":"need more","action":"call_tool","tool":"echo","args":{"text":"working"}}`, nil
	}
	return `{"thought":"done","action":"final","result":"演示任务已完成"}`, nil
}

// buildAdmin 装配算力池、调度器、Agent 运行时与管理面处理器，并引导演示数据。
// 未启用管理面时返回 nil。cleanup 用于停止 Agent 后台循环。
func buildAdmin(cfg *config.Config, logger *slog.Logger, st store.Store) (*admin.Handler, func()) {
	if !cfg.Admin.Enabled {
		return nil, func() {}
	}
	now := time.Now
	pool := compute.NewPool(now)
	sched := compute.NewScheduler(pool, now, logger)

	for _, n := range cfg.Admin.Seed.Nodes {
		node := &compute.Node{
			ID: n.ID, Kind: compute.NodeKind(n.Kind), Endpoint: n.Endpoint,
			State:    compute.NodeState(n.State),
			Capacity: compute.Resources{MemoryMB: n.MemoryMB, Slots: n.Slots, GPUs: n.GPUs},
			Labels:   n.Labels,
		}
		if err := pool.Register(node); err != nil {
			logger.Warn("seed node failed", "id", n.ID, "error", err)
		}
	}

	rt := agent.New(agent.RuntimeConfig{
		RouterModel: "router-model", WorkerModel: "worker-model", MaxSteps: 4,
	}, scriptedLLM{}, []agent.Tool{agent.EchoTool{}, agent.NewNowTool(now)}, logger)
	rt.Start(context.Background())

	seedRuntime(rt, sched, now)

	platform := &admin.Platform{Store: st, Runtime: rt, Pool: pool, Scheduler: sched}
	h := admin.NewHandler(platform, &cfg.Admin, logger)
	return h, func() { rt.Close() }
}

func seedRuntime(rt *agent.Runtime, sched *compute.Scheduler, now func() time.Time) {
	texts := []string{"帮我生成本周运营周报", "汇总上月用量并分析", "block 卡住需要人工介入"}
	for i, text := range texts {
		trigger := &agent.Trigger{
			ID:      fmt.Sprintf("trig-seed-%d", i),
			Source:  "console",
			Threads: []agent.ThreadRef{{Source: "console", ID: fmt.Sprintf("thread-%d", i)}},
			Actor:   agent.ActorRef{Source: "console", ID: "demo-user"},
			Human:   true, At: now(), Text: text,
		}
		_ = rt.Submit(context.Background(), trigger)
	}

	for i := 0; i < 3; i++ {
		job := &compute.Job{
			ID: fmt.Sprintf("job-seed-%d", i), TenantID: "default",
			Request:  compute.Resources{MemoryMB: 4096, Slots: 1, GPUs: 1},
			Priority: compute.PriorityNormal,
		}
		if err := sched.Submit(context.Background(), job); err != nil {
			continue
		}
		if i == 0 {
			// 把第一个作业标记成功，展示终态；其余保持运行。
			_ = sched.Complete(context.Background(), job.ID, compute.JobSucceeded)
		}
	}

	// 等待 Router/Worker 经事件与副作用队列异步处理完演示工单。
	time.Sleep(300 * time.Millisecond)
}
