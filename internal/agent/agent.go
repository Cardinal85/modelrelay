package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"modelrelay/internal/config"
	"modelrelay/internal/protocol"
)

// Stats 是 Agent 运行指标。
type Stats struct {
	Reconnects     atomic.Int64
	RequestsTotal  atomic.Int64
	RequestsOK     atomic.Int64
	RequestsFailed atomic.Int64
	LocalBusyTotal atomic.Int64
	CanceledTotal  atomic.Int64
}

// Agent 是 Agent 服务核心。
type Agent struct {
	cfg   *config.Agent
	local *LocalClient
	probe *Probe

	stats Stats

	mu        sync.Mutex
	active    map[[16]byte]*activeRequest
	draining  bool
	connector *Connector

	heartbeatOK atomic.Bool
	probeOK     atomic.Bool

	modelsMu sync.RWMutex
	models   []protocol.ModelInfo

	// localSem 是本地并发上限。
	localSem chan struct{}
}

type activeRequest struct {
	reqID     string   // 完整 request_id（用于控制消息）
	id16      [16]byte // 数据帧用 request id
	cancel    context.CancelFunc
	bodyCh    chan *protocol.Frame
	done      chan struct{}
	closeOnce sync.Once
	bodyLen   int64
}

// New 创建 Agent。
func New(cfg *config.Agent) (*Agent, error) {
	if cfg.Local.MaxConcurrency <= 0 {
		cfg.Local.MaxConcurrency = 8
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 16 << 20
	}
	a := &Agent{
		cfg:      cfg,
		active:   make(map[[16]byte]*activeRequest),
		localSem: make(chan struct{}, cfg.Local.MaxConcurrency),
	}
	local, err := NewLocalClient(cfg.Local)
	if err != nil {
		return nil, err
	}
	a.local = local
	a.probe = NewProbe(a, local)
	return a, nil
}

// Run 启动 Agent（阻塞直到 ctx 取消）。
func (a *Agent) Run(ctx context.Context) {
	go a.probe.Loop(ctx)
	conn := NewConnector(a)
	a.mu.Lock()
	a.connector = conn
	a.mu.Unlock()
	go func() {
		<-ctx.Done()
		conn.Stop()
	}()
	conn.Run()
}

// publishModels 向当前 Relay 发送模型更新。
func (a *Agent) publishModels() {
	a.mu.Lock()
	conn := a.connector
	a.mu.Unlock()
	if conn == nil {
		return
	}
	models := a.modelsForHello()
	_ = conn.writeControl(protocol.ModelsUpdate{Type: protocol.MsgModelsUpdate, Models: models})
}

// modelsForHello 返回注册用的模型列表。
func (a *Agent) modelsForHello() []protocol.ModelInfo {
	a.modelsMu.RLock()
	defer a.modelsMu.RUnlock()
	out := make([]protocol.ModelInfo, len(a.models))
	copy(out, a.models)
	return out
}

// setModels 更新发现到的模型列表。
func (a *Agent) setModels(models []protocol.ModelInfo) {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	a.models = models
}

// handleRequest 处理来自 Relay 的转发请求。
func (a *Agent) handleRequest(c *Connector, req protocol.Request) {
	a.stats.RequestsTotal.Add(1)
	if !protocol.IsAllowedPath(req.Method, req.Path) {
		_ = c.writeControl(protocol.Error{
			Type: protocol.MsgError, RequestID: req.RequestID,
			Code: protocol.ErrInvalidRequest, Message: "method or path not allowed",
		})
		a.stats.RequestsFailed.Add(1)
		return
	}
	if req.BodyLen < 0 || req.BodyLen > a.cfg.MaxBodyBytes {
		_ = c.writeControl(protocol.Error{
			Type: protocol.MsgError, RequestID: req.RequestID,
			Code: protocol.ErrBodyTooLarge, Message: "request body exceeds agent limit",
		})
		a.stats.RequestsFailed.Add(1)
		return
	}

	a.mu.Lock()
	draining := a.draining
	a.mu.Unlock()
	if draining {
		_ = c.writeControl(protocol.Error{Type: protocol.MsgError, RequestID: req.RequestID, Code: protocol.ErrDraining, Message: "node draining"})
		a.stats.RequestsFailed.Add(1)
		return
	}

	// 本地并发准入（非阻塞）。
	select {
	case a.localSem <- struct{}{}:
	default:
		a.stats.LocalBusyTotal.Add(1)
		a.stats.RequestsFailed.Add(1)
		_ = c.writeControl(protocol.Error{Type: protocol.MsgError, RequestID: req.RequestID, Code: protocol.ErrLocalBusy, Message: "local concurrency limit reached"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	if a.cfg.Local.ResponseTimeoutSec > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(a.cfg.Local.ResponseTimeoutSec)*time.Second)
	}
	ar := &activeRequest{
		reqID: req.RequestID, id16: protocol.RequestIDBytes(req.RequestID),
		cancel: cancel, bodyCh: make(chan *protocol.Frame, 16), done: make(chan struct{}),
		bodyLen: req.BodyLen,
	}
	a.mu.Lock()
	a.active[ar.id16] = ar
	a.mu.Unlock()

	go a.serveRequest(c, req, ar, ctx)
}

// routeBodyFrame 将请求体帧路由到对应请求。
func (a *Agent) routeBodyFrame(f *protocol.Frame) {
	a.mu.Lock()
	ar := a.active[f.RequestID]
	a.mu.Unlock()
	if ar == nil {
		log.Printf("agent: body frame for unknown request (seq %d)", f.Seq)
		return
	}
	select {
	case ar.bodyCh <- f:
	case <-ar.done:
	default:
		log.Printf("agent: request body queue full, canceling %s", ar.reqID)
		ar.cancel()
	}
}

// serveRequest 组装请求体并转发到本地模型。
func (a *Agent) serveRequest(c *Connector, req protocol.Request, ar *activeRequest, ctx context.Context) {
	defer func() {
		a.mu.Lock()
		delete(a.active, ar.id16)
		a.mu.Unlock()
		ar.closeOnce.Do(func() { close(ar.done) })
		<-a.localSem
	}()

	// 1. 组装请求体（直到 last 帧）。
	body, err := a.assembleBody(ar, ctx)
	if err != nil {
		_ = c.writeControl(protocol.Error{Type: protocol.MsgError, RequestID: ar.reqID, Code: protocol.ErrInvalidRequest, Message: err.Error()})
		a.stats.RequestsFailed.Add(1)
		return
	}

	// 2. 构建本地请求。
	outReq, err := a.local.BuildRequest(ctx, req, body)
	if err != nil {
		_ = c.writeControl(protocol.Error{Type: protocol.MsgError, RequestID: ar.reqID, Code: protocol.ErrInvalidRequest, Message: err.Error()})
		a.stats.RequestsFailed.Add(1)
		return
	}

	start := time.Now()
	// 3. 发送。
	resp, err := a.local.Do(outReq)
	if err != nil {
		code := protocol.ErrUpstreamConnectionFailed
		if errors.Is(ctx.Err(), context.Canceled) {
			code = protocol.ErrCanceled
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = protocol.ErrUpstreamTimeout
		}
		_ = c.writeControl(protocol.Error{Type: protocol.MsgError, RequestID: ar.reqID, Code: code, Message: err.Error()})
		a.stats.RequestsFailed.Add(1)
		return
	}
	defer resp.Body.Close()

	// 4. 响应头。
	hdr := filterResponseHeaders(resp.Header)
	if err := c.writeControl(protocol.ResponseHeaders{
		Type:       protocol.MsgResponseHdr,
		RequestID:  ar.reqID,
		StatusCode: resp.StatusCode,
		Headers:    hdr,
	}); err != nil {
		return
	}

	// 5. 流式转发响应体。
	first := true
	seq := uint32(0)
	buf := make([]byte, 256*1024)
	ttft := time.Since(start).Milliseconds()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			seq++
			f := protocol.NewFrame(protocol.FrameResponseBody, ar.id16, seq, buf[:n])
			f.First = first
			first = false
			if rerr == io.EOF {
				f.Last = true
			}
			if err := c.WriteFrame(f); err != nil {
				return
			}
		}
		if rerr != nil && rerr != io.EOF {
			code := protocol.ErrUpstreamConnectionFailed
			if errors.Is(ctx.Err(), context.Canceled) {
				code = protocol.ErrCanceled
			} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code = protocol.ErrUpstreamTimeout
			}
			_ = c.writeControl(protocol.Error{
				Type: protocol.MsgError, RequestID: ar.reqID,
				Code: code, Message: rerr.Error(),
			})
			a.stats.RequestsFailed.Add(1)
			return
		}
		if rerr == io.EOF {
			break
		}
	}

	duration := time.Since(start).Milliseconds()
	if err := c.writeControl(protocol.Done{
		Type:       protocol.MsgDone,
		RequestID:  ar.reqID,
		StatusCode: resp.StatusCode,
		Headers:    hdr,
		TTFTMs:     ttft,
		DurationMs: duration,
	}); err != nil {
		return
	}
	a.stats.RequestsOK.Add(1)
}

// assembleBody 从帧通道组装完整请求体。
func (a *Agent) assembleBody(ar *activeRequest, ctx context.Context) ([]byte, error) {
	var buf bytes.Buffer
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ar.done:
			return nil, context.Canceled
		case <-timer.C:
			return nil, fmt.Errorf("request body assembly timeout")
		case f := <-ar.bodyCh:
			if int64(buf.Len()+len(f.Payload)) > ar.bodyLen {
				return nil, fmt.Errorf("request body exceeds declared length")
			}
			if _, err := buf.Write(f.Payload); err != nil {
				return nil, err
			}
			if f.Last {
				if int64(buf.Len()) != ar.bodyLen {
					return nil, fmt.Errorf("request body length mismatch")
				}
				return buf.Bytes(), nil
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(30 * time.Second)
		}
	}
}

// cancelRequest 取消指定请求。
func (a *Agent) cancelRequest(reqID, reason string) {
	a.mu.Lock()
	ar := a.active[protocol.RequestIDBytes(reqID)]
	a.mu.Unlock()
	if ar == nil {
		return
	}
	a.stats.CanceledTotal.Add(1)
	ar.cancel()
	log.Printf("agent: canceled request %s (%s)", reqID, reason)
}

// cancelAll 断开连接时取消全部请求。
func (a *Agent) cancelAll(reason string) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.active))
	for id := range a.active {
		ids = append(ids, string(id[:]))
	}
	a.mu.Unlock()
	for _, id := range ids {
		a.cancelRequest(id, reason)
	}
}

// enterDrain 进入 Drain 模式。
func (a *Agent) enterDrain(graceSec int) {
	a.mu.Lock()
	a.draining = true
	a.mu.Unlock()
	log.Printf("agent: draining mode (grace %ds)", graceSec)
}

// waitForDrain 等待活动请求结束；超出宽限期后取消剩余请求。
func (a *Agent) waitForDrain(graceSec int) int {
	if graceSec <= 0 {
		graceSec = 300
	}
	deadline := time.NewTimer(time.Duration(graceSec) * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if active := a.activeCount(); active == 0 {
			return 0
		}
		select {
		case <-deadline.C:
			a.cancelAll("drain_timeout")
			return a.activeCount()
		case <-tick.C:
		}
	}
}

// activeCount 返回当前活动请求数。
func (a *Agent) activeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.active)
}

// localHealthy 返回本地模型健康状态。
func (a *Agent) localHealthy() bool { return a.local.Healthy() }

// lastProbeOK 返回最近探测是否成功。
func (a *Agent) lastProbeOK() bool { return a.probeOK.Load() }

// markHeartbeatOK 记录心跳确认。
func (a *Agent) markHeartbeatOK() { a.heartbeatOK.Store(true) }

// filterResponseHeaders 过滤响应头（去 hop-by-hop）。
func filterResponseHeaders(h http.Header) map[string]string {
	out := make(map[string]string)
	for _, k := range []string{"Content-Type", "Content-Disposition", "Cache-Control", "ETag", "X-Request-Id"} {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}
