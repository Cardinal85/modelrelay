package config

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Relay 是 Relay 服务的完整配置。
type Relay struct {
	RelayID   string `yaml:"relay_id"`
	RelayName string `yaml:"relay_name"`

	HTTPListen string `yaml:"http_listen"` // New API 上游地址，默认 127.0.0.1:9100
	WSSListen  string `yaml:"wss_listen"`  // Agent 接入地址，默认 0.0.0.0:9443

	// WSS 服务端 TLS（Relay 自己的证书）。
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
	// AgentCA 用于验证 Agent 客户端证书的 CA 证书（PEM）。
	AgentCA string `yaml:"agent_ca"`

	// InternalAuth 是 New API 调用 Relay 时使用的内部认证。
	InternalAuth struct {
		Token   string `yaml:"token"`
		Enabled bool   `yaml:"enabled"`
	} `yaml:"internal_auth"`

	// Limits 与超时。
	Limits Limits `yaml:"limits"`

	// Admin 管理服务。
	Admin Admin `yaml:"admin"`

	// Store 持久化。
	Store struct {
		DBPath string `yaml:"db_path"`
	} `yaml:"store"`

	// Retention 数据保留策略。
	Retention struct {
		KeepPromptResponse bool `yaml:"keep_prompt_response"`
		RetentionDays      int  `yaml:"retention_days"`
	} `yaml:"retention"`

	LogLevel string `yaml:"log_level"`
}

// Limits 是并发与超时限制。
type Limits struct {
	MaxBodyBytes        int64 `yaml:"max_body_bytes"`
	MaxConcurrency      int   `yaml:"max_concurrency"`
	QueueLength         int   `yaml:"queue_length"`
	QueueTimeoutSec     int   `yaml:"queue_timeout_sec"`
	TTFTTimeoutSec      int   `yaml:"ttft_timeout_sec"`
	IdleTimeoutSec      int   `yaml:"idle_timeout_sec"`
	RequestTimeoutSec   int   `yaml:"request_timeout_sec"`
	HeartbeatTimeoutSec int   `yaml:"heartbeat_timeout_sec"`
}

// Admin 是管理 API 与 WebUI 配置。
type Admin struct {
	Listen         string        `yaml:"listen"`
	Users          []AdminUser   `yaml:"users"`
	SessionTimeout time.Duration `yaml:"session_timeout"`
	SessionTTLMin  int           `yaml:"session_ttl_min"`
	TrustedProxies []string      `yaml:"trusted_proxies"`
	SecureCookie   bool          `yaml:"secure_cookie"`
	Turnstile      Turnstile     `yaml:"turnstile"`
}

// Turnstile 是 Cloudflare Turnstile 人机验证（可选）。
type Turnstile struct {
	SiteKey   string `yaml:"site_key"`
	SecretKey string `yaml:"secret_key"`
}

// AdminUser 是管理员账号。
type AdminUser struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"` // 仅用于初次初始化，建议使用初始化命令生成哈希
	Role     string `yaml:"role"`     // admin | readonly
}

// DefaultRelay 返回带默认值的 Relay 配置。
func DefaultRelay() *Relay {
	r := &Relay{}
	r.RelayID = "relay-default"
	r.HTTPListen = "127.0.0.1:9100"
	r.WSSListen = "0.0.0.0:9443"
	r.Limits = Limits{
		MaxBodyBytes:        200 << 20,
		MaxConcurrency:      64,
		QueueLength:         256,
		QueueTimeoutSec:     30,
		TTFTTimeoutSec:      120,
		IdleTimeoutSec:      300,
		RequestTimeoutSec:   1800,
		HeartbeatTimeoutSec: 60,
	}
	r.InternalAuth.Enabled = true
	r.Admin.Listen = "127.0.0.1:9200"
	r.Admin.SessionTimeout = 30 * time.Minute
	r.Admin.SessionTTLMin = 30
	r.Admin.TrustedProxies = []string{"127.0.0.1", "::1"}
	r.Store.DBPath = "modelrelay.db"
	r.Retention.RetentionDays = 30
	r.LogLevel = "info"
	return r
}

// Validate 校验 Relay 配置。
func (r *Relay) Validate() error {
	if r.RelayID == "" {
		return fmt.Errorf("relay_id is required")
	}
	if _, _, err := net.SplitHostPort(r.HTTPListen); err != nil {
		return fmt.Errorf("http_listen: %w", err)
	}
	if _, _, err := net.SplitHostPort(r.WSSListen); err != nil {
		return fmt.Errorf("wss_listen: %w", err)
	}
	if r.TLSCert == "" || r.TLSKey == "" {
		return fmt.Errorf("tls_cert and tls_key are required for WSS")
	}
	if r.AgentCA == "" {
		return fmt.Errorf("agent_ca is required to verify agent certificates")
	}
	if r.InternalAuth.Enabled && strings.TrimSpace(r.InternalAuth.Token) == "" {
		return fmt.Errorf("internal_auth.enabled requires a non-empty token")
	}
	site := strings.TrimSpace(r.Admin.Turnstile.SiteKey)
	secret := strings.TrimSpace(r.Admin.Turnstile.SecretKey)
	if (site == "") != (secret == "") {
		return fmt.Errorf("admin.turnstile.site_key and secret_key must be set together")
	}
	if r.Limits.MaxConcurrency <= 0 || r.Limits.QueueLength < 0 {
		return fmt.Errorf("limits invalid: max_concurrency>0, queue_length>=0")
	}
	if r.Admin.SessionTTLMin <= 0 {
		r.Admin.SessionTTLMin = 30
	}
	if r.Retention.RetentionDays <= 0 {
		r.Retention.RetentionDays = 30
	}
	if r.LogLevel == "" {
		r.LogLevel = "info"
	}
	return nil
}
