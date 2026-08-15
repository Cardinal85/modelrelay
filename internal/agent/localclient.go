package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"modelrelay/internal/config"
	"modelrelay/internal/protocol"
)

// LocalClient 调用本地 OpenAI-compatible 模型服务。
type LocalClient struct {
	baseURL string
	apiKey  string

	httpClient *http.Client
	lastOK     atomic.Bool
	lastErr    atomic.Value // string
}

// NewLocalClient 创建本地模型客户端。
func NewLocalClient(cfg config.Local) (*LocalClient, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.ConnectTimeoutSec > 0 {
		dialer := &net.Dialer{Timeout: time.Duration(cfg.ConnectTimeoutSec) * time.Second}
		transport.DialContext = dialer.DialContext
	}
	if strings.HasPrefix(cfg.BaseURL, "https://") && !cfg.TLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 显式配置
	}
	timeout := time.Duration(cfg.ResponseTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	l := &LocalClient{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}
	return l, nil
}

// BuildRequest 构造本地 HTTP 请求。
func (l *LocalClient) BuildRequest(ctx context.Context, req protocol.Request, body []byte) (*http.Request, error) {
	target := l.targetURL(req.Path, req.Query)
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build local request: %w", err)
	}
	// 业务 Header 白名单。
	for _, k := range []string{"Content-Type", "Accept", "OpenAI-Beta", "Idempotency-Key", "Content-Disposition"} {
		if v := req.Headers[k]; v != "" {
			httpReq.Header.Set(k, v)
		}
	}
	// 本地模型认证。
	if l.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+l.apiKey)
	}
	return httpReq, nil
}

// Do 执行本地请求。
func (l *LocalClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := l.httpClient.Do(req)
	l.recordHealth(err)
	return resp, err
}

// recordHealth 记录最近一次本地调用结果。
func (l *LocalClient) recordHealth(err error) {
	if err != nil {
		l.lastOK.Store(false)
		l.lastErr.Store(err.Error())
		return
	}
	l.lastOK.Store(true)
	l.lastErr.Store("")
}

// Healthy 返回最近一次本地调用是否成功。
func (l *LocalClient) Healthy() bool { return l.lastOK.Load() }

// LastError 返回最近一次本地错误。
func (l *LocalClient) LastError() string {
	if v := l.lastErr.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// Models 获取本地模型列表（GET /v1/models）。
func (l *LocalClient) Models(ctx context.Context) ([]string, error) {
	target := l.targetURL("/v1/models", "")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if l.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.apiKey)
	}
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("models: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	l.recordHealth(nil)
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// ChatProbe 执行一次 Chat 探测（非流式/流式）。
func (l *LocalClient) ChatProbe(ctx context.Context, model string, stream bool) error {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":1,"stream":%v}`, model, stream)
	target := l.targetURL("/v1/chat/completions", "")
	return l.postJSON(ctx, target, []byte(body), stream)
}

// CompletionsProbe 执行 Completions 探测。
func (l *LocalClient) CompletionsProbe(ctx context.Context, model string) error {
	body := fmt.Sprintf(`{"model":%q,"prompt":"ping","max_tokens":1}`, model)
	return l.postJSON(ctx, l.targetURL("/v1/completions", ""), []byte(body), false)
}

// EmbeddingsProbe 执行 Embeddings 探测。
func (l *LocalClient) EmbeddingsProbe(ctx context.Context, model string) error {
	body := fmt.Sprintf(`{"model":%q,"input":"ping"}`, model)
	return l.postJSON(ctx, l.targetURL("/v1/embeddings", ""), []byte(body), false)
}

// ResponsesProbe 执行 Responses 探测。
func (l *LocalClient) ResponsesProbe(ctx context.Context, model string) error {
	body := fmt.Sprintf(`{"model":%q,"input":"ping"}`, model)
	return l.postJSON(ctx, l.targetURL("/v1/responses", ""), []byte(body), false)
}

// ToolsProbe 执行 Tools 探测。
func (l *LocalClient) ToolsProbe(ctx context.Context, model string) error {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"tools":[{"type":"function","function":{"name":"f","description":"test","parameters":{"type":"object","properties":{}}}}],"max_tokens":1}`, model)
	return l.postJSON(ctx, l.targetURL("/v1/chat/completions", ""), []byte(body), false)
}

func (l *LocalClient) postJSON(ctx context.Context, target string, body []byte, stream bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.apiKey)
	}
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 流式响应需要完整读取（少量数据）。
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe %s: status %d", target, resp.StatusCode)
	}
	l.recordHealth(nil)
	return nil
}

// targetURL 拼接目标 URL：base 以 /v1 结尾时去掉入站路径的 /v1 前缀。
func (l *LocalClient) targetURL(path, query string) string {
	base := l.baseURL
	p := path
	if strings.HasSuffix(base, "/v1") {
		p = strings.TrimPrefix(path, "/v1")
		if p == "" {
			p = "/"
		}
	}
	u := base + p
	if query != "" {
		u += "?" + query
	}
	return u
}

// ValidateTarget 校验目标地址（防 SSRF 辅助，供测试/工具使用）。
func ValidateTarget(base, path string) (string, error) {
	base = strings.TrimSuffix(base, "/")
	p := path
	if strings.HasSuffix(base, "/v1") {
		p = strings.TrimPrefix(path, "/v1")
		if p == "" {
			p = "/"
		}
	}
	u, err := url.Parse(base + p)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	return u.String(), nil
}
