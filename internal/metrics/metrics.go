// Package metrics 是一个零依赖的 Prometheus 指标实现，支持带标签的计数器、仪表盘与直方图。
package metrics

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Registry 收集一组指标并渲染为 Prometheus 文本暴露格式。
type Registry struct {
	mu         sync.Mutex
	collectors []Collector
	names      map[string]bool
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{names: map[string]bool{}}
}

// Register 注册一个采集器，重名返回错误。
func (r *Registry) Register(c Collector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[c.Name()] {
		return fmt.Errorf("metrics: duplicate collector %q", c.Name())
	}
	r.names[c.Name()] = true
	r.collectors = append(r.collectors, c)
	return nil
}

// MustRegister 注册，失败即 panic。
func (r *Registry) MustRegister(c Collector) {
	if err := r.Register(c); err != nil {
		panic(err)
	}
}

// WriteTo 按注册顺序输出 Prometheus 文本格式。
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	r.mu.Lock()
	cs := make([]Collector, len(r.collectors))
	copy(cs, r.collectors)
	r.mu.Unlock()

	bw := bufio.NewWriter(w)
	var total int64
	for _, c := range cs {
		n, err := c.WriteTo(bw)
		total += n
		if err != nil {
			return total, err
		}
	}
	err := bw.Flush()
	return total, err
}

// Collector 是单个指标的统一接口。
type Collector interface {
	Name() string
	WriteTo(w *bufio.Writer) (int64, error)
}

// ---------- 通用工具 ----------

func labelKey(vals []string) string { return strings.Join(vals, "\x00") }

func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func renderLabels(buf *strings.Builder, names, vals []string) {
	if len(names) == 0 {
		return
	}
	buf.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(n)
		buf.WriteString(`="`)
		buf.WriteString(escapeLabel(vals[i]))
		buf.WriteByte('"')
	}
	buf.WriteByte('}')
}

// ---------- Counter ----------

type counterRow struct {
	vals []string
	val  float64
}

// CounterVec 是带标签的单调递增计数器。
type CounterVec struct {
	name, help string
	labelNames []string
	mu         sync.Mutex
	rows       map[string]*counterRow
}

// NewCounterVec 创建计数器向量。
func NewCounterVec(name, help string, labelNames ...string) *CounterVec {
	return &CounterVec{name: name, help: help, labelNames: labelNames, rows: map[string]*counterRow{}}
}

// Name 返回指标名。
func (c *CounterVec) Name() string { return c.name }

func (c *CounterVec) check(vals []string) {
	if len(vals) != len(c.labelNames) {
		panic(fmt.Sprintf("metrics: %q expects %d labels, got %d", c.name, len(c.labelNames), len(vals)))
	}
}

// Inc 给对应标签组合加 1。
func (c *CounterVec) Inc(vals ...string) { c.Add(1, vals...) }

// Add 给对应标签组合加 v（v 必须非负）。
func (c *CounterVec) Add(v float64, vals ...string) {
	c.check(vals)
	c.mu.Lock()
	defer c.mu.Unlock()
	k := labelKey(vals)
	row, ok := c.rows[k]
	if !ok {
		row = &counterRow{vals: append([]string(nil), vals...)}
		c.rows[k] = row
	}
	row.val += v
}

// WriteTo 渲染计数器。
func (c *CounterVec) WriteTo(w *bufio.Writer) (int64, error) {
	c.mu.Lock()
	rows := make([]*counterRow, 0, len(c.rows))
	for _, r := range c.rows {
		rows = append(rows, r)
	}
	c.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return labelKey(rows[i].vals) < labelKey(rows[j].vals) })

	var sb strings.Builder
	fmt.Fprintf(&sb, "# HELP %s %s\n", c.name, c.help)
	fmt.Fprintf(&sb, "# TYPE %s counter\n", c.name)
	for _, r := range rows {
		sb.WriteString(c.name)
		renderLabels(&sb, c.labelNames, r.vals)
		fmt.Fprintf(&sb, " %s\n", formatFloat(r.val))
	}
	n, err := io.WriteString(w, sb.String())
	return int64(n), err
}

// ---------- Gauge ----------

type gaugeRow struct {
	vals []string
	val  float64
}

// GaugeVec 是可增可减的仪表盘。
type GaugeVec struct {
	name, help string
	labelNames []string
	mu         sync.Mutex
	rows       map[string]*gaugeRow
}

// NewGaugeVec 创建仪表盘向量。
func NewGaugeVec(name, help string, labelNames ...string) *GaugeVec {
	return &GaugeVec{name: name, help: help, labelNames: labelNames, rows: map[string]*gaugeRow{}}
}

// Name 返回指标名。
func (g *GaugeVec) Name() string { return g.name }

// Set 设置当前值。
func (g *GaugeVec) Set(v float64, vals ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := labelKey(vals)
	row, ok := g.rows[k]
	if !ok {
		row = &gaugeRow{vals: append([]string(nil), vals...)}
		g.rows[k] = row
	}
	row.val = v
}

// Add 增量调整（可为负）。
func (g *GaugeVec) Add(v float64, vals ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := labelKey(vals)
	row, ok := g.rows[k]
	if !ok {
		row = &gaugeRow{vals: append([]string(nil), vals...)}
		g.rows[k] = row
	}
	row.val += v
}

// WriteTo 渲染仪表盘。
func (g *GaugeVec) WriteTo(w *bufio.Writer) (int64, error) {
	g.mu.Lock()
	rows := make([]*gaugeRow, 0, len(g.rows))
	for _, r := range g.rows {
		rows = append(rows, r)
	}
	g.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return labelKey(rows[i].vals) < labelKey(rows[j].vals) })

	var sb strings.Builder
	fmt.Fprintf(&sb, "# HELP %s %s\n", g.name, g.help)
	fmt.Fprintf(&sb, "# TYPE %s gauge\n", g.name)
	for _, r := range rows {
		sb.WriteString(g.name)
		renderLabels(&sb, g.labelNames, r.vals)
		fmt.Fprintf(&sb, " %s\n", formatFloat(r.val))
	}
	n, err := io.WriteString(w, sb.String())
	return int64(n), err
}

// ---------- Histogram ----------

type histogramRow struct {
	vals    []string
	count   uint64
	sum     float64
	buckets []uint64 // 与外部 buckets 上界一一对应，最后一个 +Inf 用 count 表示
}

// HistogramVec 是带桶的直方图。
type HistogramVec struct {
	name, help string
	labelNames []string
	buckets    []float64
	mu         sync.Mutex
	rows       map[string]*histogramRow
}

// NewHistogramVec 创建直方图向量，buckets 为有限上界，自动追加 +Inf。
func NewHistogramVec(name, help string, buckets []float64, labelNames ...string) *HistogramVec {
	sorted := append([]float64(nil), buckets...)
	sort.Float64s(sorted)
	return &HistogramVec{name: name, help: help, labelNames: labelNames, buckets: sorted, rows: map[string]*histogramRow{}}
}

// Name 返回指标名。
func (h *HistogramVec) Name() string { return h.name }

// Observe 记录一个样本。
func (h *HistogramVec) Observe(v float64, vals ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := labelKey(vals)
	row, ok := h.rows[k]
	if !ok {
		row = &histogramRow{vals: append([]string(nil), vals...), buckets: make([]uint64, len(h.buckets))}
		h.rows[k] = row
	}
	row.count++
	row.sum += v
	// 每个样本只计入第一个上界 >= v 的桶（分桶计数），渲染时再做前缀和得到累计值。
	for i, upper := range h.buckets {
		if v <= upper {
			row.buckets[i]++
			break
		}
	}
}

// WriteTo 渲染直方图（含 _bucket/_sum/_count）。
func (h *HistogramVec) WriteTo(w *bufio.Writer) (int64, error) {
	h.mu.Lock()
	rows := make([]*histogramRow, 0, len(h.rows))
	for _, r := range h.rows {
		rows = append(rows, r)
	}
	buckets := append([]float64(nil), h.buckets...)
	h.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return labelKey(rows[i].vals) < labelKey(rows[j].vals) })

	var sb strings.Builder
	fmt.Fprintf(&sb, "# HELP %s %s\n", h.name, h.help)
	fmt.Fprintf(&sb, "# TYPE %s histogram\n", h.name)
	for _, r := range rows {
		// 有限桶
		var cumulative uint64
		for i, upper := range buckets {
			cumulative += r.buckets[i]
			h.writeBucket(&sb, r.vals, strconv.FormatFloat(upper, 'f', -1, 64), cumulative)
		}
		// +Inf 桶等于总数
		h.writeBucket(&sb, r.vals, "+Inf", r.count)
		// sum / count
		sb.WriteString(h.name)
		sb.WriteString("_sum")
		renderLabels(&sb, h.labelNames, r.vals)
		fmt.Fprintf(&sb, " %s\n", formatFloat(r.sum))
		sb.WriteString(h.name)
		sb.WriteString("_count")
		renderLabels(&sb, h.labelNames, r.vals)
		fmt.Fprintf(&sb, " %d\n", r.count)
	}
	n, err := io.WriteString(w, sb.String())
	return int64(n), err
}

func (h *HistogramVec) writeBucket(sb *strings.Builder, vals []string, le string, cumulative uint64) {
	sb.WriteString(h.name)
	sb.WriteString("_bucket")
	names := append(append([]string(nil), h.labelNames...), "le")
	labelVals := append(append([]string(nil), vals...), le)
	renderLabels(sb, names, labelVals)
	fmt.Fprintf(sb, " %d\n", cumulative)
}
