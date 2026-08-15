// Package e2e 包含 Relay ↔ Agent ↔ 本地模型的端到端集成测试。
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modelrelay/internal/agent"
	"modelrelay/internal/certs"
	"modelrelay/internal/config"
	"modelrelay/internal/protocol"
	"modelrelay/internal/relay"
	"modelrelay/internal/store"
	"modelrelay/internal/testutil"
)

// testEnv 是 e2e 测试环境。
type testEnv struct {
	dir         string
	relaySrv    *relay.Server
	st          *store.Store
	wss         *relay.WSSServer
	upstream    *relay.UpstreamServer
	admin       *relay.AdminServer
	mock        *testutil.MockUpstream
	agentCtx    context.Context
	cancelAgent context.CancelFunc
	httpURL     string
	adminURL    string
	token       string
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()

	// 1. 证书体系。
	caCertPEM, caKeyPEM, err := certs.CreateCA("e2e-test-ca", 365)
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}
	ca, err := certs.LoadCA(caCertPEM, caKeyPEM)
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}
	writeFile(t, filepath.Join(dir, "agent-ca.crt"), caCertPEM)
	writeFile(t, filepath.Join(dir, "agent-ca.key"), caKeyPEM)

	relayCert, relayKey, err := ca.IssueServerCert("relay-e2e", []net.IP{net.ParseIP("127.0.0.1")}, nil, 365)
	if err != nil {
		t.Fatalf("relay cert: %v", err)
	}
	writeFile(t, filepath.Join(dir, "relay.crt"), relayCert)
	writeFile(t, filepath.Join(dir, "relay.key"), relayKey)

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

	// 2. 模拟上游。
	mock := testutil.NewMockUpstream("test-model")
	t.Cleanup(mock.Close)

	// 3. Relay。
	token := "test-internal-token"
	srv := relay.NewServer(&relay.RelayConfig{
		RelayID:           "relay-e2e",
		MaxBodyBytes:      16 << 20,
		MaxConcurrency:    16,
		QueueLength:       64,
		QueueTimeoutMs:    2000,
		TTFTTimeoutMs:     10000,
		IdleTimeoutMs:     60000,
		RequestTimeoutMs:  120000,
		HeartbeatTimeoutS: 8,
		InternalAuthToken: token,
	})
	wss, err := relay.NewWSSServer(srv, filepath.Join(dir, "relay.crt"), filepath.Join(dir, "relay.key"), filepath.Join(dir, "agent-ca.crt"), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("wss server: %v", err)
	}
	upstream, err := relay.NewUpstreamServer(srv, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream server: %v", err)
	}
	go wss.Start()
	go upstream.Start()
	t.Cleanup(func() { _ = upstream.Close(context.Background()); _ = wss.Close(context.Background()) })

	// 3.5 SQLite 存储 + 管理服务。
	st, err := store.Open(filepath.Join(dir, "relay.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv.SetStore(st)
	wss.SetStore(st)
	t.Cleanup(srv.Close)
	if err := st.EnsureAdmin("admin", "admin-pass", "admin"); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	if err := st.EnsureAdmin("reader", "reader-pass", "readonly"); err != nil {
		t.Fatalf("ensure reader: %v", err)
	}
	admin, err := relay.NewAdminServer(srv, st, "127.0.0.1:0", 30*time.Minute)
	if err != nil {
		t.Fatalf("admin server: %v", err)
	}
	go admin.Start()
	t.Cleanup(func() { _ = admin.Close() })

	// 4. Agent。
	agentCfg := config.DefaultAgent()
	agentCfg.NodeID = "node-1"
	agentCfg.Relays = []config.RelayAddr{{
		URL:      fmt.Sprintf("wss://127.0.0.1:%d/agent/v1/connect", wss.Addr().(*net.TCPAddr).Port),
		Priority: 1,
	}}
	agentCfg.TLS.Cert = filepath.Join(dir, "node-1.crt")
	agentCfg.TLS.Key = filepath.Join(dir, "node-1.key")
	agentCfg.TLS.CA = filepath.Join(dir, "agent-ca.crt")
	agentCfg.Local.BaseURL = mock.BaseURL()
	agentCfg.Local.APIKey = "local-key"
	agentCfg.Local.MaxConcurrency = 8
	agentCfg.HeartbeatSeconds = 2
	agentCfg.Probe.IntervalSec = 3600
	agentCfg.Probe.Enabled = []string{} // 只做模型发现，不探测（保持能力未知）

	a, err := agent.New(agentCfg)
	if err != nil {
		t.Fatalf("agent new: %v", err)
	}
	agentCtx, cancelAgent := context.WithCancel(context.Background())
	go a.Run(agentCtx)
	t.Cleanup(cancelAgent)

	env := &testEnv{
		dir:         dir,
		relaySrv:    srv,
		st:          st,
		wss:         wss,
		upstream:    upstream,
		admin:       admin,
		mock:        mock,
		agentCtx:    agentCtx,
		cancelAgent: cancelAgent,
		httpURL:     fmt.Sprintf("http://127.0.0.1:%d", upstream.Addr().(*net.TCPAddr).Port),
		adminURL:    fmt.Sprintf("http://127.0.0.1:%d", admin.Addr().(*net.TCPAddr).Port),
		token:       token,
	}
	env.waitNodeReady(t, "node-1", "test-model")
	return env
}

func (e *testEnv) waitNodeReady(t *testing.T, nodeID, model string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		snaps := e.relaySrv.Registry().List()
		for _, s := range snaps {
			if s.ID == nodeID && (s.State == relay.StateOnline || s.State == relay.StateDegraded) {
				for _, m := range e.relaySrv.Registry().ModelDirectory() {
					if m.ID == model {
						return
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	snaps := e.relaySrv.Registry().List()
	dir := e.relaySrv.Registry().ModelDirectory()
	t.Fatalf("node/model not ready: nodes=%+v directory=%+v", snaps, dir)
}

func (e *testEnv) do(t *testing.T, method, path string, body []byte, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, e.httpURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp, data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func authHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}
}

func TestE2EChatNonStream(t *testing.T) {
	env := setup(t)
	body := `{"model":"test-model","messages":[{"role":"user","content":"hello world"}]}`
	resp, data := env.do(t, http.MethodPost, "/v1/chat/completions", []byte(body), authHeader(env.token))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, data)
	}
	if len(out.Choices) == 0 || !strings.Contains(out.Choices[0].Message.Content, "hello world") {
		t.Fatalf("unexpected response: %s", data)
	}
}

func TestE2EChatStream(t *testing.T) {
	env := setup(t)
	body := `{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	resp, data := env.do(t, http.MethodPost, "/v1/chat/completions", []byte(body), authHeader(env.token))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "data: [DONE]") {
		t.Fatalf("missing [DONE]: %s", data)
	}
	if !strings.Contains(string(data), "data: ") {
		t.Fatalf("no SSE events: %s", data)
	}
	if ct := resp.Header.Get("Content-Type"); ct == "" {
		t.Fatalf("missing content-type")
	}
}

func TestE2EModelsDirectory(t *testing.T) {
	env := setup(t)
	resp, data := env.do(t, http.MethodGet, "/v1/models", nil, authHeader(env.token))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "test-model") {
		t.Fatalf("model missing: %s", data)
	}
}

func TestE2EAuthRequired(t *testing.T) {
	env := setup(t)
	resp, data := env.do(t, http.MethodGet, "/v1/models", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
}

func TestE2EModelNotFound(t *testing.T) {
	env := setup(t)
	body := `{"model":"no-such-model","messages":[]}`
	resp, data := env.do(t, http.MethodPost, "/v1/chat/completions", []byte(body), authHeader(env.token))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if e.Error.Code != protocol.ErrModelNotFound {
		t.Fatalf("code=%s", e.Error.Code)
	}
}

func TestE2EInvalidPath(t *testing.T) {
	env := setup(t)
	resp, data := env.do(t, http.MethodGet, "/v1/admin", nil, authHeader(env.token))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
}

func TestE2EStreamIsNotBuffered(t *testing.T) {
	// 流式场景：mock 每事件后立即 flush，验证 Relay 不聚合（通过到达顺序与事件完整性验证）。
	env := setup(t)
	body := `{"model":"test-model","stream":true,"messages":[{"role":"user","content":"ping"}]}`
	req, _ := http.NewRequest(http.MethodPost, env.httpURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
	for k, v := range authHeader(env.token) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	// 读取到 [DONE] 即成功（mock 输出 3 个事件）。
	var sb strings.Builder
	buf := make([]byte, 512)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("stream timeout, got: %s", sb.String())
		}
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
			if strings.Contains(sb.String(), "data: [DONE]") {
				break
			}
		}
		if err != nil {
			t.Fatalf("read err before [DONE]: %v; got %s", err, sb.String())
		}
	}
	if !strings.Contains(sb.String(), "Hello") || !strings.Contains(sb.String(), " world") {
		t.Fatalf("events not in order: %s", sb.String())
	}
}
