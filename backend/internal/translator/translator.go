package translator

import "ai-router-gateway/internal/models"

// Translator 把 OpenAI Chat Completions 协议与上游服务商原生协议互转。
// 请求方向：TranslateRequest 把 OpenAI 形态请求翻译为上游原生请求体，并返回需要附加的请求头。
// 响应方向：TranslateResponse 把上游非流式响应体翻译回 OpenAI ChatResponse。
// 流式方向：NewStream 返回一个有状态的流式翻译器，逐块把上游 SSE 事件翻译为 OpenAI 流式 chunk。
type Translator interface {
	TranslateRequest(req *models.ChatRequest) (any, map[string]string, error)
	TranslateResponse(resp any) (*models.ChatResponse, error)
	// NewStream 返回该上游协议专用的流式翻译器（带状态机）。
	// 返回 nil 表示上游流式格式与 OpenAI 兼容，引擎将走默认的 OpenAI chunk 解析。
	NewStream() StreamTranslator
}

// StreamTranslator 把上游的单个 SSE 事件（已解析为 JSON map）翻译为 OpenAI 兼容的 StreamChunk。
// 返回 (nil, nil) 表示该事件应被忽略（如 ping / message_start / message_stop），不向客户端下发。
type StreamTranslator interface {
	TranslateChunk(chunk any) (*models.StreamChunk, error)
}

var registry = map[string]Translator{}

func GetTranslator(apiType string) Translator {
	return registry[apiType]
}

func RegisterTranslator(apiType string, t Translator) {
	registry[apiType] = t
}

// init 在包加载时注册所有内置 translator，激活翻译层。
// api_type 取值与 internal/db/seed.go 中预置服务商保持一致：
//   - "anthropic" / "claude"  -> ClaudeTranslator（Anthropic Messages 协议）
//   - "gemini"                -> GeminiTranslator（Gemini Generative Language 协议）
//   - "openai"                -> OpenAITranslator（OpenAI 原生，透传）
//
// 其余 api_type（deepseek / qwen / groq / ...）均为 OpenAI 兼容，无需 translator，
// 引擎会走默认透传路径。
func init() {
	RegisterTranslator("anthropic", &ClaudeTranslator{})
	RegisterTranslator("claude", &ClaudeTranslator{})
	RegisterTranslator("gemini", &GeminiTranslator{})
	RegisterTranslator("openai", &OpenAITranslator{})
}
