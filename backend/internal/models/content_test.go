package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageContentMultimodalRoundTrip(t *testing.T) {
	req := `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"描述这张图"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}],"stream":false}`
	var cr ChatRequest
	if err := json.Unmarshal([]byte(req), &cr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(&cr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "image_url") {
		t.Fatalf("image_url dropped from forwarded request: %s", s)
	}
	if !strings.Contains(s, "data:image/png;base64,AAAA") {
		t.Fatalf("image data dropped from forwarded request: %s", s)
	}
}

func TestMessageContentTextArrayFlattened(t *testing.T) {
	req := `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}]}],"stream":false}`
	var cr ChatRequest
	if err := json.Unmarshal([]byte(req), &cr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := cr.Messages[0].Content.String(); got != "hello\nworld" {
		t.Fatalf("text array not flattened: got %q", got)
	}
	// 序列化后应是普通 JSON 字符串（"content":"hello\nworld"），而非数组
	out, _ := json.Marshal(&cr)
	if !strings.Contains(string(out), `"content":"hello\nworld"`) {
		t.Fatalf("text array should marshal as string, got: %s", string(out))
	}
}

func TestDeltaPreservesUnknownFields(t *testing.T) {
	// 模拟 StepFun / Kimi 等厂商在 delta 中发送的私有思考字段
	raw := `{"role":"assistant","thinking":"let me think...","reasoning_content":"deep thought","some_vendor_field":[1,2,3]}`
	var d Delta
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Role != "assistant" {
		t.Fatalf("role: got %q, want %q", d.Role, "assistant")
	}
	if d.ReasoningContent != "deep thought" {
		t.Fatalf("reasoning_content: got %q, want %q", d.ReasoningContent, "deep thought")
	}
	if len(d.Extra) < 2 {
		t.Fatalf("Extra should have >=2 entries, got %d: %v", len(d.Extra), d.Extra)
	}
	if _, ok := d.Extra["thinking"]; !ok {
		t.Fatal(`Extra should contain "thinking"`)
	}
	if _, ok := d.Extra["some_vendor_field"]; !ok {
		t.Fatal(`Extra should contain "some_vendor_field"`)
	}

	// 序列化后应包含所有字段（已知 + 未知）
	out, err := json.Marshal(&d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	for _, field := range []string{"role", "thinking", "reasoning_content", "some_vendor_field"} {
		if !strings.Contains(s, field) {
			t.Fatalf("field %q missing from marshaled delta: %s", field, s)
		}
	}
}

func TestDeltaEmptyExtraRoundTrip(t *testing.T) {
	// 标准 OpenAI delta（无未知字段）应正常工作
	raw := `{"role":"assistant","content":"hello"}`
	var d Delta
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, _ := json.Marshal(&d)
	s := string(out)
	if !strings.Contains(s, `"content":"hello"`) {
		t.Fatalf("standard delta round-trip failed: %s", s)
	}
	// 不应包含 Extra 的残留字段
	if strings.Contains(s, `"Extra"`) {
		t.Fatalf("Extra should not appear in output: %s", s)
	}
}
