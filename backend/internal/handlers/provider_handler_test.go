package handlers

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestIsModelLoadingError(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, false}, // 正常响应
		{`{"error":{"message":"Model is loading, please retry later"}}`, true},   // 英文 loading
		{`{"error":"模型加载中，请稍后重试"}`, true},                                        // 中文加载中
		{`{"error":"model is being loaded"}`, true},
		{`{"error":"the model is not ready"}`, true},
		{`{"error":"bad gateway"}`, false}, // 错误但非加载类
		{`{"usage":{}}`, false},            // 无 error 字段
	}
	for _, c := range cases {
		if got := isModelLoadingError(c.body); got != c.want {
			t.Errorf("isModelLoadingError(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

// TestTestModelConnectivity_RetryLoadingThenOK 验证：首次返回「加载中」后重试成功。
func TestTestModelConnectivity_RetryLoadingThenOK(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"error":"model is loading"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	h := &ProviderHandler{}
	// 用 httptest server 的 URL 直接作为 base，resolveChatURL 会补 /chat/completions
	ok, msg := h.testModelConnectivity(srv.URL, "sk-test", "m1")
	if !ok {
		t.Fatalf("期望重试后成功，实际失败: %s", msg)
	}
	if got := atomic.LoadInt32(&hits); got < 2 {
		t.Fatalf("期望至少 2 次请求（含 1 次重试），实际 %d", got)
	}
}

// TestTestModelConnectivity_Retry429ThenOK 验证：首次 429 后重试成功。
func TestTestModelConnectivity_Retry429ThenOK(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	h := &ProviderHandler{}
	ok, msg := h.testModelConnectivity(srv.URL, "sk-test", "m1")
	if !ok {
		t.Fatalf("期望 429 重试后成功，实际失败: %s", msg)
	}
	if got := atomic.LoadInt32(&hits); got < 2 {
		t.Fatalf("期望 429 触发重试，实际请求 %d 次", got)
	}
}

// TestTestModelConnectivity_AuthErrorNoRetry 验证：401 等持久错误不重试。
func TestTestModelConnectivity_AuthErrorNoRetry(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	h := &ProviderHandler{}
	ok, _ := h.testModelConnectivity(srv.URL, "bad", "m1")
	if ok {
		t.Fatal("401 不应判成功")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("401 不应重试，实际请求 %d 次", got)
	}
}
