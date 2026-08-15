package e2e

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"modelrelay/internal/relay"
)

// TestE2ECancelPropagation 验证：客户端断开 → Relay 发 cancel → Agent 取消本地请求。
func TestE2ECancelPropagation(t *testing.T) {
	env := setup(t)
	env.mock.SlowStream.Store(true)

	body := `{"model":"test-model","stream":true,"messages":[{"role":"user","content":"ping"}]}`
	req, err := http.NewRequest(http.MethodPost, env.httpURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	for k, v := range authHeader(env.token) {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	// 读取首个事件，确认链路已建立。
	buf := make([]byte, 1024)
	var sb strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting first event, got: %s", sb.String())
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
			if strings.Contains(sb.String(), "first") {
				break
			}
		}
		if rerr != nil {
			t.Fatalf("read err: %v; got %s", rerr, sb.String())
		}
	}

	// 客户端断开。
	cancel()
	_ = resp.Body.Close()

	// 本地 mock 应观察到请求被取消。
	waitDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(waitDeadline) {
		if env.mock.SlowCanceled.Load() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("local model request was not canceled after client disconnect")
}

// TestE2ECapabilityRouting 验证：节点上报能力后，不支持接口的请求不会路由到该节点。
func TestE2ECapabilityRouting(t *testing.T) {
	env := setup(t)
	// 模拟节点能力上报：仅支持 chat，不支持 embeddings。
	n := env.relaySrv.Registry().Get("node-1")
	if n == nil {
		t.Fatal("node-1 missing")
	}
	n.SetModels(map[string]*relay.ModelCap{
		"test-model": {
			ID:                   "test-model",
			Capabilities:         []string{"chat_completions", "chat_stream"},
			CapabilitiesComplete: true,
			ProbeTime:            time.Now(),
		},
	})

	// embeddings 请求：节点声明不支持 → 422。
	body := `{"model":"test-model","input":"hello"}`
	resp, data := env.do(t, http.MethodPost, "/v1/embeddings", []byte(body), authHeader(env.token))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("embeddings status=%d body=%s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "capability_not_supported") {
		t.Fatalf("missing capability error: %s", data)
	}

	// chat 请求：能力支持 → 200。
	chatBody := `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`
	resp2, data2 := env.do(t, http.MethodPost, "/v1/chat/completions", []byte(chatBody), authHeader(env.token))
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", resp2.StatusCode, data2)
	}
}
