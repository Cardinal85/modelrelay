// Package agent 实现 Agent：主动连接 Relay、透明转发本地模型请求、能力探测与健康上报。
package agent

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"modelrelay/internal/config"
	"modelrelay/internal/protocol"
	"modelrelay/internal/version"
)

// Connector 负责与 Relay 的连接、重连与消息分发。
type Connector struct {
	agent *Agent

	conn    *websocket.Conn
	writeMu sync.Mutex

	stop chan struct{}
	wg   sync.WaitGroup
}

// NewConnector 创建连接器。
func NewConnector(a *Agent) *Connector {
	return &Connector{agent: a, stop: make(chan struct{})}
}

// Run 持续尝试连接 Relay（阻塞直到 Stop）。
func (c *Connector) Run() {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}
	attempt := 0
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		relays := sortedRelays(c.agent.cfg.Relays)
		connected := false
		for i, r := range relays {
			select {
			case <-c.stop:
				return
			default:
			}
			if err := c.connectOnce(r.URL, i, len(relays)); err == nil {
				connected = true
				break
			} else {
				log.Printf("agent: connect %s failed: %v", r.URL, err)
			}
		}
		if connected {
			// 连接被 Relay 主动断开时也要退避，避免服务端故障/证书拒绝
			// 导致 Agent 紧密重连形成连接风暴。
			d := backoff[attempt]
			if attempt < len(backoff)-1 {
				attempt++
			}
			d += time.Duration(rand.Int63n(1000)) * time.Millisecond
			if !c.waitBeforeRetry(d) {
				return
			}
		} else {
			d := backoff[attempt]
			if attempt < len(backoff)-1 {
				attempt++
			}
			d += time.Duration(rand.Int63n(1000)) * time.Millisecond // 随机抖动
			log.Printf("agent: all relays unreachable, retry in %s", d.Round(time.Second))
			if !c.waitBeforeRetry(d) {
				return
			}
		}
	}
}

func (c *Connector) waitBeforeRetry(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-c.stop:
		return false
	case <-timer.C:
		return true
	}
}

// connectOnce 尝试连接指定 Relay（relayIdx 为排序后的索引），成功则运行读循环直到断开。
func (c *Connector) connectOnce(rawURL string, relayIdx, relayCount int) error {
	tlsCfg, err := c.agent.tlsConfig()
	if err != nil {
		return err
	}
	dialer := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: 15 * time.Second,
	}
	conn, _, err := dialer.Dial(rawURL, agentOriginHeader())
	if err != nil {
		return err
	}
	conn.SetReadLimit(protocol.MaxWSMessage)
	c.conn = conn
	c.agent.stats.Reconnects.Add(1)

	// 1. hello。
	models := c.agent.modelsForHello()
	hello := protocol.Hello{
		Type:             protocol.MsgHello,
		Protocol:         protocol.ProtocolVersion,
		NodeID:           c.agent.cfg.NodeID,
		AgentVersion:     version.AgentVersion(),
		Platform:         protocol.Platform{OS: config.PlatformOS(), Arch: config.PlatformArch()},
		Models:           models,
		Limits:           protocol.Limits{MaxConcurrency: c.agent.cfg.Local.MaxConcurrency},
		HeartbeatSeconds: c.agent.cfg.HeartbeatSeconds,
	}
	if err := c.writeControl(hello); err != nil {
		conn.Close()
		return err
	}

	// 2. 等待 hello_ack。
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return err
	}
	if mt != websocket.TextMessage {
		conn.Close()
		return fmt.Errorf("expected hello_ack")
	}
	typ, raw, err := protocol.ParseControl(data)
	if err != nil || typ != protocol.MsgHelloAck {
		conn.Close()
		return fmt.Errorf("expected hello_ack, got %v", err)
	}
	var ack protocol.HelloAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		conn.Close()
		return err
	}
	if !ack.Accepted {
		conn.Close()
		return fmt.Errorf("relay rejected: %s", ack.Reason)
	}
	log.Printf("agent: connected to %s (relay %s, %d models registered)", rawURL, ack.RelayID, ack.RegisteredModels)

	// 3. 启动心跳与读循环。
	hbStop := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		c.heartbeatLoop(conn, hbStop)
	}()
	// 主备模式：连接的是备用 Relay 时，周期探测主 Relay 是否恢复。
	ppStop := make(chan struct{})
	var ppWG sync.WaitGroup
	if relayIdx > 0 && c.agent.cfg.PreferPrimaryIntervalSec > 0 {
		ppWG.Add(1)
		go func() {
			defer ppWG.Done()
			c.watchPrimary(relayIdx, conn, ppStop)
		}()
	}
	c.readLoop(conn)
	close(hbStop)
	close(ppStop)
	hbWG.Wait()
	ppWG.Wait()

	// 4. 断开：取消全部活动请求。
	c.agent.cancelAll("relay_disconnected")
	conn.Close()
	return nil
}

// watchPrimary 周期检查更高优先级的 Relay 是否可用；可用则断开当前备用连接以触发回切。
func (c *Connector) watchPrimary(currentIdx int, conn *websocket.Conn, stop chan struct{}) {
	interval := time.Duration(c.agent.cfg.PreferPrimaryIntervalSec) * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	relays := sortedRelays(c.agent.cfg.Relays)
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			for _, r := range relays[:currentIdx] {
				tlsCfg, err := c.agent.tlsConfig()
				if err != nil {
					return
				}
				d := &websocket.Dialer{
					TLSClientConfig:  tlsCfg,
					HandshakeTimeout: 5 * time.Second,
				}
				c2, _, err := d.Dial(r.URL, agentOriginHeader())
				if err == nil {
					_ = c2.Close()
					log.Printf("agent: primary %s reachable, switching back", r.URL)
					_ = conn.Close()
					return
				}
			}
		}
	}
}

// readLoop 读取并分发 Relay 消息。
func (c *Connector) readLoop(conn *websocket.Conn) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch mt {
		case websocket.TextMessage:
			if int64(len(data)) > protocol.MaxControlMessage {
				log.Printf("agent: control message too large (%d)", len(data))
				return
			}
			typ, raw, err := protocol.ParseControl(data)
			if err != nil {
				log.Printf("agent: bad control: %v", err)
				continue
			}
			if !c.dispatchControl(typ, raw) {
				return
			}
		case websocket.BinaryMessage:
			c.dispatchFrame(data)
		}
	}
}

func (c *Connector) dispatchControl(typ string, raw []byte) bool {
	switch typ {
	case protocol.MsgHeartbeatAck:
		c.agent.markHeartbeatOK()
	case protocol.MsgRequest:
		var req protocol.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return true
		}
		c.agent.handleRequest(c, req)
	case protocol.MsgCancel:
		var cn protocol.Cancel
		if err := json.Unmarshal(raw, &cn); err != nil {
			return true
		}
		c.agent.cancelRequest(cn.RequestID, cn.Reason)
	case protocol.MsgDrain:
		var d protocol.Drain
		if err := json.Unmarshal(raw, &d); err != nil {
			return true
		}
		c.agent.enterDrain(d.GraceSeconds)
		go func(grace int) {
			active := c.agent.waitForDrain(grace)
			_ = c.writeControl(protocol.DrainAck{Type: protocol.MsgDrainAck, ActiveRequests: active, Draining: true})
		}(d.GraceSeconds)
	case protocol.MsgError:
		var e protocol.Error
		if err := json.Unmarshal(raw, &e); err != nil {
			return true
		}
		if e.RequestID != "" {
			c.agent.cancelRequest(e.RequestID, e.Code)
		}
	case protocol.MsgModelsUpdate:
		// Relay → Agent 方向不使用（Agent 主动上报）。
	case protocol.MsgProbe:
		var p protocol.Probe
		if err := json.Unmarshal(raw, &p); err != nil {
			return true
		}
		go c.agent.probe.RunOnce()
	case protocol.MsgBye:
		log.Printf("agent: relay closed (bye)")
		return false
	}
	return true
}

func (c *Connector) dispatchFrame(data []byte) {
	f, err := protocol.DecodeFrame(bytes.NewReader(data))
	if err != nil {
		log.Printf("agent: bad frame: %v", err)
		return
	}
	if f.Type != protocol.FrameRequestBody {
		log.Printf("agent: unexpected frame type %d", f.Type)
		return
	}
	c.agent.routeBodyFrame(f)
}

// heartbeatLoop 周期性发送心跳。
func (c *Connector) heartbeatLoop(conn *websocket.Conn, stop chan struct{}) {
	interval := time.Duration(c.agent.cfg.HeartbeatSeconds) * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var seq atomic.Uint64
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			hb := protocol.Heartbeat{
				Type:         protocol.MsgHeartbeat,
				TS:           time.Now().Unix(),
				Seq:          seq.Add(1),
				ActiveReqs:   c.agent.activeCount(),
				LocalModelOK: c.agent.localHealthy(),
				LastProbeOK:  c.agent.lastProbeOK(),
			}
			if err := c.writeControl(hb); err != nil {
				return
			}
		}
	}
}

func (c *Connector) writeControl(msg any) error {
	if c.conn == nil {
		return fmt.Errorf("agent: not connected")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// WriteFrame 发送数据帧（供请求处理器使用）。
func (c *Connector) WriteFrame(f *protocol.Frame) error {
	if c.conn == nil {
		return fmt.Errorf("agent: not connected")
	}
	data, err := f.Encode()
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// Stop 停止连接器。
func (c *Connector) Stop() {
	close(c.stop)
	if c.conn != nil {
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"), time.Now().Add(2*time.Second))
		_ = c.conn.Close()
	}
}

func agentOriginHeader() http.Header {
	h := make(http.Header)
	h.Set("Origin", protocol.AgentOrigin)
	return h
}

func sortedRelays(rs []config.RelayAddr) []config.RelayAddr {
	out := make([]config.RelayAddr, len(rs))
	copy(out, rs)
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// tlsConfig 构建 mTLS 客户端 TLS 配置。
func (a *Agent) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if a.cfg.TLS.InsecureSkipVerify {
		return nil, fmt.Errorf("agent: insecure_skip_verify is forbidden for mTLS connections")
	}
	cert, err := tls.LoadX509KeyPair(a.cfg.TLS.Cert, a.cfg.TLS.Key)
	if err != nil {
		return nil, fmt.Errorf("agent: load client certificate: %w", err)
	}
	cfg.Certificates = []tls.Certificate{cert}
	if a.cfg.TLS.CA != "" {
		pemBytes, err := os.ReadFile(a.cfg.TLS.CA)
		if err != nil {
			return nil, fmt.Errorf("agent: read relay ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("agent: invalid relay ca PEM")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}
