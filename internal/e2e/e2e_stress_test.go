package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestE2EConcurrentRequests 验证并发请求正确性（含流式与非流式混合）。
func TestE2EConcurrentRequests(t *testing.T) {
	env := setup(t)

	const nonStream = 20
	const stream = 10
	var wg sync.WaitGroup
	errCh := make(chan error, nonStream+stream)

	doChat := func(streaming bool, idx int) {
		defer wg.Done()
		body := fmt.Sprintf(`{"model":"test-model","stream":%v,"messages":[{"role":"user","content":"req-%d"}]}`, streaming, idx)
		req, err := http.NewRequest(http.MethodPost, env.httpURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
		if err != nil {
			errCh <- err
			return
		}
		for k, v := range authHeader(env.token) {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- fmt.Errorf("req-%d: %w", idx, err)
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("req-%d status=%d body=%s", idx, resp.StatusCode, data)
			return
		}
		if streaming && !strings.Contains(string(data), "data: [DONE]") {
			errCh <- fmt.Errorf("req-%d missing [DONE]", idx)
		}
		if !streaming && !strings.Contains(string(data), `"choices"`) {
			errCh <- fmt.Errorf("req-%d missing choices", idx)
		}
	}

	for i := 0; i < nonStream; i++ {
		wg.Add(1)
		go doChat(false, i)
	}
	for i := 0; i < stream; i++ {
		wg.Add(1)
		go doChat(true, i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// 全部请求应到达本地 mock。
	if got := env.mock.ChatCalls.Load(); got != nonStream {
		t.Errorf("non-stream chat calls=%d want %d", got, nonStream)
	}
	if got := env.mock.StreamCalls.Load(); got != stream {
		t.Errorf("stream calls=%d want %d", got, stream)
	}
}

// TestE2EBodyTooLarge 验证请求体超限被拒绝（413）。
func TestE2EBodyTooLarge(t *testing.T) {
	env := setup(t)
	big := bytes.Repeat([]byte("x"), 17<<20) // 17 MiB > 16 MiB 上限
	resp, data := env.do(t, http.MethodPost, "/v1/chat/completions", big, authHeader(env.token))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data[:200])
	}
	if !strings.Contains(string(data), "body_too_large") {
		t.Fatalf("missing body_too_large: %s", data[:200])
	}
}

// TestE2EQueueAndCapacity 验证并发上限生效：并发上限为 1 时，请求被排队而非立即全部通过。
func TestE2EQueueAndCapacity(t *testing.T) {
	env := setup(t)
	// 将节点并发上限调为 1，mock 慢流式阻塞住第一个请求，其余请求应排队。
	env.mock.SlowStream.Store(true)

	n := env.relaySrv.Registry().Get("node-1")
	if n == nil {
		t.Fatal("node-1 missing")
	}
	n.SetMaxConcurrency(1)

	// 第一个慢流式请求占住唯一并发槽。
	body := `{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hold"}]}`
	req, _ := http.NewRequest(http.MethodPost, env.httpURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
	for k, v := range authHeader(env.token) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("hold req: %v", err)
	}
	defer resp.Body.Close()
	// 读取首个事件确认已建立。
	buf := make([]byte, 256)
	for {
		nr, rerr := resp.Body.Read(buf)
		if nr > 0 && bytes.Contains(buf[:nr], []byte("first")) {
			break
		}
		if rerr != nil {
			t.Fatalf("hold read: %v", rerr)
		}
	}

	// 第二个请求应排队等待（不立即返回），随后取消验证释放。
	body2 := `{"model":"test-model","messages":[{"role":"user","content":"queued"}]}`
	req2, _ := http.NewRequest(http.MethodPost, env.httpURL+"/v1/chat/completions", bytes.NewReader([]byte(body2)))
	for k, v := range authHeader(env.token) {
		req2.Header.Set(k, v)
	}
	done := make(chan int, 1)
	go func() {
		r2, err := http.DefaultClient.Do(req2)
		if err != nil {
			done <- -1
			return
		}
		r2.Body.Close()
		done <- r2.StatusCode
	}()
	select {
	case <-done:
		t.Fatal("second request should be queued, but returned immediately")
	case <-time.After(500 * time.Millisecond):
		// 预期：仍在排队。
	}

	// 释放第一个请求 → 排队请求应完成。
	_ = resp.Body.Close()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("queued request status=%d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("queued request did not complete after slot released")
	}
}
