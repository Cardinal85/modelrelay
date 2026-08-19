package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"modelrelay/internal/agent"
	"modelrelay/internal/certs"
	"modelrelay/internal/config"
	"modelrelay/internal/relay"
	"modelrelay/internal/testutil"
)

// relayStack 是测试用 Relay 栈（server + wss + upstream）。
type relayStack struct {
	srv     *relay.Server
	wss     *relay.WSSServer
	up      *relay.UpstreamServer
	wssPort int
	httpURL string
	token   string
}

// newRelayStack 创建 Relay 栈。wssPort 为 0 时自动分配。
func newRelayStack(t *testing.T, dir, name string, wssPort int) *relayStack {
	t.Helper()
	srv := relay.NewServer(&relay.RelayConfig{
		RelayID:           name,
		HTTPListen:        "127.0.0.1:0",
		WSSListen:         fmt.Sprintf("127.0.0.1:%d", wssPort),
		MaxBodyBytes:      16 << 20,
		MaxConcurrency:    16,
		QueueLength:       64,
		QueueTimeoutMs:    2000,
		TTFTTimeoutMs:     10000,
		IdleTimeoutMs:     60000,
		RequestTimeoutMs:  120000,
		HeartbeatTimeoutS: 8,
		InternalAuthToken: "test-token",
		InternalAuthEnabled: true,
	})
	wss, err := relay.NewWSSServer(srv,
		filepath.Join(dir, "relay.crt"), filepath.Join(dir, "relay.key"),
		filepath.Join(dir, "agent-ca.crt"), fmt.Sprintf("127.0.0.1:%d", wssPort))
	if err != nil {
		t.Fatalf("wss %s: %v", name, err)
	}
	up, err := relay.NewUpstreamServer(srv, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream %s: %v", name, err)
	}
	go wss.Start()
	go up.Start()
	t.Cleanup(func() { _ = up.Close(context.Background()); _ = wss.ForceClose() })
	return &relayStack{
		srv:     srv,
		wss:     wss,
		up:      up,
		wssPort: wss.Addr().(*net.TCPAddr).Port,
		httpURL: fmt.Sprintf("http://127.0.0.1:%d", up.Addr().(*net.TCPAddr).Port),
		token:   "test-token",
	}
}

// newCertBundle 生成共享证书（CA、Relay 服务端证书、节点证书）。
func newCertBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	caCert, caKey, err := certs.CreateCA("reconnect-ca", 365)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	ca, err := certs.LoadCA(caCert, caKey)
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}
	relayCert, relayKey, err := ca.IssueServerCert("relay", []net.IP{net.ParseIP("127.0.0.1")}, nil, 365)
	if err != nil {
		t.Fatalf("relay cert: %v", err)
	}
	writeFile(t, filepath.Join(dir, "relay.crt"), relayCert)
	writeFile(t, filepath.Join(dir, "relay.key"), relayKey)
	writeFile(t, filepath.Join(dir, "agent-ca.crt"), caCert)
	writeFile(t, filepath.Join(dir, "agent-ca.key"), caKey)

	nodeKey, nodeCSR, err := certs.GenerateCSR("node-1")
	if err != nil {
		t.Fatalf("node csr: %v", err)
	}
	nodeCert, err := ca.IssueFromCSR(nodeCSR, "node-1", 365)
	if err != nil {
		t.Fatalf("node cert: %v", err)
	}
	writeFile(t, filepath.Join(dir, "node-1.key"), nodeKey)
	writeFile(t, filepath.Join(dir, "node-1.crt"), nodeCert)
	return dir
}

func nodeRegistered(st *relayStack, nodeID string) bool {
	for _, s := range st.srv.Registry().List() {
		if s.ID == nodeID && (s.State == relay.StateOnline || s.State == relay.StateDegraded) {
			return true
		}
	}
	return false
}

// TestAgentReconnectAfterRelayRestart 验证：Relay 重启后 Agent 自动重连并重新注册。
func TestAgentReconnectAfterRelayRestart(t *testing.T) {
	env := setup(t)
	port := env.wss.Addr().(*net.TCPAddr).Port

	// 1. 强制关闭 WSS（模拟 Relay 故障）。
	if err := env.wss.ForceClose(); err != nil {
		t.Fatalf("force close: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		return !nodeRegistered(&relayStack{srv: env.relaySrv}, "node-1")
	})

	// 2. 在同一端口重启 WSS。
	wss2, err := relay.NewWSSServer(env.relaySrv,
		filepath.Join(env.dir, "relay.crt"), filepath.Join(env.dir, "relay.key"),
		filepath.Join(env.dir, "agent-ca.crt"), fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("restart wss: %v", err)
	}
	go wss2.Start()
	defer wss2.ForceClose()

	// 3. 等待 Agent 重连并重新注册模型。
	waitFor(t, 20*time.Second, func() bool {
		if !nodeRegistered(&relayStack{srv: env.relaySrv}, "node-1") {
			return false
		}
		for _, m := range env.relaySrv.Registry().ModelDirectory() {
			if m.ID == "test-model" {
				return true
			}
		}
		return false
	})

	// 4. 重连后请求可用。
	body := `{"model":"test-model","messages":[{"role":"user","content":"after reconnect"}]}`
	resp, data := env.do(t, http.MethodPost, "/v1/chat/completions", []byte(body), authHeader(env.token))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
}

// TestStandbyRelaySwitch 验证主备组网：主故障切备、主恢复回切。
func TestStandbyRelaySwitch(t *testing.T) {
	dir := newCertBundle(t)
	mock := testutil.NewMockUpstream("test-model")
	t.Cleanup(mock.Close)

	stackA := newRelayStack(t, dir, "relay-a", 0)
	stackB := newRelayStack(t, dir, "relay-b", 0)
	portA := stackA.wssPort

	// Agent：A 优先，B 备用，1 秒探测回切。
	agentCfg := config.DefaultAgent()
	agentCfg.NodeID = "node-1"
	agentCfg.Relays = []config.RelayAddr{
		{URL: fmt.Sprintf("wss://127.0.0.1:%d/agent/v1/connect", portA), Priority: 1},
		{URL: fmt.Sprintf("wss://127.0.0.1:%d/agent/v1/connect", stackB.wssPort), Priority: 2},
	}
	agentCfg.TLS.Cert = filepath.Join(dir, "node-1.crt")
	agentCfg.TLS.Key = filepath.Join(dir, "node-1.key")
	agentCfg.TLS.CA = filepath.Join(dir, "agent-ca.crt")
	agentCfg.Local.BaseURL = mock.BaseURL()
	agentCfg.Local.MaxConcurrency = 8
	agentCfg.HeartbeatSeconds = 2
	agentCfg.PreferPrimaryIntervalSec = 1
	agentCfg.Probe.IntervalSec = 3600
	agentCfg.Probe.Enabled = []string{}

	a, err := agent.New(agentCfg)
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	// 1. 初始连接主 Relay A。
	waitFor(t, 15*time.Second, func() bool {
		return nodeRegistered(stackA, "node-1") && !nodeRegistered(stackB, "node-1")
	})

	// 2. 主 A 故障 → 切换到备 B。
	if err := stackA.wss.ForceClose(); err != nil {
		t.Fatalf("force close A: %v", err)
	}
	waitFor(t, 25*time.Second, func() bool {
		return nodeRegistered(stackB, "node-1")
	})

	// 通过备用 B 的 HTTP 上游发起请求，验证链路可用。
	body := `{"model":"test-model","messages":[{"role":"user","content":"failover"}]}`
	req, _ := http.NewRequest(http.MethodPost, stackB.httpURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+stackB.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat via B: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat via B status=%d body=%s", resp.StatusCode, data)
	}

	// 3. 主 A 恢复 → Agent 回切到 A（B 上注销）。
	wssA2, err := relay.NewWSSServer(stackA.srv,
		filepath.Join(dir, "relay.crt"), filepath.Join(dir, "relay.key"),
		filepath.Join(dir, "agent-ca.crt"), fmt.Sprintf("127.0.0.1:%d", portA))
	if err != nil {
		t.Fatalf("restart A: %v", err)
	}
	go wssA2.Start()
	defer wssA2.ForceClose()

	waitFor(t, 30*time.Second, func() bool {
		return nodeRegistered(stackA, "node-1") && !nodeRegistered(stackB, "node-1")
	})
}
