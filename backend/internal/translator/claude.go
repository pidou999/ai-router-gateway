package translator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ai-router-gateway/internal/models"
)

// ClaudeTranslator 在 OpenAI Chat Completions 协议与 Anthropic Messages 协议之间互转。
//
// 关键能力（对齐 9Router 的协议适配经验）：
//   - 多模态：OpenAI image_url -> Anthropic image content block（data: URL 解码为 base64，http(s) URL 用 url source）
//   - 工具调用：OpenAI assistant.tool_calls -> Anthropic tool_use content block；tool 消息 -> tool_result
//   - 思考流：Anthropic thinking content block -> OpenAI reasoning_content
//   - 流式：带状态机的流式翻译器，text/thinking/tool_use 增量都正确翻译为 OpenAI chunk，
//     且 tool_use 的 arguments JSON 分片会被正确拼接下发（避免被 SSE 切散）
type ClaudeTranslator struct{}

// ---------- 请求结构 ----------

type claudeImageSource struct {
	Type      string `json:"type"` // "base64" | "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type claudeContentBlock struct {
	Type      string             `json:"type"` // text | image | tool_use | tool_result | thinking
	Text      string             `json:"text,omitempty"`
	Source    *claudeImageSource `json:"source,omitempty"`
	ID        string             `json:"id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Input     json.RawMessage    `json:"input,omitempty"`
	ToolUseID string             `json:"tool_use_id,omitempty"`
	Content   any                `json:"content,omitempty"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string | []claudeContentBlock
}

type claudeTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
	Tools     []claudeTool    `json:"tools,omitempty"`
	Stream    bool            `json:"stream,omitempty"`
}

// ---------- 响应结构 ----------

type claudeRespContent struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type claudeResponse struct {
	ID         string             `json:"id"`
	Model      string             `json:"model"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []claudeRespContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      claudeUsage        `json:"usage"`
}

// ---------- OpenAI 工具调用构建（用于把上游 tool_use 转回 OpenAI 形状） ----------

type openAIToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function openAIToolCallFn `json:"function"`
}

type openAIToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ---------- 请求翻译 ----------

func (t *ClaudeTranslator) TranslateRequest(req *models.ChatRequest) (any, map[string]string, error) {
	msgs, err := t.buildMessages(req)
	if err != nil {
		return nil, nil, err
	}

	cr := claudeRequest{
		Model:     req.Model,
		MaxTokens: req.GetMaxTokens(4096), // Anthropic 要求 max_tokens 必填且 > 0
		Stream:    req.Stream,
		Messages:  msgs,
	}

	// 抽取 system 消息（仅取第一个）
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			cr.System = msg.Content.String()
			break
		}
	}

	for _, tool := range req.Tools {
		cr.Tools = append(cr.Tools, claudeTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}

	headers := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	return cr, headers, nil
}

func (t *ClaudeTranslator) buildMessages(req *models.ChatRequest) ([]claudeMessage, error) {
	var out []claudeMessage
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			// 顶层 system 单独处理
			continue
		case "tool":
			// OpenAI tool 消息 -> Anthropic user 消息 + tool_result 内容块
			out = append(out, claudeMessage{
				Role: "user",
				Content: []claudeContentBlock{{
					Type:      "tool_result",
					ToolUseID: msg.ToolCallID,
					Content:   msg.Content.String(),
				}},
			})
		case "assistant":
			blocks, err := t.assistantBlocks(msg)
			if err != nil {
				return nil, err
			}
			out = append(out, claudeMessage{Role: "assistant", Content: blocks})
		default:
			blocks, err := claudeContentBlocksFrom(msg.Content)
			if err != nil {
				return nil, err
			}
			if len(blocks) == 1 && blocks[0].Type == "text" {
				out = append(out, claudeMessage{Role: msg.Role, Content: blocks[0].Text})
			} else {
				out = append(out, claudeMessage{Role: msg.Role, Content: blocks})
			}
		}
	}
	return out, nil
}

func (t *ClaudeTranslator) assistantBlocks(msg models.Message) ([]claudeContentBlock, error) {
	var blocks []claudeContentBlock
	if text := msg.Content.String(); text != "" {
		blocks = append(blocks, claudeContentBlock{Type: "text", Text: text})
	}
		if len(msg.ToolCalls) > 0 {
			var calls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal(msg.ToolCalls, &calls); err != nil {
				return nil, fmt.Errorf("解析 assistant tool_calls 失败：%w", err)
			}
			for _, c := range calls {
				blocks = append(blocks, claudeContentBlock{
					Type:  "tool_use",
					ID:    c.ID,
					Name:  c.Function.Name,
					Input: decodeToolArgs(c.Function.Arguments),
				})
			}
		}
	if len(blocks) == 0 {
		blocks = append(blocks, claudeContentBlock{Type: "text", Text: ""})
	}
	return blocks, nil
}

func claudeContentBlocksFrom(content models.MessageContent) ([]claudeContentBlock, error) {
	s := string(content)
	if s == "" {
		return nil, nil
	}
	if s[0] != '[' {
		return []claudeContentBlock{{Type: "text", Text: s}}, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal([]byte(s), &parts); err != nil {
		return []claudeContentBlock{{Type: "text", Text: ""}}, nil
	}
	var blocks []claudeContentBlock
	for _, p := range parts {
		switch {
		case p.Type == "text" && p.Text != "":
			blocks = append(blocks, claudeContentBlock{Type: "text", Text: p.Text})
		case p.ImageURL != nil && p.ImageURL.URL != "":
			if src := claudeImageSourceFromURL(p.ImageURL.URL); src != nil {
				blocks = append(blocks, claudeContentBlock{Type: "image", Source: src})
			}
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, claudeContentBlock{Type: "text", Text: ""})
	}
	return blocks, nil
}

func claudeImageSourceFromURL(url string) *claudeImageSource {
	if strings.HasPrefix(url, "data:") {
		comma := strings.Index(url, ",")
		if comma < 0 {
			return nil
		}
		meta := url[5:comma] // e.g. image/png;base64
		data := url[comma+1:]
		parts := strings.SplitN(meta, ";", 2)
		mediaType := parts[0]
		if mediaType == "base64" {
			mediaType = "application/octet-stream"
		}
		return &claudeImageSource{Type: "base64", MediaType: mediaType, Data: data}
	}
	return &claudeImageSource{Type: "url", URL: url}
}

// decodeToolArgs 把 OpenAI tool_calls[].function.arguments（JSON 字符串）解码为
// Anthropic tool_use.input 需要的 JSON 对象。若上游已直接给出对象形态则原样返回。
func decodeToolArgs(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var obj any
		if err2 := json.Unmarshal([]byte(s), &obj); err2 == nil {
			if b, err3 := json.Marshal(obj); err3 == nil {
				return json.RawMessage(b)
			}
		}
		return json.RawMessage(s)
	}
	if json.Valid(raw) {
		return raw
	}
	return json.RawMessage("{}")
}

// ---------- 非流式响应翻译 ----------

func (t *ClaudeTranslator) TranslateResponse(resp any) (*models.ChatResponse, error) {
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal claude response: %w", err)
	}
	var cr claudeResponse
	if err := json.Unmarshal(b, &cr); err != nil {
		return nil, fmt.Errorf("unmarshal claude response: %w", err)
	}

	var text, reasoning string
	var toolCalls []openAIToolCall
	for _, c := range cr.Content {
		switch c.Type {
		case "text":
			text += c.Text
		case "thinking":
			reasoning += c.Thinking
		case "tool_use":
			args := c.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			toolCalls = append(toolCalls, openAIToolCall{
				ID:       c.ID,
				Type:     "function",
				Function: openAIToolCallFn{Name: c.Name, Arguments: string(args)},
			})
		}
	}

	msg := &models.Message{
		Role:    "assistant",
		Content: models.MessageContent(text),
	}
	if reasoning != "" {
		msg.ReasoningContent = reasoning
	}
	if len(toolCalls) > 0 {
		raw, err := json.Marshal(toolCalls)
		if err != nil {
			return nil, fmt.Errorf("marshal tool_calls: %w", err)
		}
		msg.ToolCalls = raw
	}

	return &models.ChatResponse{
		ID:      cr.ID,
		Object:  "chat.completion",
		Model:   cr.Model,
		Choices: []models.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: mapClaudeStopReason(cr.StopReason),
		}},
		Usage: models.Usage{
			PromptTokens:     cr.Usage.InputTokens,
			CompletionTokens: cr.Usage.OutputTokens,
			TotalTokens:      cr.Usage.InputTokens + cr.Usage.OutputTokens,
		},
	}, nil
}

func mapClaudeStopReason(r string) string {
	switch r {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence", "end_turn":
		return "stop"
	default:
		return "stop"
	}
}

// ---------- 流式翻译（状态机） ----------

func (t *ClaudeTranslator) NewStream() StreamTranslator {
	return &claudeStreamTranslator{blocks: map[int]*claudeStreamBlock{}}
}

type claudeStreamBlock struct {
	index    int
	typ      string // text | thinking | tool_use
	toolID   string
	toolName string
	args     strings.Builder
}

type claudeStreamTranslator struct {
	id          string
	model       string
	blocks      map[int]*claudeStreamBlock
	promptTokens int // 来自 message_start 的 usage.input_tokens，流式结尾的 message_delta 只给 output_tokens
}

func (s *claudeStreamTranslator) TranslateChunk(chunk any) (*models.StreamChunk, error) {
	m, ok := chunk.(map[string]interface{})
	if !ok {
		return nil, nil
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "message_start":
		if msg, ok := m["message"].(map[string]interface{}); ok {
			if v, ok := msg["id"].(string); ok {
				s.id = v
			}
			if v, ok := msg["model"].(string); ok {
				s.model = v
			}
			// Anthropic 在 message_start 的 usage 里给出 input_tokens（prompt token），
			// 而结尾的 message_delta 只给 output_tokens，因此必须在这里暂存。
			if u, ok := msg["usage"].(map[string]interface{}); ok {
				if v, ok := u["input_tokens"].(float64); ok {
					s.promptTokens = int(v)
				}
			}
		}
		return &models.StreamChunk{
			ID:      s.id,
			Object:  "chat.completion.chunk",
			Created: nowUnix(),
			Model:   s.model,
			Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Role: "assistant"}}},
		}, nil

	case "content_block_start":
		idx := intFrom(m["index"])
		cb, _ := m["content_block"].(map[string]interface{})
		btyp, _ := cb["type"].(string)
		if btyp == "tool_use" {
			blk := &claudeStreamBlock{index: idx, typ: btyp}
			if v, ok := cb["id"].(string); ok {
				blk.toolID = v
			}
			if v, ok := cb["name"].(string); ok {
				blk.toolName = v
			}
			s.blocks[idx] = blk
			// 工具调用开始：下发带 id/name 的 tool_calls delta
			return toolCallChunk(s.id, s.model, idx, blk.toolID, blk.toolName, ""), nil
		}
		s.blocks[idx] = &claudeStreamBlock{index: idx, typ: btyp}
		return nil, nil

	case "content_block_delta":
		idx := intFrom(m["index"])
		d, _ := m["delta"].(map[string]interface{})
		dtyp, _ := d["type"].(string)
		switch dtyp {
		case "text_delta":
			text, _ := d["text"].(string)
			return textChunk(s.id, s.model, text), nil
		case "thinking_delta":
			think, _ := d["thinking"].(string)
			return &models.StreamChunk{
				ID:      s.id,
				Object:  "chat.completion.chunk",
				Created: nowUnix(),
				Model:   s.model,
				Choices: []models.Choice{{Index: 0, Delta: &models.Delta{ReasoningContent: think}}},
			}, nil
		case "input_json_delta":
			partial, _ := d["partial_json"].(string)
			if blk := s.blocks[idx]; blk != nil {
				blk.args.WriteString(partial)
			}
			return toolCallChunkArgs(s.id, s.model, idx, partial), nil
		}
		return nil, nil

	case "message_delta":
		delta, _ := m["delta"].(map[string]interface{})
		stopReason, _ := delta["stop_reason"].(string)
		var usage models.Usage
		if u, ok := m["usage"].(map[string]interface{}); ok {
			if v, ok := u["output_tokens"].(float64); ok {
				usage.CompletionTokens = int(v)
			}
		}
		// 合并 message_start 暂存的 input_tokens（prompt token）
		usage.PromptTokens = s.promptTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		return &models.StreamChunk{
			ID:      s.id,
			Object:  "chat.completion.chunk",
			Created: nowUnix(),
			Model:   s.model,
			Choices: []models.Choice{{Index: 0, FinishReason: mapClaudeStopReason(stopReason)}},
			Usage:   usage,
		}, nil

	default:
		// message_stop / ping / error / 其它事件：不下发
		return nil, nil
	}
}

// ---------- 流式构造辅助 ----------

func nowUnix() int64 { return time.Now().Unix() }

func intFrom(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func textChunk(id, model, text string) *models.StreamChunk {
	return &models.StreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: nowUnix(),
		Model:   model,
		Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Content: text}}},
	}
}

func toolCallChunk(id, model string, idx int, toolID, name, args string) *models.StreamChunk {
	tc := models.ToolCallDelta{Index: idx, ID: toolID, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return &models.StreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: nowUnix(),
		Model:   model,
		Choices: []models.Choice{{Index: 0, Delta: &models.Delta{ToolCalls: []models.ToolCallDelta{tc}}}},
	}
}

func toolCallChunkArgs(id, model string, idx int, args string) *models.StreamChunk {
	tc := models.ToolCallDelta{Index: idx}
	tc.Function.Arguments = args
	return &models.StreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: nowUnix(),
		Model:   model,
		Choices: []models.Choice{{Index: 0, Delta: &models.Delta{ToolCalls: []models.ToolCallDelta{tc}}}},
	}
}
