package proxy

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TestResult 表示模型连通性测试的结果。
type TestResult struct {
	ModelID     string
	DisplayName string
	OK          bool
	Message     string
}

// TestModelConnectivity 用极简 chat/completions 请求验证模型是否可用。
// 支持重试：超时/429/5xx/"加载中"等可重试错误最多重试 testMaxRetries 次，退避 3s/6s/12s。
func TestModelConnectivity(baseURL, apiKey, modelID, apiType string) (*TestResult, error) {
	chatURL := resolveChatURL(baseURL)
	payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"stream":false}`, modelID)

	if apiType == "opencode" && apiKey == "" {
		apiKey = "public"
	}

	const maxRetries = 3
	var lastMsg string
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 3 * time.Second // 3s, 6s, 12s
			time.Sleep(backoff)
		}

		req, err := http.NewRequest(http.MethodPost, chatURL, strings.NewReader(payload))
		if err != nil {
			return &TestResult{ModelID: modelID, OK: false, Message: "构造请求失败：" + err.Error()}, nil
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if apiType == "opencode" {
			req.Header.Set("x-opencode-client", "desktop")
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			lastMsg = "连接超时（疑似冷启动）：" + err.Error()
			// 超时可重试
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(body)
		if len(bodyStr) > 500 {
			bodyStr = bodyStr[:500]
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if isModelLoadingError(bodyStr) {
				lastMsg = "模型加载中（冷启动）：" + bodyStr
				if attempt < maxRetries {
					continue
				}
				break
			}
			return &TestResult{ModelID: modelID, DisplayName: modelID, OK: true, Message: ""}, nil
		}

		lastMsg = fmt.Sprintf("HTTP %d：%s", resp.StatusCode, bodyStr)

		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < maxRetries {
			continue
		}
		break
	}
	return &TestResult{ModelID: modelID, OK: false, Message: lastMsg}, nil
}

// resolveChatURL 将服务商 base_url 补全为完整的 chat/completions 端点。
func resolveChatURL(baseURL string) string {
	if baseURL == "" {
		return baseURL
	}
	if strings.Contains(baseURL, "?") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/chat/completions") || strings.HasSuffix(baseURL, "/messages") {
		return baseURL
	}
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

// isModelLoadingError 判断响应体是否为 serverless 冷启动导致的「模型加载中」错误。
func isModelLoadingError(body string) bool {
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "error") {
		return false
	}
	for _, kw := range []string{
		"loading", "being loaded", "not ready", "still warming",
		"cold start", "cold-start", "try again", "retry later",
		"稍后", "加载", "预热", "启动中",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
