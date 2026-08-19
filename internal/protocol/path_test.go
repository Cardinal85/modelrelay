package protocol

import "testing"

func TestIsAllowedPath(t *testing.T) {
	if !IsAllowedPath("POST", "/v1/chat/completions") {
		t.Fatal("chat completions should be allowed")
	}
	if !IsAllowedPath("GET", "/v1/models") {
		t.Fatal("models list should be allowed")
	}
	if IsAllowedPath("POST", "/v1/models") {
		t.Fatal("POST /v1/models should be rejected")
	}
	if IsAllowedPath("GET", "/admin") || IsAllowedPath("POST", "http://127.0.0.1/") {
		t.Fatal("non-whitelist paths must be rejected")
	}
}
