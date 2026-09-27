package models

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// MessageContent 兼容 OpenAI content 字段的多种形态：
//   1) 字符串：      "hello"
//   2) parts 数组：  [{"type":"text","text":"hello"}, {"type":"image_url","image_url":{"url":"..."}}]
//   3) null / 其他形态
//
// 关键点（多模态 / 视觉模型兼容）：
//   当 content 是含非文本 part（如 image_url）的数组时，必须【原样保留】原始 JSON，
//   否则图片会在网关被丢弃，上游视觉模型只收到文字、看不到图。
//   纯文本数组仍合并为字符串（向后兼容旧逻辑与 translator）。
//   序列化由 MarshalJSON 决定：数组/对象原样输出，普通文本作为 JSON 字符串输出。
type MessageContent string

// String 返回纯文本内容：多模态场景下仅拼接文本 part，忽略图片/音频，供 translator 与日志使用。
func (mc MessageContent) String() string {
	s := string(mc)
	if len(s) == 0 {
		return ""
	}
	if s[0] == '[' {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(s), &parts); err == nil {
			var sb strings.Builder
			for _, p := range parts {
				if p.Text == "" {
					continue
				}
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(p.Text)
			}
			return sb.String()
		}
	}
	return s
}

// MarshalJSON 决定序列化方式：
//   - 内部为多模态原始 JSON（数组/对象，且合法）→ 原样输出，确保 image_url 被上游视觉模型正确接收；
//   - 否则作为普通 JSON 字符串输出。
func (mc MessageContent) MarshalJSON() ([]byte, error) {
	s := string(mc)
	if len(s) == 0 {
		return []byte(`""`), nil
	}
	if (s[0] == '[' || s[0] == '{') && json.Valid([]byte(s)) {
		return []byte(s), nil
	}
	return json.Marshal(s)
}

func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*mc = ""
		return nil
	}

	switch trimmed[0] {
	case '"': // 形态 1：普通字符串
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*mc = MessageContent(s)
		return nil

	case '[': // 形态 2：parts 数组
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			// 仅用于判断是否为多模态消息（图片/音频等），不取出具体地址
			ImageURL *struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(trimmed, &parts); err != nil {
			// 数组内结构未知：保留原始 JSON，避免整个请求失败
			*mc = MessageContent(trimmed)
			return nil
		}
		// 是否含非文本 part（图片/音频等）？若有，原样保留多模态结构，转发给上游视觉模型
		hasMultimodal := false
		for _, p := range parts {
			if p.Type != "text" || p.ImageURL != nil {
				hasMultimodal = true
				break
			}
		}
		if hasMultimodal {
			*mc = MessageContent(trimmed) // 保留原始多模态 JSON，序列化时原样转发
			return nil
		}
		// 纯文本数组：合并为字符串，向后兼容
		var sb strings.Builder
		for _, p := range parts {
			if p.Text == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
		*mc = MessageContent(sb.String())
		return nil

	default: // 形态 3：对象/数字等未知形态，原样保留而不报错
		*mc = MessageContent(trimmed)
		return nil
	}
}

type Message struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
	// 工具调用相关字段（OpenAI 规范），透传以免丢失上下文
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	// 推理模型的思考内容（DeepSeek / Qwen 等 OpenAI 兼容推理模型在 message 中携带），透传以免丢失
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type Function struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// ToolCallDelta 对应 OpenAI 流式响应中 delta.tool_calls 的增量结构，
// 用于把上游（Claude tool_use / Gemini functionCall）的流式工具调用翻译为 OpenAI 形状。
// 流式分片场景下，首个 delta 携带 index/id/name，后续 delta 仅携带 index + function.arguments。
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	TopP        float64   `json:"top_p,omitempty"`
	Tools       []Tool    `json:"tools,omitempty"`

	// 以下为 OpenAI 兼容的可选字段，本网关不解释其语义，仅原样透传给上游，
	// 避免因客户端携带这些字段而解析失败或丢失能力。
	ToolChoice      json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat  json.RawMessage `json:"response_format,omitempty"`
	Stop            json.RawMessage `json:"stop,omitempty"`
	StreamOptions   json.RawMessage `json:"stream_options,omitempty"`
	N               json.RawMessage `json:"n,omitempty"`
	Seed            json.RawMessage `json:"seed,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

// GetMaxTokens 返回 max_tokens，未设置时给出安全默认值。
// 某些上游（如 Anthropic）要求 max_tokens 必填且大于 0。
func (r *ChatRequest) GetMaxTokens(defaultValue int) int {
	if r.MaxTokens > 0 {
		return r.MaxTokens
	}
	return defaultValue
}

// Choice 同时服务于非流式（message）与流式（delta）响应。
// 这两个字段必须用指针：Go 的 omitempty 对结构体无效，
// 若用值类型会导致流式 chunk 里混入空的 "message":{"role":"","content":""}，
// 违反 OpenAI 规范（chunk 只应包含 delta），部分客户端会因此解析异常。
type Choice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Delta   `json:"delta,omitempty"`
	FinishReason string   `json:"finish_reason,omitempty"`
}

type Delta struct {
	Role            string          `json:"role,omitempty"`
	Content         string          `json:"content,omitempty"`
	// 推理模型的思考流（reasoning_content）。9Router 的 streamHelpers.js 明确把
	// delta.reasoning_content 视为有效内容；本网关此前没有该字段，反序列化时被丢弃，
	// 导致客户端在推理模型流式输出时收不到思考内容、甚至收到空回复。
	ReasoningContent string                   `json:"reasoning_content,omitempty"`
	// ToolCalls 承载流式工具调用的增量（Claude tool_use / Gemini functionCall 翻译而来）。
	ToolCalls       []ToolCallDelta           `json:"tool_calls,omitempty"`
	// Extra 捕获 delta 中所有未知字段（如各厂商私有思考字段：StepFun / Kimi / 等），
	// 序列化时原样回写，确保网关对任何新字段透明透传，避免客户端收到空 delta。
	Extra           map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON 先将原始 JSON 解析为 map 以捕获全部字段，
// 再提取已知字段到结构体成员，剩余字段存入 Extra。
func (d *Delta) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if v, ok := m["role"]; ok {
		_ = json.Unmarshal(v, &d.Role)
	}
	if v, ok := m["content"]; ok {
		_ = json.Unmarshal(v, &d.Content)
	}
	if v, ok := m["reasoning_content"]; ok {
		_ = json.Unmarshal(v, &d.ReasoningContent)
	}
	d.Extra = make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		switch k {
		case "role", "content", "reasoning_content":
			// 已提取到结构体字段，不重复存入 Extra
		default:
			d.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON 将已知字段与 Extra 中的未知字段合并输出，
// 保证客户端收到的 delta 与上游完全一致。
func (d Delta) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage, len(d.Extra)+3)
	if d.Role != "" {
		b, _ := json.Marshal(d.Role)
		m["role"] = b
	}
	if d.Content != "" {
		b, _ := json.Marshal(d.Content)
		m["content"] = b
	}
	if d.ReasoningContent != "" {
		b, _ := json.Marshal(d.ReasoningContent)
		m["reasoning_content"] = b
	}
	if len(d.ToolCalls) > 0 {
		b, _ := json.Marshal(d.ToolCalls)
		m["tool_calls"] = b
	}
	for k, v := range d.Extra {
		m[k] = v
	}
	return json.Marshal(m)
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage,omitempty"`
}

type Provider struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	BaseURL      string    `json:"base_url"`
	APIType      string    `json:"api_type"`
	Enabled      bool      `json:"enabled"`
	Priority     int       `json:"priority"`
	HealthStatus string    `json:"health_status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Account struct {
	ID              int64      `json:"id"`
	ProviderID      int64      `json:"provider_id"`
	UserID          int64      `json:"user_id"`
	Name            string     `json:"name"`
	APIKeyEncrypted string     `json:"api_key_encrypted"`
	// ExtraConfig 存储服务商私有配置（JSON 字符串），如 Cloudflare 的 accountId、Azure 的
	// endpoint/deployment 等。转发前用于替换 Provider.BaseURL 模板中的命名占位符。
	// 对应 9Router 的 credentials.providerSpecificData 思路。
	ExtraConfig     string     `json:"extra_config,omitempty"`
	RateLimitRPM    int        `json:"rate_limit_rpm"`
	RateLimitTPM    int        `json:"rate_limit_tpm"`
	Enabled         bool       `json:"enabled"`
	OAuthConfig     string     `json:"oauth_config,omitempty"` // OAuth PKCE 配置 JSON
	TokenExpiry     *time.Time `json:"token_expiry,omitempty"` // token 过期时间
	CooldownUntil   *time.Time `json:"cooldown_until"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type StreamChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	// Usage 仅在最终 chunk 中携带（OpenAI 规范：usage 出现在 finish chunk）。
	Usage Usage `json:"usage,omitempty"`
}
