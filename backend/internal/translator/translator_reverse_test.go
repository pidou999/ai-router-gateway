package translator

import (
	"encoding/json"
	"strings"
	"testing"

	"ai-router-gateway/internal/models"
)

func TestParseClaudeRequestText(t *testing.T) {
	body := `{
		"model":"claude-3-5-sonnet-20241022",
		"max_tokens":1024,
		"system":"You are helpful.",
		"messages":[
			{"role":"user","content":"Hello"},
			{"role":"assistant","content":"Hi there"},
			{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}}]}
		],
		"tools":[{"name":"get_weather","description":"d","input_schema":{"type":"object"}}]
	}`
	req, err := ParseClaudeRequest([]byte(body))
	if err != nil {
		t.Fatalf("ParseClaudeRequest error: %v", err)
	}
	if req.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("model = %q", req.Model)
	}
	if req.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d", req.MaxTokens)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("messages 数应为 4（system + 3），实际 %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content.String() != "You are helpful." {
		t.Errorf("system 消息错误: %+v", req.Messages[0])
	}
	// 多模态图片：MessageContent.String() 仅拼接文本、会丢弃图片 part，
	// 因此断言需针对原始 JSON 内容（保留 image_url）。
	if !strings.Contains(string(req.Messages[3].Content), "data:image/png;base64,AAA") {
		t.Errorf("多模态图片未转为 image_url: %s", string(req.Messages[3].Content))
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tools 解析错误: %+v", req.Tools)
	}
}

func TestParseClaudeRequestToolUseAndResult(t *testing.T) {
	// tool_use -> assistant 带 tool_calls
	body := `{"model":"x","max_tokens":10,"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"foo","input":{"a":1}}]}
	]}`
	req, err := ParseClaudeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "assistant" {
		t.Fatalf("role 应为 assistant: %+v", req.Messages)
	}
	var calls []openAIToolCall
	if err := json.Unmarshal(req.Messages[0].ToolCalls, &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].ID != "tu_1" || calls[0].Function.Name != "foo" || calls[0].Function.Arguments != `{"a":1}` {
		t.Errorf("tool_calls 解析错误: %+v", calls)
	}

	// tool_result -> role=tool 消息
	body2 := `{"model":"x","max_tokens":10,"messages":[
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"42"}]}
	]}`
	req2, err := ParseClaudeRequest([]byte(body2))
	if err != nil {
		t.Fatal(err)
	}
	m := req2.Messages[0]
	if m.Role != "tool" || m.ToolCallID != "tu_1" || m.Content.String() != "42" {
		t.Errorf("tool_result 解析错误: %+v", m)
	}
}

func TestSerializeClaudeResponse(t *testing.T) {
	args := `{"a":1}`
	resp := &models.ChatResponse{
		ID:      "chatcmpl-1",
		Model:   "claude-3-5-sonnet",
		Choices: []models.Choice{{
			Index: 0,
			Message: &models.Message{
				Role:            "assistant",
				Content:         models.MessageContent("Hi"),
				ReasoningContent: "thinking...",
				ToolCalls: mustToolCalls(t, []openAIToolCall{{
					ID: "tu1", Type: "function", Function: openAIToolCallFn{Name: "foo", Arguments: args},
				}}),
			},
			FinishReason: "tool_calls",
		}},
		Usage: models.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	}
	out, err := SerializeClaudeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	cr := out.(claudeResponse)
	if cr.Type != "message" || cr.Role != "assistant" || cr.StopReason != "tool_use" {
		t.Errorf("响应元字段错误: type=%s role=%s stop=%s", cr.Type, cr.Role, cr.StopReason)
	}
	// 顺序应为 thinking, text, tool_use
	var types []string
	for _, b := range cr.Content {
		types = append(types, b.Type)
	}
	if len(types) != 3 || types[0] != "thinking" || types[1] != "text" || types[2] != "tool_use" {
		t.Errorf("content 块顺序/类型错误: %v", types)
	}
	if cr.Usage.InputTokens != 5 || cr.Usage.OutputTokens != 3 {
		t.Errorf("usage 错误: %+v", cr.Usage)
	}
}

func TestClaudeStreamEmitter(t *testing.T) {
	em := NewClaudeStreamEmitter()
	var types []string
	emit := func(ch *models.StreamChunk) {
		for _, ev := range em.Emit(ch) {
			types = append(types, ev.Type)
		}
	}
	emit(&models.StreamChunk{ID: "m1", Model: "claude-x", Usage: models.Usage{PromptTokens: 5},
		Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Role: "assistant"}}}})
	emit(&models.StreamChunk{Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Content: "Hel"}}}})
	emit(&models.StreamChunk{Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Content: "lo"}}}})
	emit(&models.StreamChunk{Choices: []models.Choice{{Index: 0, FinishReason: "stop"}},
		Usage: models.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}})

	// 期望序列：message_start, content_block_start, content_block_delta, content_block_delta,
	//          content_block_stop, message_delta, message_stop
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if len(types) != len(want) {
		t.Fatalf("事件序列长度错误: %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Errorf("事件 %d 期望 %s 实际 %s（序列=%v）", i, want[i], types[i], types)
		}
	}

	// 工具调用流式
	em2 := NewClaudeStreamEmitter()
	var t2 []string
	emit2 := func(ch *models.StreamChunk) {
		for _, ev := range em2.Emit(ch) {
			t2 = append(t2, ev.Type)
		}
	}
	emit2(&models.StreamChunk{Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Role: "assistant"}}}})
	tc1 := models.ToolCallDelta{Index: 0, ID: "t1", Type: "function"}
	tc1.Function.Name = "foo"
	emit2(&models.StreamChunk{Choices: []models.Choice{{Index: 0, Delta: &models.Delta{ToolCalls: []models.ToolCallDelta{tc1}}}}})
	tc2 := models.ToolCallDelta{Index: 0}
	tc2.Function.Arguments = `{"a":1}`
	emit2(&models.StreamChunk{Choices: []models.Choice{{Index: 0, Delta: &models.Delta{ToolCalls: []models.ToolCallDelta{tc2}}}}})
	emit2(&models.StreamChunk{Choices: []models.Choice{{Index: 0, FinishReason: "tool_calls"}},
		Usage: models.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}})

	// 期望：message_start, content_block_start(tool_use), content_block_delta(input_json), content_block_stop, message_delta, message_stop
	want2 := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if len(t2) != len(want2) {
		t.Fatalf("工具流事件序列长度错误: %v", t2)
	}
	for i := range want2 {
		if t2[i] != want2[i] {
			t.Errorf("工具流事件 %d 期望 %s 实际 %s（序列=%v）", i, want2[i], t2[i], t2)
		}
	}
}

func TestParseGeminiRequest(t *testing.T) {
	body := `{
		"contents":[{"role":"user","parts":[{"text":"Hi"}]}],
		"systemInstruction":{"parts":[{"text":"sys"}]},
		"tools":[{"functionDeclarations":[{"name":"f","parameters":{}}]}]
	}`
	req, err := ParseGeminiRequest([]byte(body), "gemini-1.5-pro")
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gemini-1.5-pro" {
		t.Errorf("model = %q", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages 应为 2（含 system），实际 %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content.String() != "sys" {
		t.Errorf("system 错误: %+v", req.Messages[0])
	}
	if req.Messages[1].Content.String() != "Hi" {
		t.Errorf("user 错误: %+v", req.Messages[1])
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "f" {
		t.Errorf("tools 错误: %+v", req.Tools)
	}

	// 模型缺失应报错
	if _, err := ParseGeminiRequest([]byte(body), ""); err == nil {
		t.Error("缺少 model 应报错")
	}
}

func TestSerializeGeminiResponse(t *testing.T) {
	resp := &models.ChatResponse{
		Choices: []models.Choice{{
			Index:  0,
			Message: &models.Message{Role: "assistant", Content: models.MessageContent("Hi")},
			FinishReason: "stop",
		}},
		Usage: models.Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6},
	}
	out, err := SerializeGeminiResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	gr := out.(geminiResponse)
	if len(gr.Candidates) != 1 || gr.Candidates[0].Content.Role != "model" {
		t.Errorf("candidate 错误: %+v", gr.Candidates)
	}
	if len(gr.Candidates[0].Content.Parts) != 1 || gr.Candidates[0].Content.Parts[0].Text != "Hi" {
		t.Errorf("text part 错误: %+v", gr.Candidates[0].Content.Parts)
	}
	if gr.UsageMetadata == nil || gr.UsageMetadata.TotalTokenCount != 6 {
		t.Errorf("usage 错误: %+v", gr.UsageMetadata)
	}
}

func TestGeminiStreamEmitter(t *testing.T) {
	em := NewGeminiStreamEmitter()
	var evs []StreamEvent
	emit := func(ch *models.StreamChunk) {
		for _, ev := range em.Emit(ch) {
			evs = append(evs, ev)
		}
	}
	emit(&models.StreamChunk{Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Content: "Hel"}}}})
	emit(&models.StreamChunk{Choices: []models.Choice{{Index: 0, Delta: &models.Delta{Content: "lo"}}}})
	emit(&models.StreamChunk{Choices: []models.Choice{{Index: 0, FinishReason: "stop"}},
		Usage: models.Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}})

	if len(evs) != 3 {
		t.Fatalf("事件数应为 3，实际 %d", len(evs))
	}
	for _, ev := range evs {
		if ev.Type != "" {
			t.Errorf("Gemini 流式事件不应有 event 前缀，实际 %q", ev.Type)
		}
	}
	// 最后一个应含 usageMetadata
	last := evs[len(evs)-1].Data.(geminiResponse)
	if last.UsageMetadata == nil || last.UsageMetadata.TotalTokenCount != 6 {
		t.Errorf("结尾分片 usage 错误: %+v", last.UsageMetadata)
	}
}

// ---- 测试辅助 ----

func mustToolCalls(t *testing.T, calls []openAIToolCall) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(calls)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
