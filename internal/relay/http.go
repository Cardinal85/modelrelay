package relay

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"modelrelay/internal/protocol"
)

// UpstreamServer 是面向 New API 的 OpenAI-compatible HTTP 上游。
type UpstreamServer struct {
	srv      *Server
	httpSrv  *http.Server
	listener net.Listener
}

// NewUpstreamServer 创建 HTTP 上游服务。
func NewUpstreamServer(s *Server, listenAddr string) (*UpstreamServer, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("relay: listen %s: %w", listenAddr, err)
	}
	u := &UpstreamServer{srv: s, listener: ln}
	mux := http.NewServeMux()
	mux.HandleFunc("/", u.handle)
	u.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return u, nil
}

// Start 启动 HTTP 上游服务（阻塞）。
func (u *UpstreamServer) Start() error {
	err := u.httpSrv.Serve(u.listener)
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Addr 返回监听地址。
func (u *UpstreamServer) Addr() net.Addr { return u.listener.Addr() }

// Close 关闭服务。
func (u *UpstreamServer) Close(ctx context.Context) error { return u.httpSrv.Shutdown(ctx) }

// handle 是 HTTP 上游入口。
func (u *UpstreamServer) handle(w http.ResponseWriter, r *http.Request) {
	// 1. 内部认证。
	if u.srv.cfg.InternalAuthToken != "" {
		got := r.Header.Get("Authorization")
		got = strings.TrimPrefix(got, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(u.srv.cfg.InternalAuthToken)) != 1 {
			writeRelayError(w, protocol.ErrUnauthorized, "invalid internal auth token")
			return
		}
	}

	path := r.URL.Path
	// 规范化：去掉尾部斜杠（/v1/models/ → /v1/models）。
	if strings.HasSuffix(path, "/") && path != "/" {
		path = strings.TrimSuffix(path, "/")
	}

	// 2. 模型目录（Relay 自身提供，无需节点）。
	if path == "/v1/models" {
		u.handleModels(w, r)
		return
	}
	if strings.HasPrefix(path, "/v1/models/") {
		u.handleModelDetail(w, r, strings.TrimPrefix(path, "/v1/models/"))
		return
	}

	// 3. 白名单路径检查。
	if !IsAllowedPath(r.Method, path) {
		writeRelayError(w, protocol.ErrInvalidPath, fmt.Sprintf("path %s not allowed", path))
		return
	}

	// 4. 读取请求体（限制大小）。
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, u.srv.cfg.MaxBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeRelayError(w, protocol.ErrBodyTooLarge, "request body too large")
			return
		}
		writeRelayError(w, protocol.ErrInvalidRequest, "failed to read body: "+err.Error())
		return
	}

	// 5. 转发。
	u.srv.handleProxy(w, r, r.Method, path, r.URL.RawQuery, body)
}

// handleModels 返回模型目录。
func (u *UpstreamServer) handleModels(w http.ResponseWriter, r *http.Request) {
	dir := u.srv.reg.ModelDirectory()
	data := make([]map[string]any, 0, len(dir))
	for _, e := range dir {
		data = append(data, map[string]any{
			"id":         e.ID,
			"object":     "model",
			"created":    0,
			"owned_by":   "relay",
			"nodes":      e.Nodes,
			"capabilities": e.Capabilities,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

// handleModelDetail 返回单个模型。
func (u *UpstreamServer) handleModelDetail(w http.ResponseWriter, r *http.Request, modelID string) {
	for _, e := range u.srv.reg.ModelDirectory() {
		if e.ID == modelID {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           e.ID,
				"object":       "model",
				"created":      0,
				"owned_by":     "relay",
				"nodes":        e.Nodes,
				"capabilities": e.Capabilities,
			})
			return
		}
	}
	writeRelayError(w, protocol.ErrModelNotFound, "model not found: "+modelID)
}

// Log 简单日志辅助（后续接入统一 logger）。
func logf(format string, args ...any) { log.Printf(format, args...) }
