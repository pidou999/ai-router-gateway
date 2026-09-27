package translator

import (
	"encoding/json"
	"fmt"
	"strings"

	"ai-router-gateway/internal/models"
)

// GeminiTranslator 在 OpenAI Chat Completions 协议与 Google Gemini Generative Language
// 协议之间互转。
//
// 关键能力：
//   - 多模态：OpenAI image_url -> Gemini inline_data（base64；远程 URL 不支持，忽略）
//   - 工具调用：OpenAI tools -> functionDeclarations；Gemini functionCall -> OpenAI tool_calls
//   - 流式：每个 SSE 分片是一个完整 GenerateContentResponse，逐块翻译为 OpenAI delta
//     （文本 / functionCall / finish_reason + usage）
type GeminiTranslator struct{}

// ---------- 请求结构 ----------

type geminiInline struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiFuncCall struct {
	Name string `json:"name"`
	Args any    `json:"args"`
}

type geminiFuncResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiPart struct {
	Text           string          `json:"text,omitempty"`
	InlineData     *geminiInline   `json:"inline_data,omitempty"`
	FunctionCall   *geminiFuncCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResp `json:"functionResponse,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiFuncDecl struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiGenConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	TopP            float64 `json:"topP,omitempty"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	Tools             []geminiTool    `json:"tools,omitempty"`
	GenerationConfig  geminiGenConfig `json:"generationConfig,omitempty"`
}

// ---------- 响应结构 ----------

type geminiUsageMeta struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsageMeta  `json:"usageMetadata,omitempty"`
}

// ---------- 请求翻译 ----------

func (t *GeminiTranslator) TranslateRequest(req *models.ChatRequest) (any, map[string]string, error) {
	gr := geminiRequest{
		GenerationConfig: geminiGenConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
			TopP:            req.TopP,
		},
	}

	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			gr.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: msg.Content.String()}},
			}
		case "tool":
			gr.Contents = append(gr.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFuncResp{
						Name:     msg.ToolCallID,
						Response: map[string]any{"result": msg.Content.String()},
					},
				}},
			})
		case "assistant":
			parts, err := t.assistantParts(msg)
			if err != nil {
				return nil, nil, err
			}
			role := "model"
			gr.Contents = append(gr.Contents, geminiContent{Role: role, Parts: parts})
		default:
			parts, err := geminiPartsFrom(msg.Content)
			if err != nil {
				return nil, nil, err
			}
			role := msg.Role
			if role == "assistant" {
				role = "model"
			}
			gr.Contents = append(gr.Contents, geminiContent{Role: role, Parts: parts})
		}
	}

	for _, tool := range req.Tools {
		gr.Tools = append(gr.Tools, geminiTool{
			FunctionDeclarations: []geminiFuncDecl{{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			}},
		})
	}

	return gr, nil, nil
}

func (t *GeminiTranslator) assistantParts(msg models.Message) ([]geminiPart, error) {
	var parts []geminiPart
	if text := msg.Content.String(); text != "" {
		parts = append(parts, geminiPart{Text: text})
	}
	if len(msg.ToolCalls) > 0 {
		var calls []struct {
			Function struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(msg.ToolCalls, &calls); err != nil {
			return nil, fmt.Errorf("解析 assistant tool_calls 失败：%w", err)
		}
		for _, c := range calls {
			parts = append(parts, geminiPart{
				FunctionCall: &geminiFuncCall{Name: c.Function.Name, Args: decodeToolArgsAny(c.Function.Arguments)},
			})
		}
	}
	return parts, nil
}

func geminiPartsFrom(content models.MessageContent) ([]geminiPart, error) {
	s := string(content)
	if s == "" {
		return nil, nil
	}
	if s[0] != '[' {
		return []geminiPart{{Text: s}}, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal([]byte(s), &parts); err != nil {
		return []geminiPart{{Text: ""}}, nil
	}
	var out []geminiPart
	for _, p := range parts {
		switch {
		case p.Type == "text" && p.Text != "":
			out = append(out, geminiPart{Text: p.Text})
		case p.ImageURL != nil && p.ImageURL.URL != "":
			if src := geminiInlineFromURL(p.ImageURL.URL); src != nil {
				out = append(out, geminiPart{InlineData: src})
			}
		}
	}
	if len(out) == 0 {
		out = append(out, geminiPart{Text: ""})
	}
	return out, nil
}

func geminiInlineFromURL(url string) *geminiInline {
	if strings.HasPrefix(url, "data:") {
		comma := strings.Index(url, ",")
		if comma < 0 {
			return nil
		}
		meta := url[5:comma]
		data := url[comma+1:]
		parts := strings.SplitN(meta, ";", 2)
		mime := parts[0]
		if mime == "base64" {
			mime = "application/octet-stream"
		}
		return &geminiInline{MimeType: mime, Data: data}
	}
	// Gemini 仅支持 base64 inline_data，远程 URL 忽略
	return nil
}

// decodeToolArgsAny 把 OpenAI tool_calls[].function.arguments（JSON 字符串）解码为
// Gemini functionCall.args 需要的 JSON 对象（any）。若上游已直接给出对象形态则原样返回。
func decodeToolArgsAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var obj any
		if err2 := json.Unmarshal([]byte(s), &obj); err2 == nil {
			return obj
		}
		return s
	}
	var obj any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj
	}
	return map[string]any{}
}

// ---------- 非流式响应翻译 ----------

func (t *GeminiTranslator) TranslateResponse(resp any) (*models.ChatResponse, error) {
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini response: %w", err)
	}
	var gr geminiResponse
	if err := json.Unmarshal(b, &gr); err != nil {
		return nil, fmt.Errorf("unmarshal gemini response: %w", err)
	}
	if len(gr.Candidates) == 0 {
		return nil, fmt.Errorf("gemini 响应缺少 candidates")
	}

	candidate := gr.Candidates[0]
	var text string
	var toolCalls []openAIToolCall
	for _, p := range candidate.Content.Parts {
		if p.Text != "" {
			text += p.Text
		}
		if p.FunctionCall != nil {
			args, _ := json.Marshal(p.FunctionCall.Args)
			toolCalls = append(toolCalls, openAIToolCall{
				ID:       "call_" + p.FunctionCall.Name,
				Type:     "function",
				Function: openAIToolCallFn{Name: p.FunctionCall.Name, Arguments: string(args)},
			})
		}
	}

	msg := &models.Message{Role: "assistant", Content: models.MessageContent(text)}
	if len(toolCalls) > 0 {
		raw, err := json.Marshal(toolCalls)
		if err != nil {
			return nil, fmt.Errorf("marshal tool_calls: %w", err)
		}
		msg.ToolCalls = raw
	}

	cr := &models.ChatResponse{
		Object:  "chat.completion",
		Model:   "",
		Choices: []models.Choice{{Index: 0, Message: msg, FinishReason: mapGeminiFinish(candidate.FinishReason)}},
	}
	if gr.UsageMetadata != nil {
		cr.Usage = models.Usage{
			PromptTokens:     gr.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr.UsageMetadata.TotalTokenCount,
		}
	}
	return cr, nil
}

func mapGeminiFinish(r string) string {
	switch r {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "OTHER":
		return "content_filter"
	default:
		return "stop"
	}
}

// ---------- 流式翻译 ----------

func (t *GeminiTranslator) NewStream() StreamTranslator {
	return &geminiStreamTranslator{}
}

type geminiStreamTranslator struct {
	roleEmitted   bool
	finishEmitted bool
	usage         models.Usage
}

func (s *geminiStreamTranslator) TranslateChunk(chunk any) (*models.StreamChunk, error) {
	b, err := json.Marshal(chunk)
	if err != nil {
		return nil, err
	}
	var gr geminiResponse
	if err := json.Unmarshal(b, &gr); err != nil {
		return nil, err
	}
	if len(gr.Candidates) == 0 {
		return nil, nil
	}
	candidate := gr.Candidates[0]

	if gr.UsageMetadata != nil {
		s.usage = models.Usage{
			PromptTokens:     gr.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr.UsageMetadata.TotalTokenCount,
		}
	}

	// 结束分片：下发 finish_reason（+ usage），只下发一次
	if candidate.FinishReason != "" {
		if s.finishEmitted {
			return nil, nil
		}
		s.finishEmitted = true
		return &models.StreamChunk{
			Object:  "chat.completion.chunk",
			Created: nowUnix(),
			Choices: []models.Choice{{Index: 0, FinishReason: mapGeminiFinish(candidate.FinishReason)}},
			Usage:   s.usage,
		}, nil
	}

	delta := &models.Delta{}
	hasContent := false
	for _, p := range candidate.Content.Parts {
		if p.Text != "" {
			delta.Content += p.Text
			hasContent = true
		}
		if p.FunctionCall != nil {
			args, _ := json.Marshal(p.FunctionCall.Args)
			tc := models.ToolCallDelta{Index: 0, ID: "call_" + p.FunctionCall.Name, Type: "function"}
			tc.Function.Name = p.FunctionCall.Name
			tc.Function.Arguments = string(args)
			delta.ToolCalls = append(delta.ToolCalls, tc)
			hasContent = true
		}
	}
	if !hasContent {
		return nil, nil
	}
	if !s.roleEmitted {
		delta.Role = "assistant"
		s.roleEmitted = true
	}
	return &models.StreamChunk{
		Object:  "chat.completion.chunk",
		Created: nowUnix(),
		Choices: []models.Choice{{Index: 0, Delta: delta}},
	}, nil
}
