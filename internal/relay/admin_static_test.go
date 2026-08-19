package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticHandlerIndexCacheBust(t *testing.T) {
	a := &AdminServer{}
	h := a.staticHandler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("html cache-control: %s", rec.Header().Get("Cache-Control"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `/static/app.js?v=`) {
		t.Fatalf("index html should cache-bust app.js")
	}
	if !strings.Contains(body, `/static/style.css?v=`) {
		t.Fatalf("index html should cache-bust style.css")
	}

	js := httptest.NewRecorder()
	h.ServeHTTP(js, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	if js.Code != http.StatusOK {
		t.Fatalf("app.js status %d", js.Code)
	}
	if !strings.Contains(js.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("js cache-control: %s", js.Header().Get("Cache-Control"))
	}
}

func TestVerifyTurnstileEmptyToken(t *testing.T) {
	if verifyTurnstile("secret", "") {
		t.Fatal("empty token must fail")
	}
	if verifyTurnstile("", "token") {
		t.Fatal("empty secret must fail")
	}
}

func TestVerifyTurnstileSiteverify(t *testing.T) {
	var gotRemoteIP bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotRemoteIP = strings.Contains(string(body), "remoteip")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()
	old := turnstileVerifyURL
	turnstileVerifyURL = srv.URL
	defer func() { turnstileVerifyURL = old }()

	if !verifyTurnstile("secret", "ok-token") {
		t.Fatal("expected success")
	}
	if gotRemoteIP {
		t.Fatal("siteverify must not send remoteip")
	}
}
