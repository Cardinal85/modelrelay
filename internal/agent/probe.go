package agent

import (
	"context"
	"log"
	"sync"
	"time"

	"modelrelay/internal/protocol"
)

// ProbeResult 描述一次探测结果。
type ProbeResult struct {
	Model     string    `json:"model"`
	Interface string    `json:"interface"`
	Status    string    `json:"status"` // supported | unsupported | error
	Duration  int64     `json:"duration_ms"`
	Time      time.Time `json:"time"`
	Error     string    `json:"error,omitempty"`
}

// Probe 负责本地模型能力探测。
type Probe struct {
	agent *Agent
	local *LocalClient

	mu      sync.Mutex
	results []ProbeResult
	lastOK  bool
}

// NewProbe 创建探测器。
func NewProbe(a *Agent, l *LocalClient) *Probe {
	return &Probe{agent: a, local: l}
}

// Loop 周期执行探测（启动即执行一次）。
func (p *Probe) Loop(ctx context.Context) {
	p.runOnce(ctx)
	interval := time.Duration(p.agent.cfg.Probe.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.runOnce(ctx)
		}
	}
}

// RunOnce 执行一次探测（管理员手动触发时调用）。
func (p *Probe) RunOnce() {
	p.runOnce(context.Background())
}

// Results 返回最近探测结果。
func (p *Probe) Results() []ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ProbeResult, len(p.results))
	copy(out, p.results)
	return out
}

func (p *Probe) runOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// 1. 模型发现。
	modelIDs, err := p.local.Models(ctx)
	if err != nil {
		p.setOK(false)
		log.Printf("agent: model discovery failed: %v", err)
		return
	}
	if len(modelIDs) == 0 {
		p.setOK(false)
		log.Printf("agent: no models discovered")
		return
	}
	// 2. 逐模型执行启用的低消耗探测。
	enabled := make(map[string]bool)
	for _, item := range p.agent.cfg.Probe.Enabled {
		enabled[item] = true
	}
	capByModel := make(map[string][]string)
	var results []ProbeResult
	for _, mid := range modelIDs {
		caps := p.probeModel(ctx, mid, enabled, &results)
		capByModel[mid] = caps
	}

	// 3. 更新模型能力并上报。
	models := make([]protocol.ModelInfo, 0, len(modelIDs))
	for _, mid := range modelIDs {
		// 当前探测覆盖的是可配置的接口子集，未探测接口保持未知，避免
		// 把“未执行探测”误报成“不支持”。
		models = append(models, protocol.ModelInfo{ID: mid, Capabilities: capByModel[mid], CapabilitiesComplete: false})
	}
	p.agent.setModels(models)
	p.agent.publishModels()

	p.mu.Lock()
	p.results = results
	p.lastOK = true
	p.mu.Unlock()
	p.agent.probeOK.Store(true)
	log.Printf("agent: probe done: %d models, %d checks", len(models), len(results))
}

// probeModel 对一个模型执行探测，返回能力列表。
// 能力名使用协议规范中的规范名（chat_completions / chat_stream 等），
// 与 Relay 调度器的 CapabilityForPath 保持一致。
func (p *Probe) probeModel(ctx context.Context, modelID string, enabled map[string]bool, results *[]ProbeResult) []string {
	var caps []string
	probes := []struct {
		name string // 配置开关名
		cap  string // 规范能力名
		fn   func(context.Context, string) error
	}{
		{"chat", "chat_completions", func(c context.Context, m string) error { return p.local.ChatProbe(c, m, false) }},
		{"chat_stream", "chat_stream", func(c context.Context, m string) error { return p.local.ChatProbe(c, m, true) }},
		{"completions", "completions", func(c context.Context, m string) error { return p.local.CompletionsProbe(c, m) }},
		{"embeddings", "embeddings", func(c context.Context, m string) error { return p.local.EmbeddingsProbe(c, m) }},
		{"responses", "responses", func(c context.Context, m string) error { return p.local.ResponsesProbe(c, m) }},
		{"tools", "tools", func(c context.Context, m string) error { return p.local.ToolsProbe(c, m) }},
	}
	for _, pr := range probes {
		if !enabled[pr.name] {
			continue
		}
		start := time.Now()
		err := pr.fn(ctx, modelID)
		dur := time.Since(start).Milliseconds()
		status := "supported"
		if err != nil {
			status = "unsupported"
		}
		res := ProbeResult{
			Model:     modelID,
			Interface: pr.cap,
			Status:    status,
			Duration:  dur,
			Time:      time.Now(),
		}
		if err != nil {
			res.Error = truncate(err.Error(), 200)
		}
		*results = append(*results, res)
		if status == "supported" {
			caps = append(caps, pr.cap)
		}
	}
	return caps
}

func (p *Probe) setOK(ok bool) {
	p.agent.probeOK.Store(ok)
	p.mu.Lock()
	p.lastOK = ok
	p.mu.Unlock()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
