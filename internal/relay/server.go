package relay

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"modelrelay/internal/protocol"
	"modelrelay/internal/store"
)

// pendingRequest 记录一个进行中的转发请求（Relay 侧）。
type pendingRequest struct {
	reqID     [16]byte
	requestID string
	nodeID    string
	// frames 是 Agent 发来的数据帧（有界缓冲）。
	frames chan *protocol.Frame
	// headers 是 Agent 发来的响应头（容量 1）。
	headers chan protocol.ResponseHeaders
	// done/err 各容量 1，用于终结。
	done      chan protocol.Done
	err       chan protocol.Error
	closed    chan struct{}
	closeOnce sync.Once
}

func newPendingRequest(reqID [16]byte, requestID, nodeID string) *pendingRequest {
	return &pendingRequest{
		reqID:     reqID,
		requestID: requestID,
		nodeID:    nodeID,
		frames:    make(chan *protocol.Frame, 16),
		headers:   make(chan protocol.ResponseHeaders, 1),
		done:      make(chan protocol.Done, 1),
		err:       make(chan protocol.Error, 1),
		closed:    make(chan struct{}),
	}
}

func (p *pendingRequest) close() {
	p.closeOnce.Do(func() { close(p.closed) })
}

// pendingRegistry 管理 Relay 侧进行中的请求。
type pendingRegistry struct {
	mu sync.Mutex
	m  map[[16]byte]*pendingRequest
}

func newPendingRegistry() *pendingRegistry {
	return &pendingRegistry{m: make(map[[16]byte]*pendingRequest)}
}

func (p *pendingRegistry) add(req *pendingRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[req.reqID] = req
}

func (p *pendingRegistry) get(reqID [16]byte) *pendingRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m[reqID]
}

func (p *pendingRegistry) remove(reqID [16]byte) {
	p.mu.Lock()
	req := p.m[reqID]
	delete(p.m, reqID)
	p.mu.Unlock()
	if req != nil {
		req.close()
	}
}

// Server 是 Relay 服务核心。
type Server struct {
	cfg *RelayConfig

	reg       *Registry
	scheduler *Scheduler
	pending   *pendingRegistry

	// stats 供观测。
	stats *Stats

	// connByNode 记录 node_id → 连接写入句柄（供发送控制消息/帧）。
	conns connRegistry

	// 准入：全局并发信号量与排队计数。
	sem     chan struct{}
	waiting atomic.Int64

	// 持久化与观测。
	st            *store.Store // 可为 nil（未配置）
	ring          *ringBuffer
	summaryW      *summaryWriter
	keepPrompt    bool
	retentionD    int
	retentionStop chan struct{}
	retentionDone chan struct{}
}

// RelayConfig 是 relay 运行所需的内部配置视图。
type RelayConfig struct {
	RelayID string

	// Listen
	HTTPListen string
	WSSListen  string

	// Limits
	MaxBodyBytes      int64
	MaxConcurrency    int
	QueueLength       int
	QueueTimeoutMs    int64
	TTFTTimeoutMs     int64
	IdleTimeoutMs     int64
	RequestTimeoutMs  int64
	HeartbeatTimeoutS int

	InternalAuthToken    string
	InternalAuthEnabled  bool
}

// Stats 是 Relay 运行指标（原子计数）。
type Stats struct {
	RequestsTotal    atomic.Int64
	RequestsSuccess  atomic.Int64
	RequestsFailed   atomic.Int64
	RequestsCanceled atomic.Int64
	QueuedTotal      atomic.Int64
	QueueFullTotal   atomic.Int64
	NodesOnline      atomic.Int64
	NodesOffline     atomic.Int64
	AgentReconnects  atomic.Int64
	HeartbeatMissed  atomic.Int64
}

// Snapshot 返回指标快照（供管理 API / 观测）。
type StatsSnapshot struct {
	RequestsTotal    int64 `json:"requests_total"`
	RequestsSuccess  int64 `json:"requests_success"`
	RequestsFailed   int64 `json:"requests_failed"`
	RequestsCanceled int64 `json:"requests_canceled"`
	QueuedTotal      int64 `json:"queued_total"`
	QueueFullTotal   int64 `json:"queue_full_total"`
	NodesOnline      int64 `json:"nodes_online"`
	NodesOffline     int64 `json:"nodes_offline"`
	AgentReconnects  int64 `json:"agent_reconnects"`
	HeartbeatMissed  int64 `json:"heartbeat_missed"`
}

// Snapshot 返回指标快照。
func (s *Stats) Snapshot() StatsSnapshot {
	return StatsSnapshot{
		RequestsTotal:    s.RequestsTotal.Load(),
		RequestsSuccess:  s.RequestsSuccess.Load(),
		RequestsFailed:   s.RequestsFailed.Load(),
		RequestsCanceled: s.RequestsCanceled.Load(),
		QueuedTotal:      s.QueuedTotal.Load(),
		QueueFullTotal:   s.QueueFullTotal.Load(),
		NodesOnline:      s.NodesOnline.Load(),
		NodesOffline:     s.NodesOffline.Load(),
		AgentReconnects:  s.AgentReconnects.Load(),
		HeartbeatMissed:  s.HeartbeatMissed.Load(),
	}
}

// connRegistry 保存 node_id → *nodeConn。
type connRegistry struct {
	mu sync.Mutex
	m  map[string]*nodeConn
}

type nodeConn struct {
	nodeID string
	node   *Node
	send   func(msg any) error           // 发送控制消息
	sendF  func(f *protocol.Frame) error // 发送数据帧
}

func (c *connRegistry) put(nodeID string, nc *nodeConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]*nodeConn)
	}
	c.m[nodeID] = nc
}

func (c *connRegistry) get(nodeID string) *nodeConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[nodeID]
}

func (c *connRegistry) del(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, nodeID)
}

func (c *connRegistry) delIf(nodeID string, node *Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current := c.m[nodeID]; current != nil && current.node == node {
		delete(c.m, nodeID)
	}
}

// NewServer 创建 Relay 服务。
func NewServer(cfg *RelayConfig) *Server {
	s := &Server{
		cfg:     cfg,
		reg:     NewRegistry(),
		pending: newPendingRegistry(),
		stats:   &Stats{},
		conns:   connRegistry{m: make(map[string]*nodeConn)},
		sem:     make(chan struct{}, cfg.MaxConcurrency),
		ring:    newRingBuffer(500),
	}
	s.scheduler = NewScheduler(s.reg)
	return s
}

// SetStore 挂载 SQLite 存储（可空）。
func (s *Server) SetStore(st *store.Store) {
	s.st = st
	if st != nil {
		s.summaryW = newSummaryWriter()
		s.summaryW.start(st)
		s.startRetention()
	}
}

// SetRetention 设置数据保留策略。
func (s *Server) SetRetention(keep bool, days int) {
	s.keepPrompt = keep
	s.retentionD = days
	s.startRetention()
}

// Close 关闭异步写入。
func (s *Server) Close() {
	if s.retentionStop != nil {
		close(s.retentionStop)
		<-s.retentionDone
	}
	if s.summaryW != nil {
		s.summaryW.shutdown()
	}
}

func (s *Server) startRetention() {
	if s.st == nil || s.retentionD <= 0 || s.retentionStop != nil {
		return
	}
	s.retentionStop = make(chan struct{})
	s.retentionDone = make(chan struct{})
	go func() {
		defer close(s.retentionDone)
		prune := func() {
			if err := s.st.PruneAudit(s.retentionD); err != nil {
				return
			}
			_ = s.st.PruneRequestSummaries(s.retentionD)
		}
		prune()
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			select {
			case <-s.retentionStop:
				return
			case <-tick.C:
				prune()
			}
		}
	}()
}

// recordRequest 记录请求摘要（内存环 + 可选 SQLite）。
func (s *Server) recordRequest(rec *requestRecord) {
	s.ring.push(*rec)
	if s.summaryW != nil {
		s.summaryW.submit(store.RequestSummary{
			RequestID:  rec.RequestID,
			Path:       rec.Path,
			Model:      rec.Model,
			Node:       rec.Node,
			Status:     rec.Status,
			TTFTMs:     rec.TTFTMs,
			DurationMs: rec.DurationMs,
			ErrorCode:  rec.ErrorCode,
		})
	}
}

// RecentRequests 返回最近请求记录（内存环 + 持久化记录）。
func (s *Server) RecentRequests() []requestRecord {
	out := s.ring.list()
	// 倒序（新的在前）。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Registry 暴露注册表（管理 API 使用）。
func (s *Server) Registry() *Registry { return s.reg }

// StatsRef 暴露指标。
func (s *Server) StatsRef() *Stats { return s.stats }

// sendToNode 向节点发送控制消息。
func (s *Server) sendToNode(nodeID string, msg any) error {
	nc := s.conns.get(nodeID)
	if nc == nil {
		return fmt.Errorf("relay: node %s not connected", nodeID)
	}
	return nc.send(msg)
}

// sendFrameToNode 向节点发送数据帧。
func (s *Server) sendFrameToNode(nodeID string, f *protocol.Frame) error {
	nc := s.conns.get(nodeID)
	if nc == nil {
		return fmt.Errorf("relay: node %s not connected", nodeID)
	}
	return nc.sendF(f)
}
