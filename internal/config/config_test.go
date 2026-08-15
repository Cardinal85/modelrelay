package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRelayValid(t *testing.T) {
	r := DefaultRelay()
	// 默认配置缺证书路径，Validate 应报错（证书必填）。
	if err := r.Validate(); err == nil {
		t.Fatal("default relay config without certs should fail validation")
	}
}

func TestRelayValidate(t *testing.T) {
	r := DefaultRelay()
	r.TLSCert = "relay.crt"
	r.TLSKey = "relay.key"
	r.AgentCA = "agent-ca.crt"
	if err := r.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	// 非法监听地址。
	r.HTTPListen = "not-an-addr"
	if err := r.Validate(); err == nil {
		t.Fatal("bad http_listen must fail")
	}
	r.HTTPListen = "127.0.0.1:9100"

	// 空 relay_id。
	r.RelayID = ""
	if err := r.Validate(); err == nil {
		t.Fatal("empty relay_id must fail")
	}
}

func TestDefaultAgentValidate(t *testing.T) {
	a := DefaultAgent()
	// 缺 node_id / relays / 证书。
	if err := a.Validate(); err == nil {
		t.Fatal("default agent config should fail validation")
	}
}

func TestAgentValidate(t *testing.T) {
	a := DefaultAgent()
	a.NodeID = "gpu-1"
	a.Relays = []RelayAddr{{URL: "wss://relay.example.com:9443/agent/v1/connect", Priority: 1}}
	a.TLS.Cert = "node.crt"
	a.TLS.Key = "node.key"
	a.TLS.CA = "relay-ca.crt"
	a.Local.BaseURL = "http://127.0.0.1:8000/v1"
	if err := a.Validate(); err != nil {
		t.Fatalf("valid agent config rejected: %v", err)
	}

	// 非 ws/wss 协议。
	a.Relays = []RelayAddr{{URL: "https://relay.example.com/x", Priority: 1}}
	if err := a.Validate(); err == nil {
		t.Fatal("non-ws scheme must fail")
	}
	a.Relays = []RelayAddr{{URL: "wss://relay.example.com:9443/agent/v1/connect", Priority: 1}}

	// 本地地址必须 http(s)。
	a.Local.BaseURL = "ftp://x"
	if err := a.Validate(); err == nil {
		t.Fatal("non-http local base_url must fail")
	}
	a.Local.BaseURL = "http://127.0.0.1:8000/v1"

	// TLS CA 缺失且不跳过校验。
	a.TLS.CA = ""
	if err := a.Validate(); err == nil {
		t.Fatal("missing ca without insecure_skip_verify must fail")
	}
	a.TLS.CA = "relay-ca.crt"

	// 默认值填充。
	if a.Local.MaxConcurrency != 8 || !a.Local.TLSVerify || a.HeartbeatSeconds != 20 {
		t.Fatalf("defaults not applied: %+v", a)
	}
}

func TestLoadFileAndEnvExpansion(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("TEST_RELAY_TOKEN", "secret-env")
	defer os.Unsetenv("TEST_RELAY_TOKEN")

	path := filepath.Join(dir, "relay.yaml")
	content := `
relay_id: r1
http_listen: "127.0.0.1:9100"
wss_listen: "0.0.0.0:9443"
tls_cert: "c.crt"
tls_key: "k.key"
agent_ca: "ca.crt"
internal_auth:
  enabled: true
  token: "${TEST_RELAY_TOKEN}"
limits:
  max_concurrency: 4
  queue_length: 10
admin:
  listen: "127.0.0.1:9200"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	r := DefaultRelay()
	if err := LoadFile(path, r); err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.InternalAuth.Token != "secret-env" {
		t.Fatalf("env expansion failed: %q", r.InternalAuth.Token)
	}
	if r.Limits.MaxConcurrency != 4 || r.Limits.QueueLength != 10 {
		t.Fatalf("yaml fields not applied: %+v", r.Limits)
	}
}

func TestLoadFileBadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("relay_id: [unclosed"), 0o600)
	r := DefaultRelay()
	if err := LoadFile(path, r); err == nil {
		t.Fatal("malformed yaml must fail")
	}
}

func TestPlatformDir(t *testing.T) {
	// 各平台配置目录约定（windows 分支用 PROGRAMDATA）。
	if d := DataDir(); d == "" {
		t.Fatal("DataDir empty")
	}
	if !strings.Contains(DataDir(), "ModelAgent") {
		t.Fatalf("unexpected DataDir: %s", DataDir())
	}
}
