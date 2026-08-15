// Package testutil 提供测试辅助：模拟 OpenAI-compatible 上游。
package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

// MockUpstream 是一个模拟的 OpenAI-compatible 模型服务。
type MockUpstream struct {
	Server *httptest.Server

	mu           sync.Mutex
	ChatCalls    atomic.Int64
	StreamCalls  atomic.Int64
	transcriptions atomic.Int64
	models       []string
	// EchoMode 为 true 时 chat 响应回显最后一条 user 消息。
	EchoMode bool
	// ChatDelayMs 模拟本地推理延迟。
	ChatDelayMs int
	// SlowStream 开启后流式响应只发一个事件并阻塞等待客户端取消。
	SlowStream atomic.Bool
	// SlowCanceled 记录 SlowStream 请求是否被取消。
	SlowCanceled atomic.Bool
}

// NewMockUpstream 创建模拟上游。
func NewMockUpstream(models ...string) *MockUpstream {
	m := &MockUpstream{models: models, EchoMode: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", m.handleModels)
	mux.HandleFunc("/v1/chat/completions", m.handleChat)
	mux.HandleFunc("/v1/completions", m.handleCompletions)
	mux.HandleFunc("/v1/embeddings", m.handleEmbeddings)
	mux.HandleFunc("/v1/responses", m.handleResponses)
	mux.HandleFunc("/v1/audio/transcriptions", m.handleTranscriptions)
	mux.HandleFunc("/v1/audio/speech", m.handleSpeech)
	m.Server = httptest.NewServer(mux)
	return m
}

// Close 关闭模拟上游。
func (m *MockUpstream) Close() { m.Server.Close() }

// BaseURL 返回含 /v1 的 Base URL。
func (m *MockUpstream) BaseURL() string { return m.Server.URL + "/v1" }

func (m *MockUpstream) handleModels(w http.ResponseWriter, r *http.Request) {
	data := make([]map[string]any, 0, len(m.models))
	for _, id := range m.models {
		data = append(data, map[string]any{"id": id, "object": "model", "created": 0, "owned_by": "mock"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (m *MockUpstream) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Stream {
		m.StreamCalls.Add(1)
		if m.SlowStream.Load() {
			m.writeStreamSlow(w, r, body.Model)
			return
		}
		m.writeStream(w, body.Model)
		return
	}
	m.ChatCalls.Add(1)
	if m.ChatDelayMs > 0 {
		// 无 sleep 依赖，直接继续
	}
	content := "mock reply"
	if m.EchoMode && len(body.Messages) > 0 {
		content = "echo: " + body.Messages[len(body.Messages)-1].Content
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion",
		"created": 0,
		"model":   body.Model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func (m *MockUpstream) writeStream(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(http.Flusher)
	write := func(s string) {
		_, _ = fmt.Fprint(w, s)
		if fl != nil {
			fl.Flush()
		}
	}
	write("data: " + streamEvent(model, "Hello") + "\n\n")
	write("data: " + streamEvent(model, " world") + "\n\n")
	write("data: [DONE]\n\n")
}

func streamEvent(model, delta string) string {
	b, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"delta":  map[string]any{"content": delta},
			"finish_reason": nil,
		}},
	})
	return string(b)
}

// writeStreamSlow 发送一个事件后阻塞，直到请求被取消（用于取消传播测试）。
func (m *MockUpstream) writeStreamSlow(w http.ResponseWriter, r *http.Request, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(http.Flusher)
	_, _ = fmt.Fprint(w, "data: "+streamEvent(model, "first")+"\n\n")
	if fl != nil {
		fl.Flush()
	}
	<-r.Context().Done()
	m.SlowCanceled.Store(true)
}

func (m *MockUpstream) handleCompletions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "cmpl-mock",
		"object":  "text_completion",
		"created": 0,
		"model":   "mock",
		"choices": []map[string]any{{"text": "mock", "index": 0, "finish_reason": "stop"}},
	})
}

func (m *MockUpstream) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   []map[string]any{{"object": "embedding", "index": 0, "embedding": []float64{0.1, 0.2, 0.3}}},
		"model":  "mock",
		"usage":  map[string]any{"prompt_tokens": 1, "total_tokens": 1},
	})
}

func (m *MockUpstream) handleResponses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "resp-mock",
		"object":  "response",
		"created": 0,
		"model":   "mock",
		"output":  []map[string]any{{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "mock"}}}},
	})
}

// TranscriptionsCalled 统计 multipart 转写调用。
func (m *MockUpstream) TranscriptionsCalled() int64 { return m.transcriptions.Load() }

// handleTranscriptions 处理 multipart 音频转写（回显 model 字段）。
func (m *MockUpstream) handleTranscriptions(w http.ResponseWriter, r *http.Request) {
	m.transcriptions.Add(1)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "bad multipart", http.StatusBadRequest)
		return
	}
	model := r.FormValue("model")
	// 校验文件 part 存在。
	_, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file part", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"text":  "transcribed: " + model,
		"model": model,
	})
}

// SpeechBytes 返回模拟音频二进制（确定性模式）。
var SpeechBytes = func() []byte {
	b := make([]byte, 8*1024)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}()

// handleSpeech 处理音频合成（返回二进制）。
func (m *MockUpstream) handleSpeech(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	w.Header().Set("Content-Type", "audio/wav")
	_, _ = w.Write(SpeechBytes)
}

// HasStream 检查响应是否为 SSE（辅助测试）。
func HasStream(respBody string) bool { return strings.Contains(respBody, "data: [DONE]") }
