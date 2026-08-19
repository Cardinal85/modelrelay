package certmgr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// RemoteCert 是 Relay 管理 API 返回的证书元数据。
type RemoteCert struct {
	NodeID       string    `json:"node_id"`
	Serial       string    `json:"serial"`
	Subject      string    `json:"subject"`
	Issuer       string    `json:"issuer"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	Status       string    `json:"status"`
	Fingerprint  string    `json:"fingerprint"`
	RevokeReason string    `json:"revoke_reason,omitempty"`
}

// AdminClient 使用会话 Cookie 调用 Relay 管理 API。不持久化密码。
type AdminClient struct {
	baseURL string
	http    *http.Client
	user    string
	role    string
}

// NewAdminClient 创建管理 API 客户端。密码只用于 Login 调用，不会写入磁盘。
func NewAdminClient(baseURL string) (*AdminClient, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("certmgr: Relay admin URL is required")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("certmgr: invalid admin URL: %w", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &AdminClient{
		baseURL: base,
		http: &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		},
	}, nil
}

// Username 返回当前登录用户，未登录时为空。
func (c *AdminClient) Username() string { return c.user }

// LoggedIn 返回是否已有会话。
func (c *AdminClient) LoggedIn() bool { return c != nil && c.user != "" }

// Login 使用管理员账号登录。密码仅用于本次请求。
func (c *AdminClient) Login(username, password string) error {
	if c == nil {
		return fmt.Errorf("certmgr: admin client is nil")
	}
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return fmt.Errorf("certmgr: username and password are required")
	}
	body, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		return err
	}
	resp, err := c.do(http.MethodPost, "/api/login", body)
	if err != nil {
		return err
	}
	var out struct {
		Error string `json:"error"`
		User  struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return fmt.Errorf("certmgr: login response: %w", err)
	}
	if out.User.Username == "" {
		if out.Error != "" {
			return fmt.Errorf("certmgr: login failed: %s", out.Error)
		}
		return fmt.Errorf("certmgr: login failed")
	}
	c.user = out.User.Username
	c.role = out.User.Role
	return nil
}

// ListCerts 列出 Relay 已登记的证书。
func (c *AdminClient) ListCerts() ([]RemoteCert, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("certmgr: not authenticated")
	}
	resp, err := c.do(http.MethodGet, "/api/certs", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Certs []RemoteCert `json:"certs"`
		Error string       `json:"error"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("certmgr: list certs: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("certmgr: list certs: %s", out.Error)
	}
	if out.Certs == nil {
		return []RemoteCert{}, nil
	}
	return out.Certs, nil
}

// Revoke 吊销指定序列号的证书。吊销由 Relay 数据库即时生效。
func (c *AdminClient) Revoke(serial string) error {
	if !c.LoggedIn() {
		return fmt.Errorf("certmgr: not authenticated")
	}
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return fmt.Errorf("certmgr: serial is required")
	}
	body, err := json.Marshal(map[string]string{"serial": serial})
	if err != nil {
		return err
	}
	resp, err := c.do(http.MethodPost, "/api/certs/revoke", body)
	if err != nil {
		return err
	}
	var out struct {
		OK    string `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return fmt.Errorf("certmgr: revoke: %w", err)
	}
	if out.Error != "" {
		return fmt.Errorf("certmgr: revoke: %s", out.Error)
	}
	if out.OK == "" {
		return fmt.Errorf("certmgr: revoke failed")
	}
	return nil
}

// Logout 清除会话 Cookie。不涉及密码存储。
func (c *AdminClient) Logout() {
	if c == nil || c.http == nil {
		return
	}
	_, _ = c.do(http.MethodPost, "/api/logout", nil)
	c.user = ""
	c.role = ""
	jar, _ := cookiejar.New(nil)
	c.http.Jar = jar
}

func (c *AdminClient) do(method, path string, body []byte) ([]byte, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		if u, err := url.Parse(c.baseURL); err == nil && u.Host != "" {
			req.Header.Set("Origin", u.Scheme+"://"+u.Host)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &out)
		if out.Error != "" {
			return nil, fmt.Errorf("certmgr: %s %s: %s", method, path, out.Error)
		}
		return nil, fmt.Errorf("certmgr: %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	return data, nil
}
