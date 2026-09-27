package router

import (
	"encoding/json"
	"strings"
	"testing"

	"ai-router-gateway/internal/models"
)

func mustRequest(t *testing.T, body string) *models.ChatRequest {
	t.Helper()
	var req models.ChatRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return &req
}

func TestClassifyIntentVision(t *testing.T) {
	req := mustRequest(t, `{
		"model":"my-combo",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"这张图里有什么？"},
			{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}
		]}]
	}`)
	if got := ClassifyIntent(req); got != CapVision {
		t.Fatalf("intent = %q, want %q", got, CapVision)
	}
}

func TestClassifyIntentAudio(t *testing.T) {
	req := mustRequest(t, `{
		"model":"my-combo",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"听听这段"},
			{"type":"input_audio","input_audio":{"data":"xxx","format":"wav"}}
		]}]
	}`)
	if got := ClassifyIntent(req); got != CapAudio {
		t.Fatalf("intent = %q, want %q", got, CapAudio)
	}
}

func TestClassifyIntentCode(t *testing.T) {
	cases := []string{
		"帮我写一个函数，把字符串反转",
		"这段代码报错了，帮我看看",
		"```go\nfunc main() {}\n```\n为什么编译不过",
		"refactor this python class to use dependency injection",
	}
	for _, text := range cases {
		body, _ := json.Marshal(map[string]any{
			"model":    "my-combo",
			"messages": []map[string]string{{"role": "user", "content": text}},
		})
		req := mustRequest(t, string(body))
		if got := ClassifyIntent(req); got != CapCode {
			t.Fatalf("intent for %q = %q, want %q", text, got, CapCode)
		}
	}
}

func TestClassifyIntentPlainText(t *testing.T) {
	cases := []string{
		"你好，今天天气怎么样？",
		"帮我写一首关于春天的诗",
		"把这段话翻译成英文：我很好",
	}
	for _, text := range cases {
		body, _ := json.Marshal(map[string]any{
			"model":    "my-combo",
			"messages": []map[string]string{{"role": "user", "content": text}},
		})
		req := mustRequest(t, string(body))
		if got := ClassifyIntent(req); got != CapText {
			t.Fatalf("intent for %q = %q, want %q", text, got, CapText)
		}
	}
}

func TestClassifyIntentLongContext(t *testing.T) {
	long := strings.Repeat("这是一段很长的背景资料。", 6000) // 远超 longContextThreshold
	body, _ := json.Marshal(map[string]any{
		"model":    "my-combo",
		"messages": []map[string]string{{"role": "user", "content": long}},
	})
	req := mustRequest(t, string(body))
	if got := ClassifyIntent(req); got != CapLongContext {
		t.Fatalf("intent = %q, want %q", got, CapLongContext)
	}
}

func TestInferCapabilities(t *testing.T) {
	cases := []struct {
		model string
		want  Capability
	}{
		{"Qwen/Qwen2.5-VL-72B-Instruct", CapVision},
		{"gpt-4o", CapVision},
		{"Qwen/Qwen3-Coder-480B", CapCode},
		{"deepseek-ai/DeepSeek-R1", CapReasoning},
		{"moonshot-v1-128k", CapLongContext},
	}
	for _, tc := range cases {
		caps := InferCapabilities(tc.model)
		if !hasCapability(caps, tc.want) {
			t.Fatalf("InferCapabilities(%q) = %v, want to contain %q", tc.model, caps, tc.want)
		}
		if !hasCapability(caps, CapText) {
			t.Fatalf("InferCapabilities(%q) missing baseline %q", tc.model, CapText)
		}
	}
	// 普通文本模型不应被误判为视觉/代码
	plain := InferCapabilities("deepseek-ai/DeepSeek-V3")
	if hasCapability(plain, CapVision) || hasCapability(plain, CapCode) {
		t.Fatalf("DeepSeek-V3 misclassified: %v", plain)
	}
}

func TestSelectByIntentPicksMatchingModel(t *testing.T) {
	items := []comboModelItem{
		{ID: "deepseek-ai/DeepSeek-V3", ProviderID: 1},          // 文本
		{ID: "Qwen/Qwen3-Coder-480B", ProviderID: 2},            // 代码
		{ID: "Qwen/Qwen2.5-VL-72B-Instruct", ProviderID: 3},     // 视觉
	}

	if got := SelectByIntent(items, []Capability{CapVision})[0].ID; got != "Qwen/Qwen2.5-VL-72B-Instruct" {
		t.Fatalf("vision intent picked %q", got)
	}
	if got := SelectByIntent(items, []Capability{CapCode})[0].ID; got != "Qwen/Qwen3-Coder-480B" {
		t.Fatalf("code intent picked %q", got)
	}
	if got := SelectByIntent(items, []Capability{CapText})[0].ID; got != "deepseek-ai/DeepSeek-V3" {
		t.Fatalf("text intent picked %q", got)
	}
	// 所有模型都必须保留在候选中（未匹配的作为兜底）
	if len(SelectByIntent(items, []Capability{CapVision})) != len(items) {
		t.Fatal("SelectByIntent dropped candidates")
	}
}

func TestSelectByIntentManualCapabilityWins(t *testing.T) {
	items := []comboModelItem{
		{ID: "some-random-model-a", ProviderID: 1},
		{ID: "some-random-model-b", ProviderID: 2, Capability: "vision"},
	}
	if got := SelectByIntent(items, []Capability{CapVision})[0].ID; got != "some-random-model-b" {
		t.Fatalf("manual capability ignored, picked %q", got)
	}
}
