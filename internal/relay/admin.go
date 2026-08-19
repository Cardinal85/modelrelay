package relay

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"modelrelay/internal/protocol"
	"modelrelay/internal/store"
	"modelrelay/internal/version"
	"modelrelay/internal/webui"
)

const sessionCookieName = "modelrelay_session"
const loginBodyLimit = 16 << 10

// AdminOptions 是管理服务的监听与安全选项。
type AdminOptions struct {
	Listen          string
	SessionTTL      time.Duration
	TrustedProxies  []string
	SecureCookie    bool
	TurnstileSite   string
	TurnstileSecret string
}

// session 是管理会话。
type session struct {
	username string
	role     string
	expiry   time.Time
}

type loginFail struct {
	count int
	until time.Time
}

// AdminServer 是 Relay 管理 API 与 WebUI 服务。
type AdminServer struct {
	srv        *Server
	st         *store.Store
	listen     string
	sessionTTL time.Duration

	mu         sync.Mutex
	sessions   map[string]*session
	loginFails map[string]loginFail

	trustedProxies  []net.IP
	secureCookie    bool
	turnstileSite   string
	turnstileSecret string

	httpSrv  *http.Server
	listener net.Listener
}

// NewAdminServer 创建管理服务。
func NewAdminServer(srv *Server, st *store.Store, listen string, sessionTTL time.Duration) (*AdminServer, error) {
	return NewAdminServerWithOptions(srv, st, AdminOptions{Listen: listen, SessionTTL: sessionTTL})
}

// NewAdminServerWithOptions 创建带反代/Turnstile 选项的管理服务。
func NewAdminServerWithOptions(srv *Server, st *store.Store, opts AdminOptions) (*AdminServer, error) {
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 30 * time.Minute
	}
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("relay: admin listen %s: %w", opts.Listen, err)
	}
	a := &AdminServer{
		srv:             srv,
		st:              st,
		listen:          opts.Listen,
		sessionTTL:      opts.SessionTTL,
		sessions:        make(map[string]*session),
		loginFails:      make(map[string]loginFail),
		secureCookie:    opts.SecureCookie,
		turnstileSite:   opts.TurnstileSite,
		turnstileSecret: opts.TurnstileSecret,
		listener:        ln,
	}
	for _, p := range opts.TrustedProxies {
		if ip := net.ParseIP(p); ip != nil {
			a.trustedProxies = append(a.trustedProxies, ip)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login-config", a.handleLoginConfig)
	mux.HandleFunc("/api/login", a.handleLogin)
	mux.HandleFunc("/api/", a.handleAPI)
	mux.Handle("/", a.staticHandler())
	a.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return a, nil
}

func (a *AdminServer) staticHandler() http.Handler {
	fs := webui.StaticFS()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		switch name {
		case "", "/", ".", "index.html":
			name = "index.html"
		}
		f, err := fs.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		switch {
		case strings.HasSuffix(name, ".html"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			v := version.RelayVersion()
			data = bytes.ReplaceAll(data, []byte(`href="/static/style.css"`), []byte(`href="/static/style.css?v=`+v+`"`))
			data = bytes.ReplaceAll(data, []byte(`src="/static/app.js"`), []byte(`src="/static/app.js?v=`+v+`"`))
		case strings.HasSuffix(name, ".js"):
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasSuffix(name, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Cache-Control", "no-store")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
}

// Start 启动管理服务（阻塞）。
func (a *AdminServer) Start() error {
	err := a.httpSrv.Serve(a.listener)
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Addr 返回监听地址。
func (a *AdminServer) Addr() net.Addr { return a.listener.Addr() }

// Close 关闭管理服务。
func (a *AdminServer) Close() error { return a.httpSrv.Close() }

// --- 认证 ---

func (a *AdminServer) handleLoginConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"turnstile_site_key": a.turnstileSite,
	})
}

func (a *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.rejectCrossOrigin(w, r) {
		return
	}
	ip := a.clientIP(r)
	a.mu.Lock()
	f := a.loginFails[ip]
	if f.count >= 5 && time.Now().Before(f.until) {
		a.mu.Unlock()
		writeAdminError(w, http.StatusTooManyRequests, "too many login attempts, retry later")
		return
	}
	a.mu.Unlock()

	r.Body = http.MaxBytesReader(w, r.Body, loginBodyLimit)
	var body struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if a.turnstileSecret != "" {
		if strings.TrimSpace(body.TurnstileToken) == "" {
			writeAdminError(w, http.StatusUnauthorized, "turnstile verification failed")
			return
		}
		if !verifyTurnstile(a.turnstileSecret, body.TurnstileToken) {
			a.recordLoginFail(ip)
			writeAdminError(w, http.StatusUnauthorized, "turnstile verification failed")
			return
		}
	}
	user, err := a.st.GetAdminUser(body.Username)
	if err != nil || !store.CheckPassword(user.PasswordHash, body.Password) {
		a.recordLoginFail(ip)
		writeAdminError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal error")
		return
	}
	a.mu.Lock()
	a.sessions[token] = &session{username: user.Username, role: user.Role, expiry: time.Now().Add(a.sessionTTL)}
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.useSecureCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(a.sessionTTL.Seconds()),
	})
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user": map[string]string{"username": user.Username, "role": user.Role},
	})
}

func (a *AdminServer) recordLoginFail(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.loginFails[ip]
	if time.Now().After(f.until) {
		f.count = 0
	}
	f.count++
	f.until = time.Now().Add(60 * time.Second)
	a.loginFails[ip] = f
}

func (a *AdminServer) sessionFromReq(r *http.Request) (*session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, false
	}
	a.mu.Lock()
	s, ok := a.sessions[c.Value]
	if ok && time.Now().After(s.expiry) {
		delete(a.sessions, c.Value)
		ok = false
	}
	a.mu.Unlock()
	if !ok {
		return nil, false
	}
	return s, true
}

// handleAPI 是所有 /api/* 的入口（除 login）。
func (a *AdminServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		writeAdminError(w, http.StatusNotFound, "not found")
		return
	}

	// 会话认证。
	s, ok := a.sessionFromReq(r)
	if !ok {
		writeAdminError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// CSRF：非安全方法必须带同源 Origin。
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		if a.rejectCrossOrigin(w, r) {
			return
		}
	}

	// 只读角色禁止写操作。
	if s.role == "readonly" && r.Method != http.MethodGet {
		writeAdminError(w, http.StatusForbidden, "readonly role cannot modify")
		return
	}

	switch {
	case path == "/logout" && r.Method == http.MethodPost:
		a.handleLogout(w, r)
	case path == "/me":
		_ = json.NewEncoder(w).Encode(map[string]string{"username": s.username, "role": s.role})
	case path == "/overview":
		a.handleOverview(w)
	case path == "/nodes":
		a.handleNodes(w)
	case strings.HasPrefix(path, "/nodes/") && r.Method == http.MethodGet:
		a.handleNodeDetail(w, strings.TrimPrefix(path, "/nodes/"))
	case strings.HasPrefix(path, "/nodes/") && strings.HasSuffix(path, "/drain"):
		a.handleNodeAction(w, r, s, strings.TrimSuffix(strings.TrimPrefix(path, "/nodes/"), "/drain"), "drain")
	case strings.HasPrefix(path, "/nodes/") && strings.HasSuffix(path, "/kick"):
		a.handleNodeAction(w, r, s, strings.TrimSuffix(strings.TrimPrefix(path, "/nodes/"), "/kick"), "kick")
	case strings.HasPrefix(path, "/nodes/") && strings.HasSuffix(path, "/probe"):
		a.handleNodeAction(w, r, s, strings.TrimSuffix(strings.TrimPrefix(path, "/nodes/"), "/probe"), "probe")
	case strings.HasPrefix(path, "/nodes/") && strings.HasSuffix(path, "/concurrency"):
		a.handleNodeConcurrency(w, r, s, strings.TrimSuffix(strings.TrimPrefix(path, "/nodes/"), "/concurrency"))
	case path == "/capabilities":
		a.handleCapabilities(w)
	case path == "/certs":
		a.handleCerts(w)
	case path == "/certs/revoke":
		a.handleCertRevoke(w, r, s)
	case path == "/requests":
		a.handleRequests(w)
	case path == "/audit":
		a.handleAudit(w)
	case path == "/settings":
		a.handleSettings(w)
	case path == "/settings/retention":
		a.handleRetention(w, r, s)
	default:
		writeAdminError(w, http.StatusNotFound, "not found")
	}
}

func (a *AdminServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, Secure: a.useSecureCookie(r), HttpOnly: true})
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "logged out"})
}

// --- 业务端点 ---

func (a *AdminServer) handleOverview(w http.ResponseWriter) {
	counts := a.srv.reg.CountByState()
	active := 0
	for _, n := range a.srv.reg.List() {
		active += n.Active
	}
	expiring := 0
	if a.st != nil {
		expiring, _ = a.st.CertExpiring(30)
	}
	recent := a.srv.RecentRequests()
	var recentErrs []map[string]any
	for _, r := range recent {
		if r.ErrorCode != "" {
			recentErrs = append(recentErrs, map[string]any{
				"time": r.Time, "error_code": r.ErrorCode, "model": r.Model, "node": r.Node,
			})
		}
		if len(recentErrs) >= 8 {
			break
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"relay_id":         a.srv.cfg.RelayID,
		"version":          version.RelayVersion(),
		"protocol_version": protocol.ProtocolVersion,
		"stats":            a.srv.stats.Snapshot(),
		"nodes": map[string]int{
			"online": counts[StateOnline], "offline": counts[StateOffline],
			"suspect": counts[StateSuspect], "degraded": counts[StateDegraded],
			"draining": counts[StateDraining],
		},
		"active_requests": active,
		"queued":          a.srv.waiting.Load(),
		"certs_expiring":  expiring,
		"models":          a.srv.reg.ModelDirectory(),
		"recent_errors":   recentErrs,
	})
}

func (a *AdminServer) handleNodes(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": a.srv.reg.List()})
}

func (a *AdminServer) handleNodeDetail(w http.ResponseWriter, id string) {
	n := a.srv.reg.Get(id)
	if n == nil {
		writeAdminError(w, http.StatusNotFound, "node not found")
		return
	}
	snap := n.Snapshot()
	modelSnapshot := n.ModelSnapshot()
	models := make([]map[string]any, 0, len(modelSnapshot))
	for mid, mc := range modelSnapshot {
		models = append(models, map[string]any{"id": mid, "capabilities": mc.Capabilities, "probe_time": mc.ProbeTime})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"node": snap, "models": models})
}

func (a *AdminServer) handleNodeAction(w http.ResponseWriter, r *http.Request, s *session, id, action string) {
	var err error
	switch action {
	case "drain":
		var body struct {
			GraceSeconds int `json:"grace_seconds"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		grace := body.GraceSeconds
		if grace <= 0 {
			grace = 300
		}
		err = a.srv.DrainNode(id, grace)
	case "kick":
		err = a.srv.KickNode(id)
	case "probe":
		err = a.srv.sendToNode(id, protocol.Probe{Type: protocol.MsgProbe})
	}
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.st != nil {
		_ = a.st.AddAudit(s.username, action, id, "")
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": action})
}

func (a *AdminServer) handleNodeConcurrency(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var body struct {
		MaxConcurrency int `json:"max_concurrency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	n := a.srv.reg.Get(id)
	if n == nil {
		writeAdminError(w, http.StatusNotFound, "node not found")
		return
	}
	if body.MaxConcurrency <= 0 {
		writeAdminError(w, http.StatusBadRequest, "max_concurrency must be positive")
		return
	}
	n.SetMaxConcurrency(body.MaxConcurrency)
	if a.st != nil {
		_ = a.st.AddAudit(s.username, "set_concurrency", id, fmt.Sprintf("%d", body.MaxConcurrency))
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "updated"})
}

func (a *AdminServer) handleCapabilities(w http.ResponseWriter) {
	type nodeCaps struct {
		ID         string           `json:"id"`
		ModelCount int              `json:"model_count"`
		Models     []map[string]any `json:"models"`
	}
	var out []nodeCaps
	for _, snap := range a.srv.reg.List() {
		n := a.srv.reg.Get(snap.ID)
		nc := nodeCaps{ID: snap.ID, ModelCount: snap.ModelCount}
		for mid, mc := range n.ModelSnapshot() {
			nc.Models = append(nc.Models, map[string]any{"id": mid, "capabilities": mc.Capabilities, "probe_time": mc.ProbeTime})
		}
		out = append(out, nc)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": out})
}

func (a *AdminServer) handleCerts(w http.ResponseWriter) {
	certs := []store.CertMeta{}
	expiring := 0
	if a.st != nil {
		certs, _ = a.st.ListCerts()
		expiring, _ = a.st.CertExpiring(30)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"certs": certs, "expiring": expiring})
}

func (a *AdminServer) handleCertRevoke(w http.ResponseWriter, r *http.Request, s *session) {
	var body struct {
		Serial string `json:"serial"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Serial == "" {
		writeAdminError(w, http.StatusBadRequest, "serial required")
		return
	}
	if a.st == nil {
		writeAdminError(w, http.StatusBadRequest, "store not configured")
		return
	}
	meta, err := a.st.GetCert(body.Serial)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.st.RevokeCert(body.Serial, "revoked_by_admin"); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if n := a.srv.reg.Get(meta.NodeID); n != nil && n.Snapshot().CertSerial == body.Serial {
		// 吊销不仅影响后续握手，也必须立即切断当前连接。
		_ = a.srv.KickNode(meta.NodeID)
	}
	_ = a.st.AddAudit(s.username, "revoke_cert", body.Serial, "")
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "revoked"})
}

func (a *AdminServer) handleRequests(w http.ResponseWriter) {
	recent := a.srv.RecentRequests()
	out := make([]map[string]any, 0, len(recent))
	for _, r := range recent {
		out = append(out, map[string]any{
			"request_id": r.RequestID, "time": r.Time, "path": r.Path,
			"model": r.Model, "node": r.Node, "status": r.Status,
			"ttft_ms": r.TTFTMs, "duration_ms": r.DurationMs, "error_code": r.ErrorCode,
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"requests": out})
}

func (a *AdminServer) handleAudit(w http.ResponseWriter) {
	entries := []store.AuditEntry{}
	if a.st != nil {
		entries, _ = a.st.ListAudit(200)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
}

func (a *AdminServer) handleSettings(w http.ResponseWriter) {
	users := []store.AdminUser{}
	if a.st != nil {
		users, _ = a.st.ListAdminUsers()
	}
	type userView struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	uv := make([]userView, 0, len(users))
	for _, u := range users {
		uv = append(uv, userView{Username: u.Username, Role: u.Role})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"relay": map[string]any{
			"relay_id":         a.srv.cfg.RelayID,
			"http_listen":      a.srv.cfg.HTTPListen,
			"wss_listen":       a.srv.cfg.WSSListen,
			"version":          version.RelayVersion(),
			"protocol_version": protocol.ProtocolVersion,
			"retention": map[string]any{
				"keep_prompt_response": a.srv.keepPrompt,
				"retention_days":       a.srv.retentionD,
			},
		},
		"users": uv,
	})
}

func (a *AdminServer) handleRetention(w http.ResponseWriter, r *http.Request, s *session) {
	var body struct {
		KeepPromptResponse bool `json:"keep_prompt_response"`
		RetentionDays      int  `json:"retention_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.RetentionDays <= 0 {
		body.RetentionDays = 30
	}
	a.srv.SetRetention(body.KeepPromptResponse, body.RetentionDays)
	if a.st != nil {
		_ = a.st.AddAudit(s.username, "set_retention", "",
			fmt.Sprintf("keep=%v days=%d", body.KeepPromptResponse, body.RetentionDays))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "restart_required": false})
}

// --- 辅助 ---

func writeAdminError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (a *AdminServer) rejectCrossOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		writeAdminError(w, http.StatusForbidden, "cross-origin request rejected")
		return true
	}
	wantHTTP := "http://" + r.Host
	wantHTTPS := "https://" + r.Host
	if origin != wantHTTP && origin != wantHTTPS {
		writeAdminError(w, http.StatusForbidden, "cross-origin request rejected")
		return true
	}
	return false
}

func (a *AdminServer) useSecureCookie(r *http.Request) bool {
	if a.secureCookie {
		return true
	}
	if a.fromTrustedProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return r.TLS != nil
}

func (a *AdminServer) fromTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, p := range a.trustedProxies {
		if p.Equal(ip) {
			return true
		}
	}
	return false
}

func (a *AdminServer) clientIP(r *http.Request) string {
	direct := clientIP(r)
	if !a.fromTrustedProxy(r) {
		return direct
	}
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		if ip := net.ParseIP(cf); ip != nil {
			return ip.String()
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if ip := net.ParseIP(first); ip != nil {
			return ip.String()
		}
	}
	return direct
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
