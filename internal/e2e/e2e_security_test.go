package e2e

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"modelrelay/internal/certs"
	"modelrelay/internal/protocol"
	"modelrelay/internal/relay"
)

// wssURL 返回 WSS 接入地址。
func (e *testEnv) wssURL() string {
	return fmt.Sprintf("wss://127.0.0.1:%d%s", e.wss.Addr().(*net.TCPAddr).Port, relay.WSSPath)
}

func loadRootPool(t *testing.T, caFile string) *x509.CertPool {
	t.Helper()
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read ca: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatalf("bad ca pem")
	}
	return pool
}

func TestE2EMTLSRejections(t *testing.T) {
	env := setup(t)
	url := env.wssURL()
	relayCA := loadRootPool(t, filepath.Join(env.dir, "agent-ca.crt"))

	t.Run("no_client_cert_rejected", func(t *testing.T) {
		dialer := websocket.Dialer{
			TLSClientConfig: &tls.Config{RootCAs: relayCA, MinVersion: tls.VersionTLS12},
			HandshakeTimeout: 5 * time.Second,
		}
		conn, _, err := dialer.Dial(url, nil)
		if err == nil {
			conn.Close()
			t.Fatal("TLS handshake must fail without client certificate")
		}
	})

	t.Run("wrong_ca_rejected", func(t *testing.T) {
		// 用独立 CA 生成客户端证书（模拟未被 Agent CA 信任的节点）。
		dir := t.TempDir()
		caCert, caKey, err := certs.CreateCA("rogue-ca", 30)
		if err != nil {
			t.Fatalf("rogue ca: %v", err)
		}
		rogueCA, _ := certs.LoadCA(caCert, caKey)
		keyPEM, csrPEM, _ := certs.GenerateCSR("node-1")
		certPEM, _ := rogueCA.IssueFromCSR(csrPEM, "node-1", 30)
		certFile := filepath.Join(dir, "rogue.crt")
		keyFile := filepath.Join(dir, "rogue.key")
		os.WriteFile(certFile, certPEM, 0o600)
		os.WriteFile(keyFile, keyPEM, 0o600)
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			t.Fatalf("load rogue cert: %v", err)
		}
		dialer := websocket.Dialer{
			TLSClientConfig: &tls.Config{RootCAs: relayCA, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			HandshakeTimeout: 5 * time.Second,
		}
		conn, _, err := dialer.Dial(url, nil)
		if err == nil {
			conn.Close()
			t.Fatal("cert from untrusted CA must be rejected at TLS layer")
		}
	})

	t.Run("identity_mismatch_rejected", func(t *testing.T) {
		cert, err := tls.LoadX509KeyPair(
			filepath.Join(env.dir, "node-1.crt"),
			filepath.Join(env.dir, "node-1.key"),
		)
		if err != nil {
			t.Fatalf("load node cert: %v", err)
		}
		dialer := websocket.Dialer{
			TLSClientConfig: &tls.Config{RootCAs: relayCA, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			HandshakeTimeout: 5 * time.Second,
		}
		conn, _, err := dialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		// 证书 CN=node-1，但 hello 声明 node_id=node-x → 必须拒绝。
		_ = conn.WriteJSON(protocol.Hello{
			Type:         protocol.MsgHello,
			Protocol:     protocol.ProtocolVersion,
			NodeID:       "node-x",
			AgentVersion: "test",
		})
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var ack protocol.HelloAck
		if err := json.Unmarshal(data, &ack); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ack.Accepted {
			t.Fatal("identity mismatch must be rejected")
		}
		if ack.Reason != protocol.ErrIdentityMismatch {
			t.Fatalf("reason=%s", ack.Reason)
		}
	})

	t.Run("valid_hello_and_heartbeat", func(t *testing.T) {
		// 使用测试 CA 签发一个新节点证书。
		ca, err := certs.LoadCAFiles(filepath.Join(env.dir, "agent-ca.crt"), filepath.Join(env.dir, "agent-ca.key"))
		if err != nil {
			t.Fatalf("load ca: %v", err)
		}
		keyPEM, csrPEM, _ := certs.GenerateCSR("node-2")
		certPEM, _ := ca.IssueFromCSR(csrPEM, "node-2", 30)
		dir := t.TempDir()
		certFile := filepath.Join(dir, "n2.crt")
		keyFile := filepath.Join(dir, "n2.key")
		os.WriteFile(certFile, certPEM, 0o600)
		os.WriteFile(keyFile, keyPEM, 0o600)
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		dialer := websocket.Dialer{
			TLSClientConfig: &tls.Config{RootCAs: relayCA, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			HandshakeTimeout: 5 * time.Second,
		}
		conn, _, err := dialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		_ = conn.WriteJSON(protocol.Hello{
			Type:         protocol.MsgHello,
			Protocol:     protocol.ProtocolVersion,
			NodeID:       "node-2",
			AgentVersion: "test",
			Platform:     protocol.Platform{OS: "test", Arch: "test"},
		})
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read ack: %v", err)
		}
		var ack protocol.HelloAck
		if err := json.Unmarshal(data, &ack); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !ack.Accepted {
			t.Fatalf("rejected: %s", ack.Reason)
		}
		// 心跳往返。
		_ = conn.WriteJSON(protocol.Heartbeat{Type: protocol.MsgHeartbeat, TS: time.Now().Unix(), Seq: 1})
		_, data, err = conn.ReadMessage()
		if err != nil {
			t.Fatalf("read hb ack: %v", err)
		}
		var hbAck protocol.HeartbeatAck
		if err := json.Unmarshal(data, &hbAck); err != nil {
			t.Fatalf("unmarshal hb: %v", err)
		}
		if !hbAck.OK {
			t.Fatal("heartbeat not acked")
		}
		// 节点出现在注册表中。
		found := false
		for _, s := range env.relaySrv.Registry().List() {
			if s.ID == "node-2" {
				found = true
			}
		}
		if !found {
			t.Fatal("node-2 not in registry")
		}
	})

	t.Run("duplicate_node_rejected", func(t *testing.T) {
		// 用 node-1 证书再连一次（node-1 已被真实 Agent 占用）→ 必须拒绝。
		cert, err := tls.LoadX509KeyPair(
			filepath.Join(env.dir, "node-1.crt"),
			filepath.Join(env.dir, "node-1.key"),
		)
		if err != nil {
			t.Fatalf("load node cert: %v", err)
		}
		dialer := websocket.Dialer{
			TLSClientConfig: &tls.Config{RootCAs: relayCA, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			HandshakeTimeout: 5 * time.Second,
		}
		conn, _, err := dialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		_ = conn.WriteJSON(protocol.Hello{
			Type:         protocol.MsgHello,
			Protocol:     protocol.ProtocolVersion,
			NodeID:       "node-1",
			AgentVersion: "test",
			Platform:     protocol.Platform{OS: "test", Arch: "test"},
		})
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var ack protocol.HelloAck
		if err := json.Unmarshal(data, &ack); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ack.Accepted {
			t.Fatal("duplicate node_id must be rejected")
		}
		if ack.Reason != protocol.ErrDuplicateNode {
			t.Fatalf("reason=%s", ack.Reason)
		}
	})
}

// TestAdminCSRFRejected 验证跨站写请求被拒绝（Origin 校验）。
func TestAdminCSRFRejected(t *testing.T) {
	env := setup(t)
	admin := loginAdmin(t, env, "admin", "admin-pass")

	// 带恶意 Origin 的写请求 → 403。
	req, _ := http.NewRequest(http.MethodPost, env.adminURL+"/api/nodes/node-1/kick", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := admin.client.Do(req)
	if err != nil {
		t.Fatalf("csrf req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf status=%d want 403", resp.StatusCode)
	}

	// 同源写请求 → 放行。
	req2, _ := http.NewRequest(http.MethodPost, env.adminURL+"/api/nodes/node-1/kick", nil)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Origin", "http://"+req2.Host)
	resp2, err := admin.client.Do(req2)
	if err != nil {
		t.Fatalf("same-origin req: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("same-origin status=%d", resp2.StatusCode)
	}
}
