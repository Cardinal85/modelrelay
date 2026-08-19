package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"modelrelay/internal/certs"
	"modelrelay/internal/protocol"
	"modelrelay/internal/store"
)

// WSSPath 是 Agent 接入路径。
const WSSPath = "/agent/v1/connect"

// upgrader 只接受本程序 Agent 的固定 Origin，拒绝浏览器跨站连接。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return r.Header.Get("Origin") == protocol.AgentOrigin
	},
}

// wsConn 包装底层 WebSocket 连接，串行化写入（gorilla 只允许一个并发写者）。
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsConn) WriteControl(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return w.conn.WriteMessage(websocket.TextMessage, data)
}

func (w *wsConn) WriteFrame(f *protocol.Frame) error {
	data, err := f.Encode()
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
	return w.conn.WriteMessage(websocket.BinaryMessage, data)
}

// WSSServer 是 Agent 接入服务（TLS + mTLS）。
type WSSServer struct {
	srv      *Server
	httpSrv  *http.Server
	listener net.Listener
	store    *store.Store

	connsMu sync.Mutex
	conns   map[*websocket.Conn]struct{}
}

// SetStore 挂载证书状态存储。未配置时仅使用 TLS 的证书链校验。
func (w *WSSServer) SetStore(st *store.Store) { w.store = st }

// NewWSSServer 创建 WSS 服务，加载 Relay 服务端证书与 Agent CA。
func NewWSSServer(s *Server, certFile, keyFile, caFile, listenAddr string) (*WSSServer, error) {
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("relay: read agent_ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("relay: invalid agent_ca PEM")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("relay: load relay cert/key: %w", err)
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("relay: listen %s: %w", listenAddr, err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
	w := &WSSServer{srv: s, listener: tls.NewListener(ln, tlsCfg), conns: make(map[*websocket.Conn]struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc(WSSPath, w.handleConnect)
	w.httpSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return w, nil
}

// Start 启动 WSS 服务（阻塞）。
func (w *WSSServer) Start() error {
	err := w.httpSrv.Serve(w.listener)
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Addr 返回监听地址。
func (w *WSSServer) Addr() net.Addr { return w.listener.Addr() }

// Close 关闭服务（优雅，等待连接结束）。
func (w *WSSServer) Close(ctx context.Context) error {
	w.closeAllConns()
	return w.httpSrv.Shutdown(ctx)
}

// ForceClose 立即关闭监听与全部连接（测试/故障演练用）。
func (w *WSSServer) ForceClose() error {
	w.closeAllConns()
	return w.httpSrv.Close()
}

// closeAllConns 关闭全部已升级的 WebSocket 连接（hijacked 连接不受 http.Server 管理）。
func (w *WSSServer) closeAllConns() {
	w.connsMu.Lock()
	defer w.connsMu.Unlock()
	for c := range w.conns {
		_ = c.Close()
	}
}

// handleConnect 处理 Agent 的 WSS 连接。
func (w *WSSServer) handleConnect(rw http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != protocol.AgentOrigin {
		http.Error(rw, "origin not allowed", http.StatusForbidden)
		return
	}
	raw, err := upgrader.Upgrade(rw, r, nil)
	if err != nil {
		log.Printf("relay: ws upgrade failed: %v", err)
		return
	}
	raw.SetReadLimit(protocol.MaxWSMessage)
	conn := &wsConn{conn: raw}
	w.connsMu.Lock()
	w.conns[raw] = struct{}{}
	w.connsMu.Unlock()
	defer func() {
		w.connsMu.Lock()
		delete(w.conns, raw)
		w.connsMu.Unlock()
		raw.Close()
	}()

	// 1. 读取 hello（带超时）。
	_ = raw.SetReadDeadline(time.Now().Add(15 * time.Second))
	mt, data, err := raw.ReadMessage()
	if err != nil {
		log.Printf("relay: read hello: %v", err)
		return
	}
	if mt != websocket.TextMessage {
		log.Printf("relay: hello must be text message")
		return
	}
	if int64(len(data)) > protocol.MaxControlMessage {
		log.Printf("relay: hello too large")
		return
	}
	typ, rawMsg, err := protocol.ParseControl(data)
	if err != nil || typ != protocol.MsgHello {
		_ = conn.WriteControl(protocol.Error{Type: protocol.MsgError, Code: protocol.ErrInvalidRequest, Message: "expected hello"})
		return
	}
	var hello protocol.Hello
	if err := json.Unmarshal(rawMsg, &hello); err != nil {
		_ = conn.WriteControl(protocol.Error{Type: protocol.MsgError, Code: protocol.ErrInvalidRequest, Message: "malformed hello"})
		return
	}

	// 2. 校验协议版本。
	if hello.Protocol != protocol.ProtocolVersion {
		_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrUnsupportedProtocol})
		return
	}
	if strings.TrimSpace(hello.NodeID) == "" {
		_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrInvalidRequest})
		return
	}

	// 3. 校验客户端证书：CA 签发（TLS 层已做）+ CN 与 node_id 一致。
	peerCN := ""
	certDER := []byte(nil)
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		pc := r.TLS.PeerCertificates[0]
		peerCN = pc.Subject.CommonName
		certDER = pc.Raw
	}
	if peerCN == "" {
		_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrUnauthorized})
		return
	}
	if !certIdentityMatches(r.TLS.PeerCertificates[0], hello.NodeID) {
		log.Printf("relay: identity mismatch: cert CN=%s node_id=%s", peerCN, hello.NodeID)
		_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrIdentityMismatch})
		return
	}
	peer := r.TLS.PeerCertificates[0]
	peerSerial := peer.SerialNumber.String()
	if w.store != nil {
		revoked, err := w.store.IsCertRevoked(peerSerial)
		if err != nil {
			log.Printf("relay: certificate status lookup failed: %v", err)
			_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrUnauthorized})
			return
		}
		if revoked {
			log.Printf("relay: revoked certificate rejected: serial=%s node=%s", peerSerial, hello.NodeID)
			_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrUnauthorized})
			return
		}
		if err := w.store.EnsureCertMeta(store.CertMeta{
			NodeID: hello.NodeID, Serial: peerSerial, Subject: peer.Subject.String(),
			Issuer: peer.Issuer.String(), NotBefore: peer.NotBefore, NotAfter: peer.NotAfter,
			Status: "active", Fingerprint: CertFingerprint(certDER),
		}); err != nil {
			log.Printf("relay: certificate metadata write failed: %v", err)
			_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrInternal})
			return
		}
	}

	// 4. 原子重复节点检查与注册。
	node := &Node{
		ID:           hello.NodeID,
		AgentVersion: hello.AgentVersion,
		Platform:     protocol.Platform{OS: hello.Platform.OS, Arch: hello.Platform.Arch},
		State:        StateOnline,
		Limits:       protocol.Limits{MaxConcurrency: hello.Limits.MaxConcurrency},
		Models:       make(map[string]*ModelCap, len(hello.Models)),
		Heartbeat:    time.Now(),
		ConnectedAt:  time.Now(),
		CertHash:     CertFingerprint(certDER),
		CertSerial:   peerSerial,
	}
	for _, m := range hello.Models {
		node.Models[m.ID] = &ModelCap{ID: m.ID, Capabilities: m.Capabilities, CapabilitiesComplete: m.CapabilitiesComplete, ProbeTime: time.Now()}
	}
	if node.Limits.MaxConcurrency <= 0 {
		node.Limits.MaxConcurrency = 8
	}
	if !w.srv.reg.TryRegister(node) {
		log.Printf("relay: duplicate node %s", hello.NodeID)
		_ = conn.WriteControl(protocol.HelloAck{Type: protocol.MsgHelloAck, Protocol: hello.Protocol, Accepted: false, Reason: protocol.ErrDuplicateNode})
		return
	}

	// 5. 注册连接句柄。
	nc := &nodeConn{
		nodeID: hello.NodeID,
		node:   node,
		send:   func(msg any) error { return conn.WriteControl(msg) },
		sendF:  func(f *protocol.Frame) error { return conn.WriteFrame(f) },
	}
	w.srv.conns.put(hello.NodeID, nc)

	// 6. 回复 hello_ack。
	if err := conn.WriteControl(protocol.HelloAck{
		Type:             protocol.MsgHelloAck,
		Protocol:         protocol.ProtocolVersion,
		RelayID:          w.srv.cfg.RelayID,
		Accepted:         true,
		RegisteredModels: len(node.Models),
		MaxConcurrency:   node.Limits.MaxConcurrency,
		HeartbeatSeconds: w.heartbeatSeconds(),
	}); err != nil {
		w.teardown(node)
		return
	}

	log.Printf("relay: node %s online (%s/%s, %d models)", hello.NodeID, hello.Platform.OS, hello.Platform.Arch, len(node.Models))
	w.srv.stats.NodesOnline.Add(1)

	// 7. 心跳监控 + 读循环。
	hbStop := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		w.heartbeatMonitor(node, raw, hbStop)
	}()
	w.readLoop(node, conn, raw)
	close(hbStop)
	hbWG.Wait()
	w.teardown(node)
}

func certIdentityMatches(cert *x509.Certificate, nodeID string) bool {
	if len(cert.URIs) > 0 {
		for _, uri := range cert.URIs {
			if uri != nil && uri.String() == certs.AgentIdentityURI(nodeID) {
				return cert.Subject.CommonName == nodeID
			}
		}
		return false
	}
	// 兼容升级前仅使用 CN 的客户端证书；新签发证书优先使用 URI SAN。
	return cert.Subject.CommonName == nodeID
}

// heartbeatSeconds 返回期望心跳间隔。
func (w *WSSServer) heartbeatSeconds() int {
	n := w.srv.cfg.HeartbeatTimeoutS / 3
	if n < 5 {
		n = 5
	}
	return n
}

// heartbeatMonitor 检查心跳超时：超时 → suspect，翻倍 → 关闭连接。
func (w *WSSServer) heartbeatMonitor(node *Node, raw *websocket.Conn, stop chan struct{}) {
	tick := time.NewTicker(time.Duration(w.heartbeatSeconds()) * time.Second)
	defer tick.Stop()
	timeout := time.Duration(w.srv.cfg.HeartbeatTimeoutS) * time.Second
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			age := time.Since(node.LastHeartbeat())
			state := node.Snapshot().State
			if age > 2*timeout {
				log.Printf("relay: node %s heartbeat timeout, closing", node.ID)
				w.srv.stats.HeartbeatMissed.Add(1)
				_ = raw.Close()
				return
			}
			if age > timeout && state != StateSuspect {
				node.SetState(StateSuspect)
				w.srv.stats.HeartbeatMissed.Add(1)
				log.Printf("relay: node %s suspect (no heartbeat for %s)", node.ID, age.Round(time.Second))
			}
		}
	}
}

// readLoop 读取并分发 Agent 消息。
func (w *WSSServer) readLoop(node *Node, conn *wsConn, raw *websocket.Conn) {
	deadline := time.Duration(2*w.srv.cfg.HeartbeatTimeoutS+5) * time.Second
	for {
		_ = raw.SetReadDeadline(time.Now().Add(deadline))
		mt, data, err := raw.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				log.Printf("relay: node %s closed normally", node.ID)
			} else {
				log.Printf("relay: node %s read error: %v", node.ID, err)
			}
			return
		}
		switch mt {
		case websocket.TextMessage:
			if int64(len(data)) > protocol.MaxControlMessage {
				log.Printf("relay: node %s control message too large (%d)", node.ID, len(data))
				return
			}
			typ, rawMsg, err := protocol.ParseControl(data)
			if err != nil {
				log.Printf("relay: node %s bad control: %v", node.ID, err)
				continue
			}
			if !w.dispatchControl(node, conn, typ, rawMsg) {
				return
			}
		case websocket.BinaryMessage:
			f, err := protocol.DecodeFrame(bytes.NewReader(data))
			if err != nil {
				log.Printf("relay: node %s bad frame: %v", node.ID, err)
				continue
			}
			w.dispatchFrame(node, f)
		}
	}
}

func (w *WSSServer) dispatchControl(node *Node, conn *wsConn, typ string, raw []byte) bool {
	switch typ {
	case protocol.MsgHeartbeat:
		var hb protocol.Heartbeat
		if err := json.Unmarshal(raw, &hb); err != nil {
			return true
		}
		node.MarkHeartbeat(hb.Seq, hb.LocalModelOK)
		_ = conn.WriteControl(protocol.HeartbeatAck{Type: protocol.MsgHeartbeatAck, TS: time.Now().Unix(), OK: true})
	case protocol.MsgResponseHdr:
		var rh protocol.ResponseHeaders
		if err := json.Unmarshal(raw, &rh); err != nil {
			return true
		}
		if p := w.pendingOwnedBy(node, protocol.RequestIDBytes(rh.RequestID)); p != nil {
			select {
			case p.headers <- rh:
			default:
			}
		}
	case protocol.MsgDone:
		var d protocol.Done
		if err := json.Unmarshal(raw, &d); err != nil {
			return true
		}
		if p := w.pendingOwnedBy(node, protocol.RequestIDBytes(d.RequestID)); p != nil {
			select {
			case p.done <- d:
			default:
			}
		}
	case protocol.MsgError:
		var e protocol.Error
		if err := json.Unmarshal(raw, &e); err != nil {
			return true
		}
		if p := w.pendingOwnedBy(node, protocol.RequestIDBytes(e.RequestID)); p != nil {
			select {
			case p.err <- e:
			default:
			}
		}
	case protocol.MsgDrainAck:
		var da protocol.DrainAck
		if err := json.Unmarshal(raw, &da); err != nil {
			return true
		}
		log.Printf("relay: node %s drain ack: %d active, draining=%v", node.ID, da.ActiveRequests, da.Draining)
	case protocol.MsgModelsUpdate:
		var mu protocol.ModelsUpdate
		if err := json.Unmarshal(raw, &mu); err != nil {
			return true
		}
		newModels := make(map[string]*ModelCap, len(mu.Models))
		for _, m := range mu.Models {
			newModels[m.ID] = &ModelCap{ID: m.ID, Capabilities: m.Capabilities, CapabilitiesComplete: m.CapabilitiesComplete, ProbeTime: time.Now()}
		}
		node.SetModels(newModels)
		log.Printf("relay: node %s models updated: %d models", node.ID, len(newModels))
	case protocol.MsgBye:
		log.Printf("relay: node %s bye", node.ID)
		return false
	}
	return true
}

func (w *WSSServer) pendingOwnedBy(node *Node, reqID [16]byte) *pendingRequest {
	p := w.srv.pending.get(reqID)
	if p == nil || p.nodeID != node.ID {
		return nil
	}
	return p
}

func (w *WSSServer) dispatchFrame(node *Node, f *protocol.Frame) {
	p := w.pendingOwnedBy(node, f.RequestID)
	if p == nil {
		log.Printf("relay: frame for unknown or foreign request (seq %d), dropping", f.Seq)
		return
	}
	select {
	case p.frames <- f:
	case <-p.closed:
	default:
		// 不能阻塞 WSS 读循环，否则心跳和其它请求也会被拖死。
		requestID := p.requestID
		_ = w.srv.sendToNode(node.ID, protocol.Cancel{Type: protocol.MsgCancel, RequestID: requestID, Reason: "relay_frame_queue_full"})
		select {
		case p.err <- protocol.Error{Type: protocol.MsgError, RequestID: requestID, Code: protocol.ErrInternal, Message: "relay response buffer full"}:
		default:
		}
	}
}

// teardown 清理节点：移除注册、更新状态。
func (w *WSSServer) teardown(node *Node) {
	if !w.srv.reg.RemoveIfSame(node.ID, node) {
		return
	}
	w.srv.conns.delIf(node.ID, node)
	node.SetState(StateOffline)
	w.srv.stats.NodesOffline.Add(1)
	log.Printf("relay: node %s offline", node.ID)
}

// DrainNode 请求节点进入 Drain。
func (s *Server) DrainNode(nodeID string, graceSec int) error {
	n := s.reg.Get(nodeID)
	if n == nil {
		return fmt.Errorf("node %s not found", nodeID)
	}
	state := n.Snapshot().State
	if state != StateOnline && state != StateDegraded && state != StateSuspect {
		return fmt.Errorf("node %s not drainable (state %s)", nodeID, state)
	}
	n.SetState(StateDraining)
	if err := s.sendToNode(nodeID, protocol.Drain{Type: protocol.MsgDrain, GraceSeconds: graceSec}); err != nil {
		return err
	}
	return nil
}

// KickNode 踢出节点（强制断开）。
func (s *Server) KickNode(nodeID string) error {
	n := s.reg.Get(nodeID)
	if n == nil {
		return fmt.Errorf("node %s not found", nodeID)
	}
	nc := s.conns.get(nodeID)
	if nc == nil {
		s.reg.Remove(nodeID)
		n.SetState(StateOffline)
		return nil
	}
	_ = nc.send(protocol.Bye{Type: protocol.MsgBye, Reason: "kicked"})
	s.reg.Remove(nodeID)
	s.conns.del(nodeID)
	n.SetState(StateOffline)
	return nil
}
