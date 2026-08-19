package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
)

// Agent 是 Agent 服务的完整配置。
type Agent struct {
	NodeID string `yaml:"node_id"`
	// MaxBodyBytes 限制 Agent 侧单请求组装缓冲，防止 Relay/Agent 配置不一致时内存失控。
	MaxBodyBytes int64 `yaml:"max_body_bytes"`

	// Relays 是按优先级排列的 Relay 地址列表（priority 越小越优先）。
	Relays []RelayAddr `yaml:"relays"`

	// TLS 客户端证书（mTLS）。
	TLS struct {
		Cert               string `yaml:"cert"`
		Key                string `yaml:"key"`
		CA                 string `yaml:"ca"` // Relay 服务端 CA
		InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	} `yaml:"tls"`

	// Local 本地模型服务。
	Local Local `yaml:"local"`

	// Probe 能力探测。
	Probe Probe `yaml:"probe"`

	HeartbeatSeconds int `yaml:"heartbeat_interval"`
	// PreferPrimaryIntervalSec 是主备模式下检查主 Relay 是否恢复的间隔（秒）。
	// 0 表示不主动回切主 Relay。
	PreferPrimaryIntervalSec int    `yaml:"prefer_primary_interval"`
	LogLevel                 string `yaml:"log_level"`
}

// RelayAddr 是一个 Relay 连接地址。
type RelayAddr struct {
	URL      string `yaml:"url"`
	Priority int    `yaml:"priority"`
}

// Local 是本地模型服务配置。
type Local struct {
	BaseURL            string `yaml:"base_url"`
	APIKey             string `yaml:"api_key"`
	TLSVerify          bool   `yaml:"tls_verify"`
	ConnectTimeoutSec  int    `yaml:"connect_timeout_sec"`
	ResponseTimeoutSec int    `yaml:"response_timeout_sec"`
	MaxConcurrency     int    `yaml:"max_concurrency"`
}

// Probe 是能力探测配置。
type Probe struct {
	IntervalSec int `yaml:"interval_sec"`
	// TestModel 已废弃；探测始终按 /v1/models 返回的每个模型分别执行。
	TestModel string `yaml:"test_model"`
	// 开关：chat/chat_stream/completions/embeddings/responses/tools/multimodal。
	Enabled []string `yaml:"enabled"`
}

// DefaultAgent 返回带默认值的 Agent 配置。
func DefaultAgent() *Agent {
	a := &Agent{}
	a.TLS.InsecureSkipVerify = false
	a.Local = Local{
		ConnectTimeoutSec:  5,
		ResponseTimeoutSec: 300,
		MaxConcurrency:     8,
		TLSVerify:          true,
	}
	a.MaxBodyBytes = 200 << 20
	a.Probe = Probe{
		IntervalSec: 600,
		TestModel:   "",
		Enabled:     []string{"chat", "chat_stream", "embeddings", "responses", "tools"},
	}
	a.HeartbeatSeconds = 20
	a.LogLevel = "info"
	return a
}

// Validate 校验 Agent 配置。
func (a *Agent) Validate() error {
	if a.NodeID == "" {
		return fmt.Errorf("node_id is required")
	}
	if len(a.Relays) == 0 {
		return fmt.Errorf("at least one relay address is required")
	}
	for _, r := range a.Relays {
		u, err := url.Parse(r.URL)
		if err != nil {
			return fmt.Errorf("relay url %q: %w", r.URL, err)
		}
		if u.Scheme != "wss" {
			return fmt.Errorf("relay url %q must use wss:// with mTLS", r.URL)
		}
		if _, _, err := net.SplitHostPort(u.Host); err != nil && u.Port() == "" {
			return fmt.Errorf("relay url %q must include a port", r.URL)
		}
	}
	if a.TLS.Cert == "" || a.TLS.Key == "" {
		return fmt.Errorf("tls.cert and tls.key (client certificate) are required for mTLS")
	}
	if a.TLS.InsecureSkipVerify {
		return fmt.Errorf("tls.insecure_skip_verify must remain false for mTLS")
	}
	if a.TLS.CA == "" {
		return fmt.Errorf("tls.ca is required for mTLS server verification")
	}
	if a.Local.BaseURL == "" {
		return fmt.Errorf("local.base_url is required")
	}
	if u, err := url.Parse(a.Local.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("local.base_url must be http(s)://, got %q", a.Local.BaseURL)
	}
	if a.Local.MaxConcurrency <= 0 {
		a.Local.MaxConcurrency = 8
	}
	if a.MaxBodyBytes <= 0 {
		a.MaxBodyBytes = 200 << 20
	}
	if a.HeartbeatSeconds <= 0 {
		a.HeartbeatSeconds = 20
	}
	if a.PreferPrimaryIntervalSec < 0 {
		a.PreferPrimaryIntervalSec = 0
	}
	if a.LogLevel == "" {
		a.LogLevel = "info"
	}
	return nil
}

// PlatformOS 返回当前操作系统标识。
func PlatformOS() string { return runtime.GOOS }

// PlatformArch 返回当前架构标识。
func PlatformArch() string { return runtime.GOARCH }

// DataDir 返回平台相关的配置目录。
func DataDir() string {
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("PROGRAMDATA"); d != "" {
			return d + `\ModelAgent`
		}
		return `C:\ProgramData\ModelAgent`
	case "darwin":
		return "/Library/Application Support/ModelAgent"
	default:
		return "/etc/model-agent"
	}
}
