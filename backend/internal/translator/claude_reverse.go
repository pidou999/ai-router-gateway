package translator

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ai-router-gateway/internal/models"
)

// 本文件实现 Claude translator 的「逆向」映射，用于原生 Anthropic `/v1/messages` 入口：
//   - ParseClaudeRequest：Anthropic 请求体 -> OpenAI ChatRequest（喂给路由引擎）
//   - SerializeClaudeResponse：OpenAI ChatResponse -> Anthropic 响应体
//   - claudeStreamEmitter：OpenAI StreamChunk -> Anthropic SSE 事件序列
//
// 与 translator.go 的正向映射（OpenAI->Claude）互为逆操作，字段名完全对齐。
// 这样网关即可作为 Anthropic SDK / Claude Code 的 drop-in 中转。

// ParseClaudeRequest 把 Anthropic Messages 请求体解析为内部 ChatRequest。
func ParseClaudeRequest(body []byte) (*models.ChatRequest, error) {
	var cr claudeRequest
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("解析 Anthropic 请求失败：%w", err)
	}
	if cr.Model == "" {
		return nil, fmt.Errorf("model 为必填字段")
	}

	req := &models.ChatRequest{
		Model:     cr.Model,
		Stream:    cr.Stream,
		MaxTokens: cr.MaxTokens,
	}
	if cr.System != "" {
		req.Messages = append(req.Messages, models.Message{Role: "system", Content: models.MessageContent(cr.System)})
	}

	for _, m := range cr.Messages {
		msg, err := claudeContentToMessage(m.Role, m.Content)
		if err != nil {
			return nil, err
		}
		req.Messages = append(req.Messages, msg)
	}

	for _, tool := range cr.Tools {
		req.Tools = append(req.Tools, models.Tool{
			Type: "function",
			Function: models.Function{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return req, nil
}

// claudeContentToMessage 把一条 Anthropic 消息的 content（string 或 content block 数组）
// 转换为内部 Message。
func claudeContentToMessage(role string, content any) (models.Message, error) {
	switch c := content.(type) {
	case string:
		return models.Message{Role: role, Content: models.MessageContent(c)}, nil
	case nil:
		return models.Message{Role: role, Content: models.MessageContent("")}, nil
	}

	// content 是 block 数组，先还原成 claudeContentBlock 切片
	raw, err := json.Marshal(content)
	if err != nil {
		return models.Message{}, err
	}
	var blocks []claudeContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		// 解析失败则把原文当字符串兜底
		return models.Message{Role: role, Content: models.MessageContent(string(raw))}, nil
	}

	msg := models.Message{Role: role}
	var textParts []string
	multimodal := false

	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				textParts = append(textParts, b.Text)
			}
		case "image":
			multimodal = true
			url := claudeImageSourceToURL(b.Source)
			if url != "" {
				msg.Content = appendMultimodal(msg.Content, "image_url", url)
			}
		case "thinking":
			if b.Text != "" {
				msg.ReasoningContent += b.Text
			}
		case "tool_use":
			args := b.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			if err := appendToolCall(&msg, b.ID, b.Name, args); err != nil {
				return models.Message{}, err
			}
		case "tool_result":
			// Anthropic 把 tool_result 作为 user 消息的 content block；
			// OpenAI 侧表示为独立的 role="tool" 消息，这里直接返回一条 tool 消息。
			resp := blockContentToString(b.Content)
			return models.Message{Role: "tool", ToolCallID: b.ToolUseID, Content: models.MessageContent(resp)}, nil
		}
	}

	if len(msg.ToolCalls) > 0 {
		// assistant 消息带 tool_use，content 用文本（若有）
		msg.Content = models.MessageContent(joinText(textParts))
		return msg, nil
	}
	if multimodal {
		// 已写入多模态 JSON，textParts 忽略
		return msg, nil
	}
	msg.Content = models.MessageContent(joinText(textParts))
	return msg, nil
}

func claudeImageSourceToURL(src *claudeImageSource) string {
	if src == nil {
		return ""
	}
	if src.Type == "url" && src.URL != "" {
		return src.URL
	}
	if src.Type == "base64" && src.Data != "" {
		mime := src.MediaType
		if mime == "" {
			mime = "application/octet-stream"
		}
		return "data:" + mime + ";base64," + src.Data
	}
	return ""
}

func blockContentToString(content any) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	// tool_result 的 content 可能是 [{type:text,text:...}] 数组，提取文本
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &arr); err == nil {
		var sb = ""
		for _, p := range arr {
			if p.Type == "text" && p.Text != "" {
				if sb != "" {
					sb += "\n"
				}
				sb += p.Text
			}
		}
		if sb != "" {
			return sb
		}
	}
	return string(raw)
}

// SerializeClaudeResponse 把内部 ChatResponse 还原为 Anthropic 响应体。
func SerializeClaudeResponse(resp *models.ChatResponse) (any, error) {
	var choice models.Choice
	if len(resp.Choices) > 0 {
		choice = resp.Choices[0]
	}
	var blocks []claudeRespContent
	var text, reasoning string
	var toolUses []claudeRespContent

	if choice.Message != nil {
		text = choice.Message.Content.String()
		reasoning = choice.Message.ReasoningContent
		if len(choice.Message.ToolCalls) > 0 {
			var calls []openAIToolCall
			if err := json.Unmarshal(choice.Message.ToolCalls, &calls); err != nil {
				return nil, fmt.Errorf("解析 tool_calls 失败：%w", err)
			}
			for _, c := range calls {
				args := json.RawMessage(c.Function.Arguments)
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				toolUses = append(toolUses, claudeRespContent{
					Type:  "tool_use",
					ID:    c.ID,
					Name:  c.Function.Name,
					Input: args,
				})
			}
		}
	}

	// Anthropic 要求内容块顺序：thinking -> text -> tool_use
	if reasoning != "" {
		blocks = append(blocks, claudeRespContent{Type: "thinking", Thinking: reasoning})
	}
	if text != "" || (len(toolUses) == 0 && reasoning == "") {
		blocks = append(blocks, claudeRespContent{Type: "text", Text: text})
	}
	blocks = append(blocks, toolUses...)

	id := resp.ID
	if id == "" {
		id = "msg_" + randomID()
	}
	return claudeResponse{
		ID:         id,
		Type:       "message",
		Role:       "assistant",
		Model:      resp.Model,
		Content:    blocks,
		StopReason: reverseMapClaudeStopReason(choice.FinishReason),
		Usage: claudeUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}, nil
}

func reverseMapClaudeStopReason(openai string) string {
	switch openai {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "stop" // Anthropic 无对应枚举，降级为 end_turn
	default:
		return "end_turn"
	}
}

// ---------- 流式发射器（OpenAI StreamChunk -> Anthropic SSE 事件） ----------

// StreamEvent 表示一个厂商原生 SSE 事件（不含 `event:`/`data:` 前缀，由 handler 拼装）。
// 导出以便 handlers 包构造原生流式响应。
//   - Type 为空字符串时，handler 仅输出 `data:` 行（Gemini 流式格式）；
//   - 否则输出 `event: <Type>` + `data:` 两行（Anthropic 格式）。
//   - Data 可为 map[string]any（Anthropic）或任意结构体（Gemini 的完整响应体）。
type StreamEvent struct {
	Type string `json:"type"`
	Data any    `json:"-"`
}

// claudeStreamEmitter 把 OpenAI 流式 chunk 翻译成 Anthropic 的 SSE 事件序列。
// Anthropic 要求：先 message_start，再按内容块顺序 content_block_start/delta/stop，
// 最后 message_delta + message_stop；thinking 块必须在 text 块之前。
type claudeStreamEmitter struct {
	started     bool
	curType     string // 当前打开的内容块类型：text | thinking | tool_use | ""
	curIndex    int
	toolBlocks  map[int]*toolStreamBlock // OpenAI tool index -> Anthropic 块状态
	nextToolIdx int
}

type toolStreamBlock struct {
	anthropicIdx int
	id           string
	name         string
}

// NewClaudeStreamEmitter 返回一个带状态的 Anthropic 流式发射器。
func NewClaudeStreamEmitter() *claudeStreamEmitter {
	return &claudeStreamEmitter{
		toolBlocks:  make(map[int]*toolStreamBlock),
		nextToolIdx: 0,
	}
}

// Emit 把单个 OpenAI StreamChunk 翻译为 0..N 个 Anthropic 事件。
func (e *claudeStreamEmitter) Emit(chunk *models.StreamChunk) []StreamEvent {
	if chunk == nil || len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]
	delta := choice.Delta
	if delta == nil {
		delta = &models.Delta{}
	}
	var events []StreamEvent

	// 首块：emit message_start
	if !e.started {
		e.started = true
		usage := map[string]any{"input_tokens": chunk.Usage.PromptTokens, "output_tokens": 0}
		events = append(events, StreamEvent{
			Type: "message_start",
			Data: map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":            chunk.ID,
					"type":          "message",
					"role":          "assistant",
					"model":         chunk.Model,
					"content":       []any{},
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage":         usage,
				},
			},
		})
	}

	// 工具调用增量：先处理，再处理文本/思考（Anthropic 要求 thinking/text 在 tool_use 之前，
	// 但同一响应通常只有一类，这里按到达顺序开块并自动闭合前一块）。
	if len(delta.ToolCalls) > 0 {
		for _, tc := range delta.ToolCalls {
			blk, ok := e.toolBlocks[tc.Index]
			if !ok {
				// 新工具调用：闭合当前块，开新的 tool_use 块
				events = append(events, e.closeCurrent()...)
				idx := e.nextToolIdx
				e.nextToolIdx++
				blk = &toolStreamBlock{anthropicIdx: idx, id: tc.ID, name: tc.Function.Name}
				e.toolBlocks[tc.Index] = blk
				e.curType = "tool_use"
				e.curIndex = idx
				events = append(events, StreamEvent{
					Type: "content_block_start",
					Data: map[string]any{
						"type":  "content_block_start",
						"index": idx,
						"content_block": map[string]any{
							"type": "tool_use",
							"id":   tc.ID,
							"name": tc.Function.Name,
						},
					},
				})
			}
			if tc.Function.Arguments != "" {
				events = append(events, StreamEvent{
					Type: "content_block_delta",
					Data: map[string]any{
						"type":  "content_block_delta",
						"index": blk.anthropicIdx,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
					},
				})
			}
		}
		return events
	}

	// 思考流
	if delta.ReasoningContent != "" {
		if e.curType != "thinking" {
			events = append(events, e.closeCurrent()...)
			idx := e.nextToolIdx
			e.nextToolIdx++
			e.curType = "thinking"
			e.curIndex = idx
			events = append(events, StreamEvent{
				Type: "content_block_start",
				Data: map[string]any{
					"type":         "content_block_start",
					"index":        idx,
					"content_block": map[string]any{"type": "thinking"},
				},
			})
		}
		events = append(events, StreamEvent{
			Type: "content_block_delta",
			Data: map[string]any{
				"type":  "content_block_delta",
				"index": e.curIndex,
				"delta": map[string]any{"type": "thinking_delta", "thinking": delta.ReasoningContent},
			},
		})
		return events
	}

	// 普通文本
	if delta.Content != "" {
		if e.curType != "text" {
			events = append(events, e.closeCurrent()...)
			idx := e.nextToolIdx
			e.nextToolIdx++
			e.curType = "text"
			e.curIndex = idx
			events = append(events, StreamEvent{
				Type: "content_block_start",
				Data: map[string]any{
					"type":         "content_block_start",
					"index":        idx,
					"content_block": map[string]any{"type": "text"},
				},
			})
		}
		events = append(events, StreamEvent{
			Type: "content_block_delta",
			Data: map[string]any{
				"type":  "content_block_delta",
				"index": e.curIndex,
				"delta": map[string]any{"type": "text_delta", "text": delta.Content},
			},
		})
		return events
	}

	// 结束块（带 finish_reason 或 usage）
	if choice.FinishReason != "" || (chunk.Usage.TotalTokens != 0 && chunk.Usage.PromptTokens != 0) {
		events = append(events, e.closeCurrent()...)
		// 逐个闭合所有 tool_use 块
		for _, blk := range e.toolBlocks {
			events = append(events, StreamEvent{
				Type: "content_block_stop",
				Data: map[string]any{"type": "content_block_stop", "index": blk.anthropicIdx},
			})
		}
		stopReason := reverseMapClaudeStopReason(choice.FinishReason)
		events = append(events, StreamEvent{
			Type: "message_delta",
			Data: map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   stopReason,
					"stop_sequence": nil,
				},
				"usage": map[string]any{
					"input_tokens":  chunk.Usage.PromptTokens,
					"output_tokens": chunk.Usage.CompletionTokens,
				},
			},
		})
		events = append(events, StreamEvent{Type: "message_stop", Data: map[string]any{"type": "message_stop"}})
	}
	return events
}

// closeCurrent 闭合当前仍处于打开状态的内容块（text/thinking），返回对应事件。
func (e *claudeStreamEmitter) closeCurrent() []StreamEvent {
	if e.curType == "" || e.curType == "tool_use" {
		// tool_use 块在结束阶段统一闭合，这里不处理
		return nil
	}
	idx := e.curIndex
	e.curType = ""
	return []StreamEvent{{
		Type: "content_block_stop",
		Data: map[string]any{"type": "content_block_stop", "index": idx},
	}}
}

// ---------- 辅助函数 ----------

// appendMultimodal 把一条多模态 part（image_url）追加进 content，保持原始 JSON 数组形态。
func appendMultimodal(cur models.MessageContent, typ, url string) models.MessageContent {
	s := string(cur)
	var parts []map[string]any
	if s != "" && len(s) > 0 && s[0] == '[' {
		_ = json.Unmarshal([]byte(s), &parts)
	}
	parts = append(parts, map[string]any{
		"type":     typ,
		"image_url": map[string]any{"url": url},
	})
	b, _ := json.Marshal(parts)
	return models.MessageContent(b)
}

// joinText 用换行拼接非空文本块。
func joinText(parts []string) string {
	var sb strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(p)
	}
	return sb.String()
}

// appendToolCall 把一条 tool_use 块追加进 assistant 消息的 tool_calls（JSON 数组）。
func appendToolCall(msg *models.Message, id, name string, args json.RawMessage) error {
	var calls []openAIToolCall
	if len(msg.ToolCalls) > 0 {
		if err := json.Unmarshal(msg.ToolCalls, &calls); err != nil {
			return err
		}
	}
	calls = append(calls, openAIToolCall{
		ID:       id,
		Type:     "function",
		Function: openAIToolCallFn{Name: name, Arguments: string(args)},
	})
	raw, err := json.Marshal(calls)
	if err != nil {
		return err
	}
	msg.ToolCalls = raw
	return nil
}

// randomID 生成短随机 id（用于补全缺失的 Anthropic message id）。
func randomID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
