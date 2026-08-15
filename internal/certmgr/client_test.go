package certmgr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdminClientLoginListRevoke(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Username != "admin" || body.Password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid username or password"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "modelrelay_session", Value: "tok", Path: "/"})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]string{"username": "admin", "role": "admin"},
		})
	})
	mux.HandleFunc("/api/certs", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("modelrelay_session"); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not authenticated"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"certs": []RemoteCert{{
				NodeID:    "gpu-001",
				Serial:    "abc123",
				Subject:   "CN=gpu-001",
				Status:    "active",
				NotBefore: time.Now().Add(-time.Hour),
				NotAfter:  time.Now().Add(24 * time.Hour),
			}},
		})
	})
	mux.HandleFunc("/api/certs/revoke", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("modelrelay_session"); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not authenticated"})
			return
		}
		var body struct {
			Serial string `json:"serial"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Serial != "abc123" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "serial required"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "revoked"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := NewAdminClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("admin", "wrong"); err == nil {
		t.Fatal("bad password must fail")
	}
	if c.LoggedIn() {
		t.Fatal("failed login must not keep session")
	}
	if err := c.Login("admin", "secret"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if c.Username() != "admin" {
		t.Fatalf("user = %s", c.Username())
	}
	certs, err := c.ListCerts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(certs) != 1 || certs[0].Serial != "abc123" {
		t.Fatalf("certs: %+v", certs)
	}
	if err := c.Revoke("abc123"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}

func TestAdminClientAuthFailureAndNoPasswordField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid username or password"})
	}))
	defer srv.Close()

	c, err := NewAdminClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Login("admin", "nope")
	if err == nil || !strings.Contains(err.Error(), "invalid username or password") {
		t.Fatalf("expected auth error, got %v", err)
	}
	if c.LoggedIn() {
		t.Fatal("must not be logged in")
	}
	if _, err := c.ListCerts(); err == nil {
		t.Fatal("unauthenticated list must fail")
	}
	if err := c.Revoke("x"); err == nil {
		t.Fatal("unauthenticated revoke must fail")
	}

	raw, _ := json.Marshal(c)
	if strings.Contains(strings.ToLower(string(raw)), "nope") || strings.Contains(strings.ToLower(string(raw)), "password") {
		t.Fatalf("client must not persist password: %s", raw)
	}
}

func TestNewAdminClientRequiresURL(t *testing.T) {
	if _, err := NewAdminClient("  "); err == nil {
		t.Fatal("empty url must fail")
	}
}
