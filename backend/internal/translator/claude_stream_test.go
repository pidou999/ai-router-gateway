package translator

import (
	"testing"
)

// TestClaudeStreamPromptTokens 验证流式场景 prompt token 不丢失：
// Anthropic 在 message_start 的 usage.input_tokens 给出 prompt token，
// 而结尾 message_delta 的 usage 只给 output_tokens，转换器必须在状态机里暂存并合并。
func TestClaudeStreamPromptTokens(t *testing.T) {
	st := (&ClaudeTranslator{}).NewStream()

	start := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":    "msg_01",
			"model": "claude-3-5-sonnet",
			"usage": map[string]interface{}{
				"input_tokens":  float64(123),
				"output_tokens": float64(0),
			},
		},
	}
	if _, err := st.TranslateChunk(start); err != nil {
		t.Fatalf("message_start: %v", err)
	}

	// 中间若干文本 delta，不应影响最终 usage
	textDelta := map[string]interface{}{
		"type":  "content_block_delta",
		"index": float64(0),
		"delta": map[string]interface{}{"type": "text_delta", "text": "hello"},
	}
	if _, err := st.TranslateChunk(textDelta); err != nil {
		t.Fatalf("text_delta: %v", err)
	}

	end := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason": "end_turn",
		},
		"usage": map[string]interface{}{
			"output_tokens": float64(45),
		},
	}
	sc, err := st.TranslateChunk(end)
	if err != nil {
		t.Fatalf("message_delta: %v", err)
	}
	if sc == nil {
		t.Fatal("message_delta chunk 为 nil")
	}
	u := sc.Usage
	if u.PromptTokens != 123 {
		t.Errorf("PromptTokens = %d, want 123 (来自 message_start)", u.PromptTokens)
	}
	if u.CompletionTokens != 45 {
		t.Errorf("CompletionTokens = %d, want 45", u.CompletionTokens)
	}
	if u.TotalTokens != 168 {
		t.Errorf("TotalTokens = %d, want 168 (123+45)", u.TotalTokens)
	}
}
