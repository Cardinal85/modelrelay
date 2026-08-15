// Package relay 实现 Relay 服务：HTTP 上游、节点注册表、调度、WSS 接入与转发。
package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"modelrelay/internal/protocol"
)

// NodeState 是节点状态。
type NodeState string

const (
	StateConnecting NodeState = "connecting"
	StateOnline     NodeState = "online"
	StateSuspect    NodeState = "suspect"
	StateDegraded   NodeState = "degraded"
	StateDraining   NodeState = "draining"
	StateOffline    NodeState = "offline"
)

// ModelCap 描述一个模型在节点上的接口能力。
type ModelCap struct {
	ID                   string
	Capabilities         []string
	CapabilitiesComplete bool // false 表示能力目录不完整，未知能力按乐观策略调度
	ProbeTime            time.Time
}

// Node 是 Relay 内存中的节点状态。
type Node struct {
	ID           string
	AgentVersion string
	Platform     protocol.Platform
	State        NodeState
	Limits       protocol.Limits
	Models       map[string]*ModelCap
	Active       int // 当前活动请求数
	Heartbeat    time.Time
	HeartbeatSeq uint64
	ConnectedAt  time.Time
	CertHash     string // 客户端证书 SHA256 前 16 字节 hex（用于展示/审计）
	CertSerial   string // 客户端证书序列号，用于运行时吊销
	LastError    string

	mu sync.Mutex
}

// Snapshot 返回节点状态的只读快照。
type NodeSnapshot struct {
	ID            string    `json:"id"`
	AgentVersion  string    `json:"agent_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	State         NodeState `json:"state"`
	MaxConcurrent int       `json:"max_concurrent"`
	Active        int       `json:"active"`
	ModelCount    int       `json:"model_count"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	ConnectedAt   time.Time `json:"connected_at"`
	CertHash      string    `json:"cert_hash"`
	CertSerial    string    `json:"cert_serial,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

// Snapshot 生成节点快照。
func (n *Node) Snapshot() NodeSnapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return NodeSnapshot{
		ID:            n.ID,
		AgentVersion:  n.AgentVersion,
		OS:            n.Platform.OS,
		Arch:          n.Platform.Arch,
		State:         n.State,
		MaxConcurrent: n.Limits.MaxConcurrency,
		Active:        n.Active,
		ModelCount:    len(n.Models),
		LastHeartbeat: n.Heartbeat,
		ConnectedAt:   n.ConnectedAt,
		CertHash:      n.CertHash,
		CertSerial:    n.CertSerial,
		LastError:     n.LastError,
	}
}

// Online 报告节点是否可接收新请求。
func (n *Node) Online() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return (n.State == StateOnline || n.State == StateDegraded) && n.Active < n.Limits.MaxConcurrency
}

// HasCapacity 报告是否有并发容量。
func (n *Node) HasCapacity() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.Active < n.Limits.MaxConcurrency
}

// SupportsModel 检查模型存在且（能力未知或包含指定能力）。
func (n *Node) SupportsModel(model, capability string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	mc, ok := n.Models[model]
	if !ok {
		return false
	}
	if capability == "" {
		return true
	}
	if !mc.CapabilitiesComplete {
		return true // 探测范围不完整，未知能力允许转发，由本地服务返回真实结果
	}
	for _, c := range mc.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// Reserve 在节点上占用一个并发槽位。
func (n *Node) Reserve() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Active >= n.Limits.MaxConcurrency {
		return false
	}
	n.Active++
	return true
}

// Release 释放一个并发槽位。
func (n *Node) Release() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Active > 0 {
		n.Active--
	}
}

// SetMaxConcurrency 调整节点最大并发。
func (n *Node) SetMaxConcurrency(v int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if v > 0 {
		n.Limits.MaxConcurrency = v
	}
}

// SetModels 替换节点模型列表（能力探测更新时调用）。
func (n *Node) SetModels(models map[string]*ModelCap) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Models = cloneModels(models)
}

// MarkHeartbeat 更新心跳。
func (n *Node) MarkHeartbeat(seq uint64, localOK bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Heartbeat = time.Now()
	n.HeartbeatSeq = seq
	if !localOK && n.State == StateOnline {
		n.State = StateDegraded
	}
	if localOK && (n.State == StateDegraded || n.State == StateSuspect) {
		n.State = StateOnline
	}
}

// ModelSnapshot 返回线程安全的模型目录副本。
func (n *Node) ModelSnapshot() map[string]ModelCap {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]ModelCap, len(n.Models))
	for id, model := range n.Models {
		if model == nil {
			continue
		}
		copyModel := *model
		copyModel.Capabilities = append([]string(nil), model.Capabilities...)
		out[id] = copyModel
	}
	return out
}

func cloneModels(models map[string]*ModelCap) map[string]*ModelCap {
	out := make(map[string]*ModelCap, len(models))
	for id, model := range models {
		if model == nil {
			continue
		}
		copyModel := *model
		copyModel.Capabilities = append([]string(nil), model.Capabilities...)
		out[id] = &copyModel
	}
	return out
}

// SetState 设置节点状态。
func (n *Node) SetState(s NodeState) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.State = s
}

// LastHeartbeat 返回最近心跳时间。
func (n *Node) LastHeartbeat() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.Heartbeat
}

// CertFingerprint 计算证书指纹。
func CertFingerprint(certPEM []byte) string {
	sum := sha256.Sum256(certPEM)
	return hex.EncodeToString(sum[:8])
}

// Registry 是节点注册表，汇总节点与模型目录。
type Registry struct {
	mu    sync.RWMutex
	nodes map[string]*Node
}

// NewRegistry 创建注册表。
func NewRegistry() *Registry {
	return &Registry{nodes: make(map[string]*Node)}
}

// Register 注册（或覆盖）一个节点。
func (r *Registry) Register(n *Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.ID] = n
}

// TryRegister 原子地检查并注册节点。重复在线节点不会覆盖现有连接。
func (r *Registry) TryRegister(n *Node) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.nodes[n.ID]; ok {
		s := old.Snapshot()
		if s.State != StateOffline {
			return false
		}
	}
	r.nodes[n.ID] = n
	return true
}

// Get 获取节点。
func (r *Registry) Get(id string) *Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nodes[id]
}

// ExistsOnline 判断节点 ID 是否已在线。
func (r *Registry) ExistsOnline(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	if !ok {
		return false
	}
	state := n.Snapshot().State
	return state == StateOnline || state == StateSuspect || state == StateDegraded || state == StateDraining
}

// Remove 移除节点（断开时）。
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
}

// RemoveIfSame 仅在注册表仍指向指定连接时移除节点，避免旧连接清理新连接。
func (r *Registry) RemoveIfSame(id string, expected *Node) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.nodes[id]; ok && current == expected {
		delete(r.nodes, id)
		return true
	}
	return false
}

// List 列出全部节点快照，按 ID 排序。
func (r *Registry) List() []NodeSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeSnapshot, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n.Snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CountByState 统计各状态节点数。
func (r *Registry) CountByState() map[NodeState]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[NodeState]int)
	for _, n := range r.nodes {
		out[n.Snapshot().State]++
	}
	return out
}

// ModelDirectory 汇总全部模型目录（含节点列表）。
type ModelDirectoryEntry struct {
	ID           string   `json:"id"`
	Nodes        []string `json:"nodes"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// ModelDirectory 返回 Relay 的 /v1/models 目录。
// capabilities 取所有节点能力的并集；capabilities 为空表示未知。
func (r *Registry) ModelDirectory() []ModelDirectoryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	merged := make(map[string]*ModelDirectoryEntry)
	for _, n := range r.nodes {
		if state := n.Snapshot().State; state != StateOnline && state != StateDegraded {
			continue
		}
		for id, mc := range n.ModelSnapshot() {
			e, ok := merged[id]
			if !ok {
				e = &ModelDirectoryEntry{ID: id}
				merged[id] = e
			}
			e.Nodes = append(e.Nodes, n.ID)
			seen := make(map[string]bool)
			for _, c := range e.Capabilities {
				seen[c] = true
			}
			for _, c := range mc.Capabilities {
				if !seen[c] {
					e.Capabilities = append(e.Capabilities, c)
					seen[c] = true
				}
			}
		}
	}
	out := make([]ModelDirectoryEntry, 0, len(merged))
	ids := make([]string, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = append(out, *merged[id])
	}
	return out
}

// NodeID 描述注册表节点信息（用于调度候选）。
type candidate struct {
	node *Node
}

// String 便于日志。
func (n *Node) String() string { return fmt.Sprintf("node(%s)", n.ID) }
