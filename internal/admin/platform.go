package admin

import (
	"strings"

	"nexus/internal/agent"
	"nexus/internal/compute"
	"nexus/internal/store"
)

// Platform 组合管理面需要的全部只读数据源；Runtime 可为 nil（未启用 Agent 时）。
type Platform struct {
	Store     store.Store
	Runtime   *agent.Runtime
	Pool      *compute.Pool
	Scheduler *compute.Scheduler
}

func (p *Platform) overview() overviewDTO {
	tenants := p.Store.ListTenants()
	keys := p.Store.ListAPIKeys()

	tickets := p.tickets()
	openTickets := 0
	for _, t := range tickets {
		if t.State == "open" {
			openTickets++
		}
	}

	nodes := p.nodes()
	healthy := 0
	for _, n := range nodes {
		if n.State == "healthy" {
			healthy++
		}
	}

	jobs := p.jobs()
	running, queued := 0, 0
	for _, j := range jobs {
		switch j.State {
		case "running":
			running++
		case "queued":
			queued++
		}
	}

	return overviewDTO{
		Tenants: len(tenants), Keys: len(keys),
		Tickets: len(tickets), OpenTickets: openTickets,
		Nodes: len(nodes), HealthyNodes: healthy,
		Jobs: len(jobs), RunningJobs: running, QueuedJobs: queued,
	}
}

func (p *Platform) tenants() []tenantDTO {
	src := p.Store.ListTenants()
	out := make([]tenantDTO, 0, len(src))
	for _, t := range src {
		out = append(out, tenantDTO{
			ID: t.ID, Name: t.Name, RPM: t.RequestsPerMinute, BudgetUSD: t.BudgetUSD,
		})
	}
	return out
}

func (p *Platform) keys() []keyDTO {
	src := p.Store.ListAPIKeys()
	out := make([]keyDTO, 0, len(src))
	for _, k := range src {
		models := k.Models
		if models == nil {
			models = []string{}
		}
		out = append(out, keyDTO{
			ID: k.ID, MaskedKey: maskKey(k.Key), TenantID: k.TenantID,
			Enabled: k.Enabled, RPM: k.RequestsPerMinute, Models: models,
		})
	}
	return out
}

func (p *Platform) tickets() []ticketDTO {
	if p.Runtime == nil {
		return []ticketDTO{}
	}
	src := p.Runtime.AllTickets()
	out := make([]ticketDTO, 0, len(src))
	for _, t := range src {
		dto := ticketDTO{
			ID: t.ID, Title: t.Title, State: string(t.State),
			Assignments: len(t.Assignments), Outcome: t.Outcome,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		}
		if asg := t.LatestAssignment(); asg != nil && asg.Report != nil {
			dto.LatestReport = string(asg.Report.State)
		}
		out = append(out, dto)
	}
	return out
}

func (p *Platform) nodes() []nodeDTO {
	src := p.Pool.Nodes()
	out := make([]nodeDTO, 0, len(src))
	for _, n := range src {
		labels := n.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		out = append(out, nodeDTO{
			ID: n.ID, Kind: string(n.Kind), Endpoint: n.Endpoint, State: string(n.State),
			Capacity:  toResources(n.Capacity),
			Used:      toResources(n.Used),
			Available: toResources(n.Available()),
			Labels:    labels, LastBeat: n.LastBeat,
		})
	}
	return out
}

func (p *Platform) jobs() []jobDTO {
	src := p.Scheduler.Jobs()
	out := make([]jobDTO, 0, len(src))
	for _, j := range src {
		out = append(out, jobDTO{
			ID: j.ID, TenantID: j.TenantID, Priority: int(j.Priority),
			State: string(j.State), NodeID: j.NodeID, Request: toResources(j.Request),
			EnqueuedAt: j.EnqueuedAt, StartedAt: j.StartedAt, EndedAt: j.EndedAt,
		})
	}
	return out
}

func toResources(r compute.Resources) resourcesDTO {
	return resourcesDTO{MemoryMB: r.MemoryMB, Slots: r.Slots, GPUs: r.GPUs}
}

// maskKey 只保留密钥前 7 位与后 4 位，中间用星号替代，避免完整凭据下发前端。
func maskKey(raw string) string {
	if len(raw) <= 11 {
		return strings.Repeat("*", len(raw))
	}
	return raw[:7] + strings.Repeat("*", 8) + raw[len(raw)-4:]
}
