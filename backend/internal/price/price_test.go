package price

import (
	"testing"
	"time"
)

func TestGetNotFound(t *testing.T) {
	// 空缓存应返回 nil
	if Get("nonexistent-model-xyz") != nil {
		t.Error("expected nil for unknown model")
	}
}

func TestEstimateCostUsesSyncedPrice(t *testing.T) {
	// 模拟一个同步结果
	mu.Lock()
	prices["gpt-4o"] = LLMPrice{Input: 2.5, Output: 10.0, Source: SourceSync, UpdatedAt: time.Now()}
	mu.Unlock()

	got := EstimateOutputCost("gpt-4o", 1_000_000)
	want := 10.0
	if got != want {
		t.Errorf("EstimateOutputCost(1M tokens) = %v, want %v", got, want)
	}

	got = EstimateInputCost("gpt-4o", 500_000)
	want = 1.25
	if got != want {
		t.Errorf("EstimateInputCost(500K tokens) = %v, want %v", got, want)
	}
}

func TestEstimateCostFallbackToZero(t *testing.T) {
	// 未同步的模型应返回 0（不是 $2/1M 占位）
	got := EstimateOutputCost("unknown-model", 1_000_000)
	if got != 0 {
		t.Errorf("expected 0 for unknown model, got %v", got)
	}
}

func TestCaseInsensitive(t *testing.T) {
	mu.Lock()
	prices["claude-sonnet-4-5"] = LLMPrice{Output: 15.0, Source: SourceSync, UpdatedAt: time.Now()}
	mu.Unlock()

	if Get("CLAUDE-SONNET-4-5") == nil {
		t.Error("expected case-insensitive lookup to find model")
	}
	if Get("Claude-Sonnet-4-5") == nil {
		t.Error("expected mixed-case lookup to find model")
	}
}

func TestLastSyncDefaults(t *testing.T) {
	// 从未同步时，LastSync 为零值，Error 为 nil
	if !LastSyncTime().IsZero() {
		t.Error("expected zero time before first sync")
	}
	if LastSyncError() != nil {
		t.Errorf("expected nil error before first sync, got %v", LastSyncError())
	}
}
