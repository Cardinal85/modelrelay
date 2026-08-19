package relay

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

func verifyTurnstile(secret, token string) bool {
	if secret == "" || strings.TrimSpace(token) == "" {
		return false
	}
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	// Do not send remoteip: behind Cloudflare the connecting address is often an
	// edge IP, and a mismatch causes siteverify to fail even with a valid token.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.PostForm(turnstileVerifyURL, form)
	if err != nil {
		log.Printf("relay: turnstile siteverify request failed: %v", err)
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		log.Printf("relay: turnstile siteverify read failed: %v", err)
		return false
	}
	var out struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		log.Printf("relay: turnstile siteverify decode failed: %v", err)
		return false
	}
	if !out.Success {
		log.Printf("relay: turnstile siteverify failed: error-codes=%v", out.ErrorCodes)
	}
	return out.Success
}
