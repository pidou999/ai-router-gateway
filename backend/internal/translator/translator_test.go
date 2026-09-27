package translator

import (
	"encoding/json"
	"strings"
	"testing"

	"ai-router-gateway/internal/models"
)

func TestRegistryRegistered(t *testing.T) {
	for _, at := range []string{"anthropic", "claude", "gemini", "openai"} {
		if GetTranslator(at) == nil {
			t.Errorf("translator 未注册: %s", at)
		}
	}
	// OpenAI 兼容的服务商不应有 translator，走默认透传
	if GetTranslator("deepseek") != nil {
		t.Errorf("deepseek 不应注册 translator")
	}
}

func TestClaudeTranslateRequest(t *testing.T) {
	req := &models.ChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []models.Message{
			{Role: "system", Content: models.MessageContent("You are helpful.")},
			{Role: "user", Content: models.MessageContent("What is this?")},
			{Role: "assistant", Content: models.MessageContent("Let me check."), ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]`)},
			{Role: "tool", ToolCallID: "call_1", Content: models.MessageContent("sunny")},
			{Role: "user", Content: models.MessageContent(`[{"type":"text","text":"here"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD"}}]`)},
		},
		Tools: []models.Tool{{Function: models.Function{Name: "get_weather", Description: "w", Parameters: map[string]any{"type": "object"}}}},
	}
	body, headers, err := (&ClaudeTranslator{}).TranslateRequest(req)
	if err != nil {
		t.Fatalf("TranslateRequest error: %v", err)
	}
	cr, ok := body.(claudeRequest)
	if !ok {
		t.Fatalf("body 类型错误: %T", body)
	}
	if cr.System != "You are helpful." {
		t.Errorf("system 未提取: %q", cr.System)
	}
	if cr.MaxTokens != 4096 {
		t.Errorf("max_tokens 默认值错误: %d", cr.MaxTokens)
	}
	if headers["anthropic-version"] != "2023-06-01" {
		t.Errorf("缺少 anthropic-version 头: %v", headers)
	}
	// assistant 的 tool_calls 应变成 tool_use 内容块
	var sawToolUse, sawToolResult, sawImage bool
	for _, m := range cr.Messages {
		blocks, isBlocks := m.Content.([]claudeContentBlock)
		if !isBlocks {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				sawToolUse = true
				if b.Name != "get_weather" || string(b.Input) != `{"city":"SF"}` {
					t.Errorf("tool_use 内容错误: name=%q input=%s", b.Name, b.Input)
				}
			case "tool_result":
				sawToolResult = true
				if b.ToolUseID != "call_1" {
					t.Errorf("tool_result id 错误: %q", b.ToolUseID)
				}
			case "image":
				sawImage = true
				if b.Source == nil || b.Source.Type != "base64" || b.Source.Data != "QUJD" {
					t.Errorf("image source 错误: %+v", b.Source)
				}
			}
		}
	}
	if !sawToolUse {
		t.Error("未生成 tool_use 内容块")
	}
	if !sawToolResult {
		t.Error("未生成 tool_result 内容块")
	}
	if !sawImage {
		t.Error("未生成 image 内容块")
	}
	if len(cr.Tools) != 1 || cr.Tools[0].Name != "get_weather" {
		t.Errorf("tools 未正确翻译: %+v", cr.Tools)
	}
}

func TestClaudeTranslateResponseToolUse(t *testing.T) {
	raw := claudeResponse{
		ID:      "msg_1",
		Model:   "claude-3-5-sonnet",
		Content: []claudeRespContent{
			{Type: "text", Text: "Result: "},
			{Type: "thinking", Thinking: "hmm"},
			{Type: "tool_use", ID: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
		},
		StopReason: "tool_use",
		Usage:      claudeUsage{InputTokens: 10, OutputTokens: 5},
	}
	resp, err := (&ClaudeTranslator{}).TranslateResponse(raw)
	if err != nil {
		t.Fatalf("TranslateResponse error: %v", err)
	}
	msg := resp.Choices[0].Message
	if msg.Content.String() != "Result: " {
		t.Errorf("text 拼接错误: %q", msg.Content.String())
	}
	if msg.ReasoningContent != "hmm" {
		t.Errorf("reasoning 未提取: %q", msg.ReasoningContent)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason 错误: %q", resp.Choices[0].FinishReason)
	}
	if len(msg.ToolCalls) == 0 {
		t.Fatal("未生成 tool_calls")
	}
	var tcs []openAIToolCall
	if err := json.Unmarshal(msg.ToolCalls, &tcs); err != nil {
		t.Fatalf("tool_calls 解析失败: %v", err)
	}
	if tcs[0].Function.Name != "get_weather" || tcs[0].Function.Arguments != `{"city":"SF"}` {
		t.Errorf("tool_calls 内容错误: %+v", tcs[0])
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("usage 错误: %+v", resp.Usage)
	}
}

func TestClaudeStreamStateMachine(t *testing.T) {
	st := (&ClaudeTranslator{}).NewStream()
	events := []map[string]interface{}{
		{"type": "message_start", "message": map[string]interface{}{"id": "msg_1", "model": "claude-x"}},
		{"type": "content_block_start", "index": float64(0), "content_block": map[string]interface{}{"type": "text"}},
		{"type": "content_block_delta", "index": float64(0), "delta": map[string]interface{}{"type": "text_delta", "text": "Hello"}},
		{"type": "content_block_start", "index": float64(1), "content_block": map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "get_weather"}},
		{"type": "content_block_delta", "index": float64(1), "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": "{\"city\""}},
		{"type": "content_block_delta", "index": float64(1), "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": ":\"SF\"}"}},
		{"type": "content_block_stop", "index": float64(1)},
		{"type": "message_delta", "delta": map[string]interface{}{"stop_reason": "tool_use"}, "usage": map[string]interface{}{"output_tokens": float64(12)}},
		{"type": "message_stop"},
		{"type": "ping"},
	}

	var textChunks, toolOpen, toolArgs, finishes int
	var lastArgs string
	for _, ev := range events {
		sc, err := st.TranslateChunk(ev)
		if err != nil {
			t.Fatalf("TranslateChunk error: %v", err)
		}
		if sc == nil {
			continue
		}
		for _, c := range sc.Choices {
			if c.Delta != nil {
				if c.Delta.Content != "" {
					textChunks++
				}
				if len(c.Delta.ToolCalls) > 0 {
					tc := c.Delta.ToolCalls[0]
					if tc.ID != "" {
						toolOpen++
						if tc.Function.Name != "get_weather" {
							t.Errorf("tool 名称错误: %q", tc.Function.Name)
						}
					}
					if tc.Function.Arguments != "" {
						toolArgs++
						lastArgs = tc.Function.Arguments
					}
					if c.Delta.Role == "assistant" {
						// role 在第一个 chunk 出现
					}
				}
			}
			if c.FinishReason != "" {
				finishes++
				if c.FinishReason != "tool_calls" {
					t.Errorf("finish 错误: %q", c.FinishReason)
				}
				if sc.Usage.CompletionTokens != 12 {
					t.Errorf("流式 usage 错误: %+v", sc.Usage)
				}
			}
		}
	}
	if textChunks != 1 {
		t.Errorf("text chunk 数应为 1，实际 %d", textChunks)
	}
	if toolOpen != 1 {
		t.Errorf("tool 开启 chunk 数应为 1，实际 %d", toolOpen)
	}
	if toolArgs != 2 {
		t.Errorf("tool 参数分片数应为 2，实际 %d", toolArgs)
	}
	// 两个分片应拼接为完整 JSON
	if lastArgs != `:"SF"}` {
		t.Errorf("最后一个参数分片应为 :\"SF\"，实际 %q", lastArgs)
	}
	if finishes != 1 {
		t.Errorf("finish chunk 数应为 1，实际 %d", finishes)
	}
	// 验证序列化出 tool_calls 字段
	b, _ := json.Marshal(map[string]interface{}{"choices": []models.Choice{{Index: 0, Delta: &models.Delta{ToolCalls: []models.ToolCallDelta{{Index: 1, ID: "t", Function: struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	}{Name: "get_weather", Arguments: "{}"}}}}}}})
	if !strings.Contains(string(b), "tool_calls") {
		t.Error("Delta 序列化未包含 tool_calls")
	}
}

func TestGeminiTranslateRequest(t *testing.T) {
	req := &models.ChatRequest{
		Model: "gemini-1.5-pro",
		Messages: []models.Message{
			{Role: "system", Content: models.MessageContent("sys")},
			{Role: "user", Content: models.MessageContent(`[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,SU1H"}}]`)},
			{Role: "assistant", Content: models.MessageContent("ok"), ToolCalls: json.RawMessage(`[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]`)},
		},
	}
	body, _, err := (&GeminiTranslator{}).TranslateRequest(req)
	if err != nil {
		t.Fatalf("TranslateRequest error: %v", err)
	}
	gr, ok := body.(geminiRequest)
	if !ok {
		t.Fatalf("body 类型错误: %T", body)
	}
	if gr.SystemInstruction == nil || gr.SystemInstruction.Parts[0].Text != "sys" {
		t.Error("systemInstruction 错误")
	}
	if len(gr.Contents) != 2 {
		t.Fatalf("contents 数错误（system 应进入 SystemInstruction，不计 contents）: %d", len(gr.Contents))
	}
	// user 消息含 inline_data
	var sawInline bool
	for _, p := range gr.Contents[0].Parts {
		if p.InlineData != nil && p.InlineData.Data == "SU1H" && p.InlineData.MimeType == "image/jpeg" {
			sawInline = true
		}
	}
	if !sawInline {
		t.Error("未生成 inline_data")
	}
	// assistant tool_calls -> functionCall（contents[1] 为 assistant）
	var sawFunc bool
	for _, p := range gr.Contents[1].Parts {
		if p.FunctionCall != nil {
			sawFunc = true
		}
	}
	if !sawFunc {
		t.Error("未生成 functionCall")
	}
}

func TestGeminiTranslateResponseFunctionCall(t *testing.T) {
	raw := geminiResponse{
		Candidates: []geminiCandidate{{
			Content:      geminiContent{Parts: []geminiPart{{Text: "answer"}, {FunctionCall: &geminiFuncCall{Name: "f", Args: map[string]any{"x": 1}}}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &geminiUsageMeta{PromptTokenCount: 3, CandidatesTokenCount: 4, TotalTokenCount: 7},
	}
	resp, err := (&GeminiTranslator{}).TranslateResponse(raw)
	if err != nil {
		t.Fatalf("TranslateResponse error: %v", err)
	}
	if resp.Choices[0].Message.Content.String() != "answer" {
		t.Errorf("text 错误: %q", resp.Choices[0].Message.Content.String())
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish 错误: %q", resp.Choices[0].FinishReason)
	}
	if len(resp.Choices[0].Message.ToolCalls) == 0 {
		t.Fatal("未生成 tool_calls")
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("usage 错误: %+v", resp.Usage)
	}
}

func TestGeminiStream(t *testing.T) {
	st := (&GeminiTranslator{}).NewStream()
	mkCand := func(text, finish string) map[string]interface{} {
		content := map[string]interface{}{"parts": []map[string]interface{}{}}
		if text != "" {
			content["parts"] = []map[string]interface{}{{"text": text}}
		}
		c := map[string]interface{}{"content": content}
		if finish != "" {
			c["finishReason"] = finish
		}
		return c
	}
	chunks := []map[string]interface{}{
		{"candidates": []map[string]interface{}{mkCand("Hel", "")}},
		{"candidates": []map[string]interface{}{mkCand("lo", "")}},
		{"candidates": []map[string]interface{}{mkCand("", "STOP")},
			"usageMetadata": map[string]interface{}{"promptTokenCount": float64(2), "candidatesTokenCount": float64(3), "totalTokenCount": float64(5)}},
	}
	var texts, finishes int
	var roleSeen bool
	for _, c := range chunks {
		sc, err := st.TranslateChunk(c)
		if err != nil {
			t.Fatalf("TranslateChunk error: %v", err)
		}
		if sc == nil {
			continue
		}
		for _, ch := range sc.Choices {
			if ch.Delta != nil {
				if ch.Delta.Role == "assistant" {
					roleSeen = true
				}
				texts += len(ch.Delta.Content)
			}
			if ch.FinishReason != "" {
				finishes++
				if sc.Usage.TotalTokens != 5 {
					t.Errorf("流式 usage 错误: %+v", sc.Usage)
				}
			}
		}
	}
	if !roleSeen {
		t.Error("未下发 role")
	}
	if texts != 5 { // "Hel" + "lo"
		t.Errorf("拼接文本应为 Hello(5)，实际长度 %d", texts)
	}
	if finishes != 1 {
		t.Errorf("finish 数应为 1，实际 %d", finishes)
	}
}
