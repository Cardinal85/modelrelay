package relay

import (
	"context"
	"errors"
	"time"

	"modelrelay/internal/protocol"
)

// 调度错误分类（区分可等待与不可等待）。
var (
	ErrModelNotFound          = errors.New(protocol.ErrModelNotFound)
	ErrCapabilityNotSupported = errors.New(protocol.ErrCapabilityNotSupported)
	ErrNoNodeOnline           = errors.New(protocol.ErrNoAvailableNode + ": no node online")
	ErrAllNodesBusy           = errors.New(protocol.ErrNoAvailableNode + ": all nodes busy or draining")
)

// Scheduler 根据模型与接口能力选择节点。
type Scheduler struct {
	reg *Registry
}

// NewScheduler 创建调度器。
func NewScheduler(reg *Registry) *Scheduler {
	return &Scheduler{reg: reg}
}

// CapabilityForPath 将 HTTP 路径映射为能力名；返回 "" 表示不要求能力（如 /v1/models）。
func CapabilityForPath(method, path string) string {
	switch {
	case method == "POST" && path == "/v1/chat/completions":
		return "chat_completions"
	case method == "POST" && path == "/v1/completions":
		return "completions"
	case method == "POST" && path == "/v1/embeddings":
		return "embeddings"
	case method == "POST" && path == "/v1/responses":
		return "responses"
	case method == "POST" && path == "/v1/audio/transcriptions":
		return "audio_transcriptions"
	case method == "POST" && path == "/v1/audio/translations":
		return "audio_translations"
	case method == "POST" && path == "/v1/audio/speech":
		return "audio_speech"
	case method == "POST" && path == "/v1/images/generations":
		return "image_generations"
	case method == "POST" && path == "/v1/images/edits":
		return "image_edits"
	case method == "POST" && path == "/v1/images/variations":
		return "image_variations"
	case method == "POST" && path == "/v1/moderations":
		return "moderations"
	case method == "POST" && path == "/v1/rerank":
		return "rerank"
	case method == "POST" && path == "/v1/reranking":
		return "rerank"
	default:
		return ""
	}
}

// IsAllowedPath 判断路径与方法是否在转发白名单内。
func IsAllowedPath(method, path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/embeddings", "/v1/responses",
		"/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech",
		"/v1/images/generations", "/v1/images/edits", "/v1/images/variations",
		"/v1/moderations", "/v1/rerank", "/v1/reranking":
		return method == "POST" || method == "GET"
	case "/v1/models", "/v1/models/":
		return method == "GET"
	default:
		return false
	}
}

// Select 选择一个可用节点。
// model 为请求的模型名；capability 为接口能力名（"" 表示不要求）。
func (s *Scheduler) Select(model, capability string) (*Node, error) {
	var candidates []*Node
	for _, snap := range s.reg.List() {
		if snap.State != StateOnline && snap.State != StateDegraded {
			continue
		}
		n := s.reg.Get(snap.ID)
		if n == nil || !n.HasCapacity() {
			continue
		}
		if !n.SupportsModel(model, capability) {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) == 0 {
		return nil, s.classifyNoNode(model, capability)
	}
	// 最少负载优先，其次 round-robin 分散。
	best := candidates[0]
	for _, c := range candidates[1:] {
		if load(c) < load(best) {
			best = c
		}
	}
	return best, nil
}

func load(n *Node) float64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Limits.MaxConcurrency <= 0 {
		return 1
	}
	return float64(n.Active) / float64(n.Limits.MaxConcurrency)
}

func (s *Scheduler) classifyNoNode(model, capability string) error {
	online := 0
	hasModel := false
	capOK := false
	for _, snap := range s.reg.List() {
		if snap.State == StateOnline || snap.State == StateDegraded {
			online++
		}
		n := s.reg.Get(snap.ID)
		if n == nil {
			continue
		}
		mc, ok := n.ModelSnapshot()[model]
		if ok {
			hasModel = true
			if !mc.CapabilitiesComplete {
				capOK = true
				continue
			}
			for _, c := range mc.Capabilities {
				if c == capability {
					capOK = true
				}
			}
		}
	}
	switch {
	case online == 0:
		return ErrNoNodeOnline
	case !hasModel:
		return ErrModelNotFound
	case !capOK:
		return ErrCapabilityNotSupported
	default:
		// 在线且有模型有能力，但都忙或正在 Drain：可等待。
		return ErrAllNodesBusy
	}
}

// WaitForNode 选择一个可用节点；当节点全部繁忙时按 queueTimeout 等待容量释放。
func (s *Scheduler) WaitForNode(ctx context.Context, model, capability string, queueTimeout time.Duration) (*Node, error) {
	deadline := time.Now().Add(queueTimeout)
	for {
		node, err := s.Select(model, capability)
		if err == nil {
			return node, nil
		}
		if !errors.Is(err, ErrAllNodesBusy) {
			return nil, err
		}
		wait := 50 * time.Millisecond
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		if wait <= 0 {
			return nil, ErrAllNodesBusy
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}
