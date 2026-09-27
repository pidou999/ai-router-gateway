package router

import "testing"

// resolveChatURL 是转发前的端点补全逻辑。历史上它对 anthropic/claude 也追加
// /chat/completions，而载荷早已被 translator 转成 Anthropic Messages 格式，
// 打到真实 Anthropic 必然 404 —— 这里用表驱动锁住各协议的正确端点。
func TestResolveChatURL(t *testing.T) {
	cases := []struct {
		name    string
		apiType string
		baseURL string
		model   string
		stream  bool
		want    string
	}{
		{"openai 只填 base", "openai", "https://api.openai.com/v1", "gpt-4o", false,
			"https://api.openai.com/v1/chat/completions"},
		{"openai 尾部斜杠", "openai", "https://api.openai.com/v1/", "gpt-4o", false,
			"https://api.openai.com/v1/chat/completions"},
		{"openai 已含完整端点", "openai", "https://api.openai.com/v1/chat/completions", "gpt-4o", false,
			"https://api.openai.com/v1/chat/completions"},

		{"claude 只填 base 应补 /messages", "claude", "https://api.anthropic.com/v1", "claude-3-5-sonnet", false,
			"https://api.anthropic.com/v1/messages"},
		{"anthropic 只填 base 应补 /messages", "anthropic", "https://api.anthropic.com/v1", "claude-3-5-sonnet", false,
			"https://api.anthropic.com/v1/messages"},
		{"claude 尾部斜杠", "claude", "https://api.anthropic.com/v1/", "claude-3-5-sonnet", false,
			"https://api.anthropic.com/v1/messages"},
		{"claude 已含 /messages 保持原样", "claude", "https://api.anthropic.com/v1/messages", "claude-3-5-sonnet", false,
			"https://api.anthropic.com/v1/messages"},
		{"claude 走 OpenAI 兼容端点时保持原样", "claude", "https://proxy.example.com/v1/chat/completions", "claude-3-5-sonnet", false,
			"https://proxy.example.com/v1/chat/completions"},

		{"gemini 非流式", "gemini", "https://generativelanguage.googleapis.com/v1beta/models", "gemini-2.0-flash", false,
			"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"},
		{"gemini 流式", "gemini", "https://generativelanguage.googleapis.com/v1beta/models", "gemini-2.0-flash", true,
			"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse"},

		{"带查询参数（Azure）原样返回", "openai", "https://x.openai.azure.com/openai/deployments/d1/chat/completions?api-version=2024-02-01", "gpt-4o", false,
			"https://x.openai.azure.com/openai/deployments/d1/chat/completions?api-version=2024-02-01"},
		{"空 base 原样返回", "openai", "", "gpt-4o", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveChatURL(tc.apiType, tc.baseURL, tc.model, tc.stream)
			if got != tc.want {
				t.Errorf("resolveChatURL(%q, %q, %q, %v)\n got: %s\nwant: %s",
					tc.apiType, tc.baseURL, tc.model, tc.stream, got, tc.want)
			}
		})
	}
}
