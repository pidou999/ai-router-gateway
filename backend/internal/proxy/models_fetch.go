package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ModelEntry 表示一个从上游拉取的模型条目。
type ModelEntry struct {
	ModelID     string
	DisplayName string
	OwnedBy     string
}

// FetchOpenAIModels 从 OpenAI 兼容 /models 端点拉取模型列表。
// baseURL 应为已替换模板占位符的基址（不含 /chat/completions），apiKey 为空则不附加鉴权。
func FetchOpenAIModels(baseURL, apiKey string) ([]ModelEntry, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	modelsBase := baseURL
	if strings.HasSuffix(modelsBase, "/chat/completions") {
		modelsBase = modelsBase[:len(modelsBase)-len("/chat/completions")]
	}
	modelsURL := modelsBase + "/models"

	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("状态码 %d：%s", resp.StatusCode, msg)
	}

	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析响应失败：%w", err)
	}

	result := make([]ModelEntry, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		display := m.Name
		if display == "" {
			display = m.ID
		}
		result = append(result, ModelEntry{
			ModelID:     m.ID,
			DisplayName: display,
			OwnedBy:     m.OwnedBy,
		})
	}
	return result, nil
}

// FetchCloudflareModels 从 Cloudflare Workers AI /ai/models/search 端点获取可用模型列表。
// resolvedBase 应为已替换 {accountId} 的 base_url（如 .../accounts/REAL_ID/ai/v1）。
func FetchCloudflareModels(resolvedBase, apiKey string) ([]ModelEntry, error) {
	base := strings.TrimRight(resolvedBase, "/")
	if strings.HasSuffix(base, "/v1") {
		base = base[:len(base)-len("/v1")]
	}
	searchURL := base + "/models/search?task=Text+Generation"

	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("状态码 %d：%s", resp.StatusCode, msg)
	}

	var parsed struct {
		Result []struct {
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析响应失败：%w", err)
	}

	result := make([]ModelEntry, 0, len(parsed.Result))
	for _, m := range parsed.Result {
		if m.Name == "" {
			continue
		}
		result = append(result, ModelEntry{
			ModelID:     m.Name,
			DisplayName: m.Name,
			OwnedBy:     "cloudflare",
		})
	}
	return result, nil
}
