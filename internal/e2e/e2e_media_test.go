package e2e

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"

	"modelrelay/internal/testutil"
)

// TestE2EMultipartPassthrough 验证 multipart 请求完整透传到本地模型。
func TestE2EMultipartPassthrough(t *testing.T) {
	env := setup(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", "test-model"); err != nil {
		t.Fatal(err)
	}
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x01}); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, env.httpURL+"/v1/audio/transcriptions", &buf)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	data := make([]byte, 4096)
	n, _ := resp.Body.Read(data)
	body := string(data[:n])
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if !bytes.Contains([]byte(body), []byte("transcribed: test-model")) {
		t.Fatalf("multipart not passed through: %s", body)
	}
	if env.mock.TranscriptionsCalled() != 1 {
		t.Fatalf("mock transcriptions calls=%d", env.mock.TranscriptionsCalled())
	}
}

// TestE2EBinaryResponsePassthrough 验证二进制响应逐字节透传。
func TestE2EBinaryResponsePassthrough(t *testing.T) {
	env := setup(t)

	body := `{"model":"test-model","input":"hello"}`
	resp, data := env.do(t, http.MethodPost, "/v1/audio/speech", []byte(body), authHeader(env.token))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data[:min(len(data), 200)])
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("content-type=%s", ct)
	}
	if !bytes.Equal(data, testutil.SpeechBytes) {
		t.Fatalf("binary payload mismatch: got %d bytes want %d", len(data), len(testutil.SpeechBytes))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
