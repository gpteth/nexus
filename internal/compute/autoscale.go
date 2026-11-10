package compute

import (
	"context"
	"time"
)

// Scaler 是弹性伸缩端口：真实环境对接云厂商/容器编排 API，测试中可注入 mock。
type Scaler interface {
	// ScaleUp 增加 count 个算力节点。
	ScaleUp(ctx context.Context, count int) error
	// ScaleDown 下线指定节点。
	ScaleDown(ctx context.Context, nodeIDs []string) error
}

// AutoscaleConfig 是自动扩缩配置。
type AutoscaleConfig struct {
	TargetQueueDepth int           // 排队任务超过该值触发扩容
	ScaleUpStep      int           // 单次扩容节点数
	ScaleDownIdleTTL time.Duration // 节点空闲且存活超过该时长才允许缩容，避免抖动
	MaxNodes         int           // 节点数上限
}

// AutoScaler 根据排队长度与空闲利用率驱动扩缩。
type AutoScaler struct {
	pool      *Pool
	scheduler *Scheduler
	scaler    Scaler
	cfg       AutoscaleConfig
	now       func() time.Time
	firstSeen map[string]time.Time
}

// NewAutoScaler 构造自动扩缩器。
func NewAutoScaler(pool *Pool, sched *Scheduler, scaler Scaler, cfg AutoscaleConfig, now func() time.Time) *AutoScaler {
	return &AutoScaler{
		pool:      pool,
		scheduler: sched,
		scaler:    scaler,
		cfg:       cfg,
		now:       now,
		firstSeen: map[string]time.Time{},
	}
}

// Reconcile 评估并执行一次扩缩。
func (a *AutoScaler) Reconcile(ctx context.Context) error {
	a.trackAges()

	total := len(a.pool.Nodes())
	if a.scheduler.Queued() > a.cfg.TargetQueueDepth && total < a.cfg.MaxNodes {
		step := a.cfg.ScaleUpStep
		if total+step > a.cfg.MaxNodes {
			step = a.cfg.MaxNodes - total
		}
		if step > 0 {
			if err := a.scaler.ScaleUp(ctx, step); err != nil {
				return err
			}
		}
	}

	if idle := a.idleNodes(); len(idle) > 0 {
		ids := make([]string, 0, len(idle))
		for _, n := range idle {
			ids = append(ids, n.ID)
		}
		return a.scaler.ScaleDown(ctx, ids)
	}
	return nil
}

// trackAges 记录节点首次出现时间，并清理已消失节点。
func (a *AutoScaler) trackAges() {
	existing := map[string]struct{}{}
	for _, n := range a.pool.Nodes() {
		existing[n.ID] = struct{}{}
		if _, ok := a.firstSeen[n.ID]; !ok {
			a.firstSeen[n.ID] = a.now()
		}
	}
	for id := range a.firstSeen {
		if _, ok := existing[id]; !ok {
			delete(a.firstSeen, id)
		}
	}
}

// idleNodes 返回零占用且存活超过空闲 TTL 的节点。
func (a *AutoScaler) idleNodes() []*Node {
	out := make([]*Node, 0)
	for _, n := range a.pool.Nodes() {
		if n.Used == (Resources{}) && a.now().Sub(a.firstSeen[n.ID]) >= a.cfg.ScaleDownIdleTTL {
			out = append(out, n)
		}
	}
	return out
}
