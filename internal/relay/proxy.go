package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"modelrelay/internal/protocol"
)

// errQueueFull / errQueueTimeout 是准入错误。
var (
	errQueueFull    = errors.New(protocol.ErrQueueFull)
	errQueueTimeout = errors.New(protocol.ErrQueueTimeout)
)

// bodyChunkSize 是请求体分片大小。
const bodyChunkSize = 128 * 1024

// openAIErrorBody 构造 OpenAI 兼容错误体。
func openAIErrorBody(code, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "relay_error",
			"param":   nil,
			"code":    code,
		},
	})
	return b
}

// writeRelayError 写一个 Relay 侧错误响应。
func writeRelayError(w http.ResponseWriter, code, message string) {
	status := protocol.HTTPStatus(code)
	if status == 499 {
		status = http.StatusRequestTimeout
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(openAIErrorBody(code, message))
}

// handleProxy 处理一个需要转发到 Agent 的请求。
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request, method, path, query string, body []byte) {
	reqID := uuid.NewString()
	model, stream, err := parseRouteInfo(path, body, r)
	if err != nil {
		writeRelayError(w, protocol.ErrInvalidRequest, err.Error())
		return
	}
	capability := CapabilityForPath(method, path)
	if stream && capability == "chat_completions" {
		capability = "chat_stream"
	}

	s.stats.RequestsTotal.Add(1)

	// 请求摘要记录（defer 在终态统一上报）。
	rec := &requestRecord{
		RequestID: reqID,
		Path:      path,
		Model:     model,
		Time:      time.Now(),
	}
	start := time.Now()
	defer func() {
		rec.DurationMs = time.Since(start).Milliseconds()
		s.recordRequest(rec)
	}()

	// 1. 准入（有界队列 + 全局并发）。
	ctx := r.Context()
	var cancel context.CancelFunc
	if s.cfg.RequestTimeoutMs > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(s.cfg.RequestTimeoutMs)*time.Millisecond)
		defer cancel()
	}
	release, err := s.acquire(ctx)
	if err != nil {
		if errors.Is(err, errQueueFull) {
			rec.Status, rec.ErrorCode = 429, protocol.ErrQueueFull
			writeRelayError(w, protocol.ErrQueueFull, "relay queue is full")
		} else if errors.Is(err, errQueueTimeout) {
			rec.Status, rec.ErrorCode = 503, protocol.ErrQueueTimeout
			writeRelayError(w, protocol.ErrQueueTimeout, "timed out waiting in relay queue")
		} else {
			rec.Status, rec.ErrorCode = 499, protocol.ErrCanceled
			writeRelayError(w, protocol.ErrCanceled, "request canceled")
		}
		s.stats.RequestsFailed.Add(1)
		return
	}
	defer release()

	// 2. 选择节点（全部繁忙时在队列超时内等待容量）。
	node, err := s.scheduler.WaitForNode(ctx, model, capability, time.Duration(s.cfg.QueueTimeoutMs)*time.Millisecond)
	if err != nil {
		s.stats.RequestsFailed.Add(1)
		code := classifySelectorErr(err)
		rec.Status, rec.ErrorCode = protocol.HTTPStatus(code), code
		writeRelayError(w, code, err.Error())
		return
	}
	rec.Node = node.ID
	if !node.Reserve() {
		s.stats.RequestsFailed.Add(1)
		rec.Status, rec.ErrorCode = 503, protocol.ErrNoAvailableNode
		writeRelayError(w, protocol.ErrNoAvailableNode, "node capacity changed")
		return
	}
	defer node.Release()

	// 3. 建立 pending 并发送请求。
	id16 := protocol.RequestIDBytes(reqID)
	pend := newPendingRequest(id16, reqID)
	s.pending.add(pend)
	defer s.pending.remove(id16)

	reqHeaders := whitelistHeaders(r.Header)
	reqMsg := protocol.Request{
		Type:         protocol.MsgRequest,
		RequestID:    reqID,
		Method:       method,
		Path:         path,
		Query:        query,
		Headers:      reqHeaders,
		BodyLen:      int64(len(body)),
		BodyEncoding: "raw",
		Stream:       stream,
	}
	if err := s.sendToNode(node.ID, reqMsg); err != nil {
		s.stats.RequestsFailed.Add(1)
		rec.Status, rec.ErrorCode = 503, protocol.ErrNoAvailableNode
		writeRelayError(w, protocol.ErrNoAvailableNode, "node connection lost: "+err.Error())
		return
	}

	// 4. 发送请求体分片。
	seq := uint32(0)
	for off := 0; off < len(body); off += bodyChunkSize {
		seq++
		end := off + bodyChunkSize
		if end > len(body) {
			end = len(body)
		}
		f := protocol.NewFrame(protocol.FrameRequestBody, protocol.RequestIDBytes(reqID), seq, body[off:end])
		f.First = off == 0
		f.Last = end == len(body)
		if err := s.sendFrameToNode(node.ID, f); err != nil {
			s.stats.RequestsFailed.Add(1)
			rec.Status, rec.ErrorCode = 503, protocol.ErrNoAvailableNode
			writeRelayError(w, protocol.ErrNoAvailableNode, "node connection lost: "+err.Error())
			return
		}
	}
	if len(body) == 0 {
		// 空请求体也发一个 last 帧。
		f := protocol.NewFrame(protocol.FrameRequestBody, protocol.RequestIDBytes(reqID), 1, nil)
		f.First, f.Last = true, true
		_ = s.sendFrameToNode(node.ID, f)
	}

	// 5. 转发响应。
	s.forwardResponse(w, ctx, node, pend, stream, reqID, rec)
}

// forwardResponse 将 Agent 的响应帧流式转发给客户端。
func (s *Server) forwardResponse(w http.ResponseWriter, ctx context.Context, node *Node, pend *pendingRequest, stream bool, reqID string, rec *requestRecord) {
	var (
		ttftTimer *time.Timer
		ttftC     <-chan time.Time
	)
	if s.cfg.TTFTTimeoutMs > 0 {
		ttftTimer = time.NewTimer(time.Duration(s.cfg.TTFTTimeoutMs) * time.Millisecond)
		ttftC = ttftTimer.C
		defer ttftTimer.Stop()
	}
	start := time.Now()

	wroteHeader := false
	idleTimer := time.NewTimer(time.Hour)
	defer idleTimer.Stop()
	idleC := (<-chan time.Time)(nil)
	resetIdle := func() {
		d := time.Duration(s.cfg.IdleTimeoutMs) * time.Millisecond
		if d <= 0 {
			idleC = nil
			return
		}
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(d)
		idleC = idleTimer.C
	}
	resetIdle()

	applyHeaders := func(rh protocol.ResponseHeaders) {
		for k, v := range rh.Headers {
			if v == "" {
				continue
			}
			switch k {
			case "Content-Type", "Content-Disposition", "Cache-Control", "ETag", "X-Request-Id":
				w.Header().Set(k, v)
			}
		}
		w.WriteHeader(rh.StatusCode)
		wroteHeader = true
	}

	for {
		select {
		case <-ctx.Done():
			// 客户端取消或整体超时。
			s.stats.RequestsCanceled.Add(1)
			_ = s.sendToNode(node.ID, protocol.Cancel{Type: protocol.MsgCancel, RequestID: reqID, Reason: "client_closed"})
			if !wroteHeader {
				rec.Status, rec.ErrorCode = 499, protocol.ErrCanceled
				writeRelayError(w, protocol.ErrCanceled, "request canceled")
			}
			return

		case rh := <-pend.headers:
			rec.Status = rh.StatusCode
			rec.TTFTMs = time.Since(start).Milliseconds()
			applyHeaders(rh)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			if ttftTimer != nil {
				ttftTimer.Stop()
				ttftC = nil
			}

		case <-ttftC:
			// 首帧超时。
			s.stats.RequestsFailed.Add(1)
			rec.Status, rec.ErrorCode = 504, protocol.ErrTTFTTimeout
			_ = s.sendToNode(node.ID, protocol.Cancel{Type: protocol.MsgCancel, RequestID: reqID, Reason: "ttft_timeout"})
			if !wroteHeader {
				writeRelayError(w, protocol.ErrTTFTTimeout, "first token timeout")
			}
			return

		case <-idleC:
			s.stats.RequestsFailed.Add(1)
			rec.Status, rec.ErrorCode = 504, protocol.ErrIdleTimeout
			_ = s.sendToNode(node.ID, protocol.Cancel{Type: protocol.MsgCancel, RequestID: reqID, Reason: "idle_timeout"})
			if !wroteHeader {
				writeRelayError(w, protocol.ErrIdleTimeout, "stream idle timeout")
			}
			return

		case f := <-pend.frames:
			if !wroteHeader {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				wroteHeader = true
			}
			if _, err := w.Write(f.Payload); err != nil {
				log.Printf("relay: write to client failed: %v", err)
				return
			}
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			resetIdle()
			if ttftTimer != nil {
				ttftTimer.Stop()
				ttftC = nil
			}

		case <-pend.done:
			// done 与最后一帧可能同时就绪；先排空已到达的帧，避免 select 随机丢帧。
		drainDone:
			for {
				select {
				case f := <-pend.frames:
					if _, werr := w.Write(f.Payload); werr != nil {
						return
					}
					if fl, ok := w.(http.Flusher); ok {
						fl.Flush()
					}
				default:
					break drainDone
				}
			}
			s.stats.RequestsSuccess.Add(1)
			if rec.Status == 0 {
				rec.Status = 200
			}
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			return

		case e := <-pend.err:
			// 排空已到达的帧（流中途出错时保留已产出数据）。
		drainErr:
			for {
				select {
				case f := <-pend.frames:
					if _, werr := w.Write(f.Payload); werr != nil {
						return
					}
					if fl, ok := w.(http.Flusher); ok {
						fl.Flush()
					}
				default:
					break drainErr
				}
			}
			s.stats.RequestsFailed.Add(1)
			rec.Status, rec.ErrorCode = protocol.HTTPStatus(e.Code), e.Code
			if !wroteHeader {
				writeRelayError(w, e.Code, e.Message)
			}
			return
		}
	}
}

// acquire 是有界队列 + 全局并发准入。
func (s *Server) acquire(ctx context.Context) (func(), error) {
	if s.waiting.Load() >= int64(s.cfg.QueueLength) {
		s.stats.QueueFullTotal.Add(1)
		return nil, errQueueFull
	}
	s.waiting.Add(1)
	defer s.waiting.Add(-1)
	timer := time.NewTimer(time.Duration(s.cfg.QueueTimeoutMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, nil
	case <-timer.C:
		return nil, errQueueTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// classifySelectorErr 将调度错误转换为协议错误码。
func classifySelectorErr(err error) string {
	switch {
	case errors.Is(err, ErrModelNotFound):
		return protocol.ErrModelNotFound
	case errors.Is(err, ErrCapabilityNotSupported):
		return protocol.ErrCapabilityNotSupported
	case errors.Is(err, ErrNoNodeOnline):
		return protocol.ErrNoAvailableNode
	case errors.Is(err, ErrAllNodesBusy):
		return protocol.ErrNoAvailableNode
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.ErrQueueTimeout
	default:
		return protocol.ErrNoAvailableNode
	}
}

// whitelistHeaders 提取允许透传的请求 Header。
func whitelistHeaders(h http.Header) map[string]string {
	out := make(map[string]string)
	for _, k := range []string{
		"Content-Type", "Accept", "OpenAI-Beta", "Idempotency-Key",
		"Content-Disposition", "X-Request-Id",
	} {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

// parseRouteInfo 从请求体提取 model 与 stream（仅在需要时解析 JSON）。
func parseRouteInfo(path string, body []byte, r *http.Request) (model string, stream bool, err error) {
	if path == "/v1/models" {
		return "", false, nil
	}
	ct := r.Header.Get("Content-Type")
	if len(body) == 0 {
		return "", false, fmt.Errorf("empty request body")
	}
	// multipart：请求体已读入内存，先恢复 Body 再解析表单。
	if len(ct) >= 19 && ct[:19] == "multipart/form-data" {
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			return "", false, fmt.Errorf("parse multipart: %w", err)
		}
		return r.FormValue("model"), false, nil
	}
	var info struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", false, fmt.Errorf("invalid JSON body: %w", err)
	}
	return info.Model, info.Stream, nil
}
