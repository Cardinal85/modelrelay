package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"modelrelay/internal/relay"
)

// adminClient 携带会话 Cookie 的管理 API 客户端。
type adminClient struct {
	t      *testing.T
	env    *testEnv
	client *http.Client
}

func loginAdmin(t *testing.T, env *testEnv, user, pass string) *adminClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &adminClient{t: t, env: env, client: &http.Client{Jar: jar}}
	resp := c.doRaw("/api/login", "POST", fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass))
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s failed: %d %s", user, resp.StatusCode, body)
	}
	return c
}

func (c *adminClient) doRaw(path, method, body string) *http.Response {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, c.env.adminURL+path, reader)
	if err != nil {
		c.t.Fatalf("req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("do %s: %v", path, err)
	}
	return resp
}

func (c *adminClient) get(path string) (int, []byte) {
	c.t.Helper()
	resp := c.doRaw(path, "GET", "")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func (c *adminClient) post(path, body string) (int, []byte) {
	c.t.Helper()
	resp := c.doRaw(path, "POST", body)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestAdminLoginAndOverview(t *testing.T) {
	env := setup(t)

	// 未认证 → 401。
	resp, _ := env.do(t, http.MethodGet, "/api/overview", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", resp.StatusCode)
	}

	// 错误密码 → 401。
	bad := loginRaw(t, env, "admin", "wrong")
	if bad != http.StatusUnauthorized {
		t.Fatalf("bad password status=%d", bad)
	}

	// 正确登录。
	admin := loginAdmin(t, env, "admin", "admin-pass")

	// /api/me。
	st, body := admin.get("/api/me")
	if st != http.StatusOK || !strings.Contains(string(body), `"admin"`) {
		t.Fatalf("me: %d %s", st, body)
	}

	// /api/overview。
	st, body = admin.get("/api/overview")
	if st != http.StatusOK {
		t.Fatalf("overview: %d %s", st, body)
	}
	var ov struct {
		Nodes struct {
			Online int `json:"online"`
		} `json:"nodes"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &ov); err != nil {
		t.Fatalf("overview unmarshal: %v", err)
	}
	if ov.Nodes.Online != 1 {
		t.Fatalf("online nodes=%d", ov.Nodes.Online)
	}
	found := false
	for _, m := range ov.Models {
		if m.ID == "test-model" {
			found = true
		}
	}
	if !found {
		t.Fatalf("model missing in overview: %s", body)
	}

	// /api/nodes。
	st, body = admin.get("/api/nodes")
	if st != http.StatusOK || !strings.Contains(string(body), "node-1") {
		t.Fatalf("nodes: %d %s", st, body)
	}

	// /api/settings。
	st, body = admin.get("/api/settings")
	if st != http.StatusOK || !strings.Contains(string(body), "relay-e2e") {
		t.Fatalf("settings: %d %s", st, body)
	}

	// /api/audit。
	st, body = admin.get("/api/audit")
	if st != http.StatusOK {
		t.Fatalf("audit: %d", st)
	}
}

func TestAdminNodeActions(t *testing.T) {
	env := setup(t)
	admin := loginAdmin(t, env, "admin", "admin-pass")

	// 触发探测。
	st, body := admin.post("/api/nodes/node-1/probe", "")
	if st != http.StatusOK {
		t.Fatalf("probe: %d %s", st, body)
	}

	// Drain：API 返回后节点应已同步进入 draining 状态。
	st, body = admin.post("/api/nodes/node-1/drain", `{"grace_seconds":30}`)
	if st != http.StatusOK {
		t.Fatalf("drain: %d %s", st, body)
	}
	n := env.relaySrv.Registry().Get("node-1")
	if n == nil || n.Snapshot().State != relay.StateDraining {
		t.Fatalf("node not draining after drain: %+v", n.Snapshot())
	}

	// 踢出：API 返回后旧连接被断开；Agent 会自动重连，新连接 ConnectedAt 应更新。
	before := env.relaySrv.Registry().Get("node-1").Snapshot().ConnectedAt
	st, body = admin.post("/api/nodes/node-1/kick", "")
	if st != http.StatusOK {
		t.Fatalf("kick: %d %s", st, body)
	}
	waitFor(t, 10*time.Second, func() bool {
		n := env.relaySrv.Registry().Get("node-1")
		return n != nil && n.Snapshot().ConnectedAt.After(before)
	})

	// 审计应有记录。
	_, body = admin.get("/api/audit")
	for _, action := range []string{"probe", "drain", "kick"} {
		if !strings.Contains(string(body), action) {
			t.Fatalf("audit missing %s: %s", action, body)
		}
	}
}

func TestAdminReadonlyRole(t *testing.T) {
	env := setup(t)
	reader := loginAdmin(t, env, "reader", "reader-pass")

	// 只读可以查看。
	st, _ := reader.get("/api/nodes")
	if st != http.StatusOK {
		t.Fatalf("readonly get nodes: %d", st)
	}

	// 只读禁止写操作。
	st, _ = reader.post("/api/nodes/node-1/kick", "")
	if st != http.StatusForbidden {
		t.Fatalf("readonly kick status=%d", st)
	}
	st, _ = reader.post("/api/certs/revoke", `{"serial":"x"}`)
	if st != http.StatusForbidden {
		t.Fatalf("readonly revoke status=%d", st)
	}
}

func TestAdminRevocationDisconnectsAndRejectsReconnect(t *testing.T) {
	env := setup(t)
	admin := loginAdmin(t, env, "admin", "admin-pass")
	snap := env.relaySrv.Registry().Get("node-1").Snapshot()
	if snap.CertSerial == "" {
		t.Fatal("node certificate serial was not recorded")
	}

	status, body := admin.post("/api/certs/revoke", fmt.Sprintf(`{"serial":%q}`, snap.CertSerial))
	if status != http.StatusOK {
		t.Fatalf("revoke: %d %s", status, body)
	}
	waitFor(t, 5*time.Second, func() bool {
		return env.relaySrv.Registry().Get("node-1") == nil
	})
	if revoked, err := env.st.IsCertRevoked(snap.CertSerial); err != nil || !revoked {
		t.Fatalf("certificate not revoked: revoked=%v err=%v", revoked, err)
	}
}

func TestAdminRequestsRecorded(t *testing.T) {
	env := setup(t)
	admin := loginAdmin(t, env, "admin", "admin-pass")

	// 发一次业务请求。
	body := `{"model":"test-model","messages":[{"role":"user","content":"track me"}]}`
	resp, _ := env.do(t, http.MethodPost, "/v1/chat/completions", []byte(body), authHeader(env.token))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status=%d", resp.StatusCode)
	}

	// 请求追踪页应能看到记录。
	waitFor(t, 5*time.Second, func() bool {
		st, b := admin.get("/api/requests")
		if st != http.StatusOK {
			return false
		}
		var d struct {
			Requests []struct {
				Path   string `json:"path"`
				Status int    `json:"status"`
				Node   string `json:"node"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(b, &d); err != nil {
			return false
		}
		for _, r := range d.Requests {
			if r.Path == "/v1/chat/completions" && r.Status == 200 && r.Node == "node-1" {
				return true
			}
		}
		return false
	})

	// SQLite 中也有摘要。
	reqs, err := env.st.ListRequestSummaries(10)
	if err != nil || len(reqs) == 0 {
		t.Fatalf("no persisted summaries: %v", err)
	}
	if reqs[0].Path != "/v1/chat/completions" {
		t.Fatalf("summary path=%s", reqs[0].Path)
	}
}

func TestAdminWebUIServed(t *testing.T) {
	env := setup(t)
	resp, err := http.Get(env.adminURL + "/")
	if err != nil {
		t.Fatalf("get webui: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webui status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "ModelRelay") {
		t.Fatal("webui index missing")
	}
	// 静态资源。
	resp2, err := http.Get(env.adminURL + "/static/app.js")
	if err != nil || resp2.StatusCode != http.StatusOK {
		t.Fatalf("static app.js: %v %v", err, resp2)
	}
	resp2.Body.Close()
}

// loginRaw 执行一次登录并返回状态码（不保留会话）。
func loginRaw(t *testing.T, env *testEnv, user, pass string) int {
	t.Helper()
	resp, err := http.Post(env.adminURL+"/api/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass)))
	if err != nil {
		t.Fatalf("login req: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
