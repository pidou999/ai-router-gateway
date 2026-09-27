package trace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewAndContextRoundTrip(t *testing.T) {
	tr := New(42)
	if tr.UserID != 42 {
		t.Fatalf("UserID = %d, want 42", tr.UserID)
	}
	if tr.RequestID == "" {
		t.Fatal("RequestID 不应为空")
	}
	if tr.Segments == nil {
		t.Fatal("Segments 应被初始化")
	}
	if tr.StartedAt.IsZero() {
		t.Fatal("StartedAt 应被初始化")
	}

	ctx := WithTrace(context.Background(), tr)
	got := FromContext(ctx)
	if got != tr {
		t.Fatalf("FromContext 返回了不同的实例")
	}

	// 未挂载 trace 的 context 必须返回 nil，保证调用方判空后可无损降级
	if FromContext(context.Background()) != nil {
		t.Fatal("空 context 应返回 nil")
	}
}

func TestRequestIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := New(1).RequestID
		if seen[id] {
			t.Fatalf("RequestID 重复: %s", id)
		}
		seen[id] = true
	}
}

func TestStartEndSegment(t *testing.T) {
	tr := New(1)
	tr.StartSegment("upstream")
	time.Sleep(12 * time.Millisecond)
	tr.EndSegment("upstream", "ok", map[string]any{"status_code": 200})

	s := tr.Segments["upstream"]
	if s == nil {
		t.Fatal("upstream 段缺失")
	}
	if s.Status != "ok" {
		t.Fatalf("Status = %q, want ok", s.Status)
	}
	if s.DurationMs < 10 {
		t.Fatalf("DurationMs = %d, 应 >= 10", s.DurationMs)
	}
	if s.Detail["status_code"] != 200 {
		t.Fatalf("Detail status_code = %v, want 200", s.Detail["status_code"])
	}
}

func TestStartSegmentIdempotent(t *testing.T) {
	tr := New(1)
	tr.StartSegment("route")
	first := tr.Segments["route"].StartedAt
	time.Sleep(5 * time.Millisecond)
	tr.StartSegment("route") // 重复开启不应重置起点
	if !tr.Segments["route"].StartedAt.Equal(first) {
		t.Fatal("重复 StartSegment 不应重置 StartedAt")
	}
}

func TestEndSegmentWithoutStart(t *testing.T) {
	tr := New(1)
	// 未 StartSegment 直接结束：应自动补建，且耗时以请求起点计
	tr.EndSegment("auth", "error", map[string]any{"error": "invalid key"})
	s := tr.Segments["auth"]
	if s == nil {
		t.Fatal("auth 段应被自动创建")
	}
	if s.Status != "error" {
		t.Fatalf("Status = %q, want error", s.Status)
	}
	if s.Detail["error"] != "invalid key" {
		t.Fatalf("Detail error = %v", s.Detail["error"])
	}
}

func TestEndSegmentKeepsExistingDetail(t *testing.T) {
	tr := New(1)
	tr.StartSegment("route")
	tr.SetDetail("route", map[string]any{"provider": "openai"})
	tr.EndSegment("route", "ok", nil) // detail 传 nil 时应保留

	if got := tr.Segments["route"].Detail["provider"]; got != "openai" {
		t.Fatalf("detail 被 nil 覆盖了: %v", got)
	}
	if tr.Segments["route"].Status != "ok" {
		t.Fatal("Status 未更新")
	}
}

func TestRecordSegment(t *testing.T) {
	tr := New(1)
	tr.RecordSegment("translate", "ok", 37, map[string]any{
		"api_type":   "claude",
		"translated": true,
	})
	s := tr.Segments["translate"]
	if s.DurationMs != 37 {
		t.Fatalf("DurationMs = %d, want 37", s.DurationMs)
	}
	if s.Detail["api_type"] != "claude" {
		t.Fatalf("Detail api_type = %v", s.Detail["api_type"])
	}

	// 覆盖写：同名段应被替换
	tr.RecordSegment("translate", "skip", 0, nil)
	if tr.Segments["translate"].Status != "skip" || tr.Segments["translate"].DurationMs != 0 {
		t.Fatal("RecordSegment 未覆盖同名段")
	}
}

func TestSetDetailCreatesSegment(t *testing.T) {
	tr := New(1)
	tr.SetDetail("route", map[string]any{"account_id": int64(7)})
	s := tr.Segments["route"]
	if s == nil {
		t.Fatal("SetDetail 应自动创建段")
	}
	if s.Detail["account_id"] != int64(7) {
		t.Fatalf("Detail account_id = %v", s.Detail["account_id"])
	}
}

// TestSetDetailMergesKeys 回归测试：路由阶段先写入的组合信息不得被后续写入整段覆盖。
// 历史 bug：SetDetail 用 `s.Detail = detail` 整体替换，导致上游胜出后写入
// provider 信息时把 combo / intent / picked_model 抹掉，日志里查不到智能路由记录。
func TestSetDetailMergesKeys(t *testing.T) {
	tr := New(1)
	tr.StartSegment("route")
	tr.SetDetail("route", map[string]any{
		"combo":        "free-1",
		"intent":       "vision",
		"picked_model": "qwen-vl",
	})
	// 上游胜出后写入服务商信息（同名 key 覆盖，异名 key 必须保留）
	tr.SetDetail("route", map[string]any{
		"provider_id":   int64(49),
		"provider_name": "魔塔",
		"picked_model":  "qwen-vl-plus",
	})

	d := tr.Segments["route"].Detail
	if d["combo"] != "free-1" {
		t.Fatalf("combo 被覆盖丢失，detail = %v", d)
	}
	if d["intent"] != "vision" {
		t.Fatalf("intent 被覆盖丢失，detail = %v", d)
	}
	if d["provider_name"] != "魔塔" {
		t.Fatalf("provider_name 未写入，detail = %v", d)
	}
	if d["picked_model"] != "qwen-vl-plus" {
		t.Fatalf("同名 key 应以后写为准，got %v", d["picked_model"])
	}

	// 结束段时带 detail 也应合并而非替换
	tr.EndSegment("route", "error", map[string]any{"error": "所有层级均已耗尽"})
	d = tr.Segments["route"].Detail
	if d["combo"] != "free-1" || d["error"] != "所有层级均已耗尽" {
		t.Fatalf("EndSegment 未合并 detail，got %v", d)
	}
}

func TestAddAttemptAndModel(t *testing.T) {
	tr := New(1)
	tr.RecordModel("claude-3-5-sonnet")
	tr.AddAttempt(Attempt{
		ProviderID: 1, ProviderName: "anthropic", AccountID: 3,
		Model: "claude-3-5-sonnet", APIType: "claude",
		StatusCode: 429, Status: "error", Error: "rate limited", LatencyMs: 120,
	})
	tr.AddAttempt(Attempt{
		ProviderID: 2, ProviderName: "openai", AccountID: 5,
		Model: "gpt-4o", APIType: "openai",
		StatusCode: 200, Status: "ok", LatencyMs: 340,
	})

	if tr.Model != "claude-3-5-sonnet" {
		t.Fatalf("Model = %q", tr.Model)
	}
	if len(tr.Attempts) != 2 {
		t.Fatalf("Attempts 数量 = %d, want 2", len(tr.Attempts))
	}
	if tr.Attempts[0].StatusCode != 429 || tr.Attempts[1].Status != "ok" {
		t.Fatal("attempt 内容不正确")
	}
}

func TestJSONShape(t *testing.T) {
	tr := New(9)
	tr.RecordModel("gpt-4o")
	tr.RecordSegment("auth", "ok", 1, map[string]any{"user_id": int64(9)})
	tr.RecordSegment("route", "ok", 2, map[string]any{"provider_name": "openai"})
	tr.RecordSegment("translate", "skip", 0, nil)
	tr.RecordSegment("upstream", "ok", 500, map[string]any{"status_code": 200})
	tr.AddAttempt(Attempt{ProviderID: 1, Status: "ok", LatencyMs: 500})

	raw := tr.JSON()
	for _, key := range []string{"auth", "route", "translate", "upstream", "attempts", "request_id", "gpt-4o"} {
		if !strings.Contains(raw, key) {
			t.Fatalf("JSON 缺少 %q: %s", key, raw)
		}
	}

	var decoded struct {
		RequestID string                    `json:"request_id"`
		UserID    int64                     `json:"user_id"`
		Model     string                    `json:"model"`
		Segments  map[string]map[string]any `json:"segments"`
		Attempts  []map[string]any          `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("JSON 不可解析: %v", err)
	}
	if decoded.UserID != 9 || decoded.Model != "gpt-4o" {
		t.Fatalf("解析结果不符: %+v", decoded)
	}
	if len(decoded.Segments) != 4 {
		t.Fatalf("段数量 = %d, want 4", len(decoded.Segments))
	}
	if len(decoded.Attempts) != 1 {
		t.Fatalf("attempts 数量 = %d, want 1", len(decoded.Attempts))
	}
}

func TestLogSetsEndedAt(t *testing.T) {
	tr := New(1)
	if !tr.EndedAt.IsZero() {
		t.Fatal("EndedAt 初始应为零值")
	}
	tr.Log()
	if tr.EndedAt.IsZero() {
		t.Fatal("Log 后 EndedAt 应被设置")
	}
	if tr.EndedAt.Before(tr.StartedAt) {
		t.Fatal("EndedAt 不应早于 StartedAt")
	}
}

// 并发安全：引擎中多 goroutine（流式转发 + 主流程）可能同时写入
func TestConcurrentAccess(t *testing.T) {
	tr := New(1)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				tr.StartSegment("upstream")
				tr.SetDetail("route", map[string]any{"i": i})
				tr.AddAttempt(Attempt{ProviderID: int64(i)})
				tr.EndSegment("upstream", "ok", nil)
				_ = tr.JSON()
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if len(tr.Attempts) != 400 {
		t.Fatalf("Attempts = %d, want 400", len(tr.Attempts))
	}
}
