package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

func verifyTurnstile(secret, token, remoteIP string) bool {
	if secret == "" || strings.TrimSpace(token) == "" {
		return false
	}
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.PostForm(turnstileVerifyURL, form)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return false
	}
	var out struct {
		Success bool `json:"success"`
	}
	if json.Unmarshal(body, &out) != nil {
		return false
	}
	return out.Success
}
