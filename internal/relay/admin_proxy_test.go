package relay

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustedProxy(t *testing.T) {
	a := &AdminServer{trustedProxies: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}

	fromLoopback := httptest.NewRequest(http.MethodGet, "/", nil)
	fromLoopback.RemoteAddr = "127.0.0.1:54321"
	fromLoopback.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := a.clientIP(fromLoopback); got != "203.0.113.9" {
		t.Fatalf("trusted proxy CF-Connecting-IP: got %s", got)
	}

	fromLoopback.Header.Del("CF-Connecting-IP")
	fromLoopback.Header.Set("X-Forwarded-For", "198.51.100.4, 10.0.0.1")
	if got := a.clientIP(fromLoopback); got != "198.51.100.4" {
		t.Fatalf("trusted proxy X-Forwarded-For: got %s", got)
	}

	fromInternet := httptest.NewRequest(http.MethodGet, "/", nil)
	fromInternet.RemoteAddr = "8.8.8.8:443"
	fromInternet.Header.Set("CF-Connecting-IP", "203.0.113.9")
	fromInternet.Header.Set("X-Forwarded-For", "198.51.100.4")
	if got := a.clientIP(fromInternet); got != "8.8.8.8" {
		t.Fatalf("untrusted remote must ignore forwarded headers, got %s", got)
	}
}

func TestSecureCookieFromTrustedProto(t *testing.T) {
	a := &AdminServer{trustedProxies: []net.IP{net.ParseIP("127.0.0.1")}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Header.Set("X-Forwarded-Proto", "https")
	if !a.useSecureCookie(req) {
		t.Fatal("https forwarded from trusted proxy should set Secure cookie")
	}

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	plain.RemoteAddr = "8.8.8.8:9"
	plain.Header.Set("X-Forwarded-Proto", "https")
	if a.useSecureCookie(plain) {
		t.Fatal("untrusted X-Forwarded-Proto must not set Secure cookie")
	}
}
