package translator

import (
	"encoding/json"
	"fmt"

	"ai-router-gateway/internal/models"
)

// 本文件实现 Gemini translator 的「逆向」映射，用于原生 Gemini generateContent 入口：
//   - ParseGeminiRequest：Gemini 请求体（contents）-> OpenAI ChatRequest（model 来自 URL 路径）
//   - SerializeGeminiResponse：OpenAI ChatResponse -> Gemini GenerateContentResponse
//   - geminiStreamEmitter：OpenAI StreamChunk -> Gemini 流式 GenerateContentResponse 序列
//
// 与 translator.go 的正向映射（OpenAI->Gemini）互为逆操作，字段名完全对齐。

// ParseGeminiRequest 把 Gemini 请求体解析为内部 ChatRequest。
// model 来自 URL 路径（Gemini 原生请求体不含 model 字段），由 handler 传入。
func ParseGeminiRequest(body []byte, model string) (*models.ChatRequest, error) {
	var gr geminiRequest
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, fmt.Errorf("解析 Gemini 请求失败：%w", err)
	}
	if model == "" {
		return nil, fmt.Errorf("model 为必填字段（来自 URL 路径）")
	}

	req := &models.ChatRequest{
		Model:     model,
		MaxTokens: gr.GenerationConfig.MaxOutputTokens,
		Tools:     nil,
	}

	if gr.SystemInstruction != nil {
		var texts []string
		for _, p := range gr.SystemInstruction.Parts {
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) > 0 {
			req.Messages = append(req.Messages, models.Message{Role: "system", Content: models.MessageContent(joinText(texts))})
		}
	}

	for _, c := range gr.Contents {
		role := c.Role
		switch role {
		case "model":
			role = "assistant"
		case "user", "assistant", "tool":
		default:
			role = "user"
		}
		msg, err := geminiContentToMessage(role, c)
		if err != nil {
			return nil, err
		}
		req.Messages = append(req.Messages, msg)
	}

	for _, tool := range gr.Tools {
		for _, fd := range tool.FunctionDeclarations {
			req.Tools = append(req.Tools, models.Tool{
				Type: "function",
				Function: models.Function{
					Name:        fd.Name,
					Description: fd.Description,
					Parameters:  fd.Parameters,
				},
			})
		}
	}
	return req, nil
}

// geminiContentToMessage 把一条 Gemini content 转换为内部 Message。
func geminiContentToMessage(role string, c geminiContent) (models.Message, error) {
	msg := models.Message{Role: role}
	var texts []string
	multimodal := false

	for _, p := range c.Parts {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
		if p.InlineData != nil && p.InlineData.Data != "" {
			multimodal = true
			mime := p.InlineData.MimeType
			if mime == "" {
				mime = "application/octet-stream"
			}
			url := "data:" + mime + ";base64," + p.InlineData.Data
			msg.Content = appendMultimodal(msg.Content, "image_url", url)
		}
		if p.FunctionCall != nil {
			args, _ := json.Marshal(p.FunctionCall.Args)
			if err := appendToolCall(&msg, "call_"+p.FunctionCall.Name, p.FunctionCall.Name, args); err != nil {
				return models.Message{}, err
			}
		}
		if p.FunctionResponse != nil {
			// Gemini functionResponse -> OpenAI tool 消息
			resp := funcResponseToString(p.FunctionResponse.Response)
			return models.Message{Role: "tool", ToolCallID: p.FunctionResponse.Name, Content: models.MessageContent(resp)}, nil
		}
	}

	if multimodal {
		return msg, nil
	}
	msg.Content = models.MessageContent(joinText(texts))
	return msg, nil
}

func funcResponseToString(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	if v, ok := resp["result"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return ""
	}
	return string(b)
}

// SerializeGeminiResponse 把内部 ChatResponse 还原为 Gemini GenerateContentResponse。
func SerializeGeminiResponse(resp *models.ChatResponse) (any, error) {
	var choice models.Choice
	if len(resp.Choices) > 0 {
		choice = resp.Choices[0]
	}
	var parts []geminiPart

	if choice.Message != nil {
		if text := choice.Message.Content.String(); text != "" {
			parts = append(parts, geminiPart{Text: text})
		}
		if len(choice.Message.ToolCalls) > 0 {
			var calls []openAIToolCall
			if err := json.Unmarshal(choice.Message.ToolCalls, &calls); err != nil {
				return nil, fmt.Errorf("解析 tool_calls 失败：%w", err)
			}
			for _, c := range calls {
				args := decodeToolArgsAny(json.RawMessage(c.Function.Arguments))
				parts = append(parts, geminiPart{FunctionCall: &geminiFuncCall{Name: c.Function.Name, Args: args}})
			}
		}
	}

	out := geminiResponse{
		Candidates: []geminiCandidate{{
			Content:      geminiContent{Role: "model", Parts: parts},
			FinishReason: reverseMapGeminiFinish(choice.FinishReason),
		}},
	}
	if resp.Usage.TotalTokens != 0 || resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0 {
		out.UsageMetadata = &geminiUsageMeta{
			PromptTokenCount:     resp.Usage.PromptTokens,
			CandidatesTokenCount: resp.Usage.CompletionTokens,
			TotalTokenCount:      resp.Usage.TotalTokens,
		}
	}
	return out, nil
}

func reverseMapGeminiFinish(openai string) string {
	switch openai {
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	case "tool_calls":
		return "STOP"
	default:
		return "STOP"
	}
}

// ---------- 流式发射器（OpenAI StreamChunk -> Gemini 流式 GenerateContentResponse） ----------

type geminiStreamEmitter struct {
	toolArgs map[int]string // OpenAI tool index -> 累积的参数 JSON 字符串
	toolIDs  map[int]string
	toolNames map[int]string
}

func newGeminiStreamEmitter() *geminiStreamEmitter {
	return &geminiStreamEmitter{
		toolArgs:  make(map[int]string),
		toolIDs:   make(map[int]string),
		toolNames: make(map[int]string),
	}
}

// NewGeminiStreamEmitter 返回一个带状态的 Gemini 流式发射器。
func NewGeminiStreamEmitter() *geminiStreamEmitter {
	return newGeminiStreamEmitter()
}

// Emit 把单个 OpenAI StreamChunk 翻译为 0..N 个 Gemini GenerateContentResponse。
// 每个 Gemini 流式分片是「自包含」的响应，因此文本用增量下发，工具调用在结束分片携带。
func (e *geminiStreamEmitter) Emit(chunk *models.StreamChunk) []StreamEvent {
	if chunk == nil || len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]
	delta := choice.Delta
	if delta == nil {
		delta = &models.Delta{}
	}
	var out []StreamEvent

	// 累积工具调用（参数按字符串拼接，结束分片再解析为对象，避免被 SSE 切散）
	for _, tc := range delta.ToolCalls {
		if tc.ID != "" {
			e.toolIDs[tc.Index] = stripCallPrefix(tc.ID)
		}
		if tc.Function.Name != "" {
			e.toolNames[tc.Index] = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			e.toolArgs[tc.Index] += tc.Function.Arguments
		}
	}

	// 文本增量（Gemini 流式每个分片自包含，仅输出 data: 行，无 event: 前缀）
	if delta.Content != "" {
		out = append(out, StreamEvent{
			Type: "",
			Data: geminiResponse{
				Candidates: []geminiCandidate{{
					Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: delta.Content}}},
				}},
			},
		})
	}

	// 结束分片：携带 finish_reason + usage，以及尚未下发的工具调用
	isFinish := choice.FinishReason != "" ||
		(chunk.Usage.TotalTokens != 0 && chunk.Usage.PromptTokens != 0)
	if isFinish {
		var parts []geminiPart
		if delta.Content != "" {
			parts = append(parts, geminiPart{Text: delta.Content})
		}
		for idx := range e.toolNames {
			name := e.toolNames[idx]
			args := decodeToolArgsAny(json.RawMessage(e.toolArgs[idx]))
			parts = append(parts, geminiPart{FunctionCall: &geminiFuncCall{Name: name, Args: args}})
		}
		final := geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      geminiContent{Role: "model", Parts: parts},
				FinishReason: reverseMapGeminiFinish(choice.FinishReason),
			}},
		}
		if chunk.Usage.TotalTokens != 0 {
			final.UsageMetadata = &geminiUsageMeta{
				PromptTokenCount:     chunk.Usage.PromptTokens,
				CandidatesTokenCount: chunk.Usage.CompletionTokens,
				TotalTokenCount:      chunk.Usage.TotalTokens,
			}
		}
		out = append(out, StreamEvent{Type: "", Data: final})
	}
	return out
}

func stripCallPrefix(id string) string {
	const p = "call_"
	if len(id) > len(p) && id[:len(p)] == p {
		return id[len(p):]
	}
	return id
}
