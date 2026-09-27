package router

import (
	"testing"
	"time"
)

func TestRateLimiterNoLimit(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < 100; i++ {
		if rl.Exceeded(1, 0, 0) {
			t.Fatal("未配置限额时不应超限")
		}
		rl.RecordRequest(1)
	}
}

func TestRateLimiterRPMExceed(t *testing.T) {
	rl := NewRateLimiter()
	const acc int64 = 10
	const rpm = 3
	// 前 3 次不超限
	for i := 0; i < rpm; i++ {
		if rl.Exceeded(acc, rpm, 0) {
			t.Fatalf("第 %d 次请求不应超限", i+1)
		}
		rl.RecordRequest(acc)
	}
	// 第 4 次应超限
	if !rl.Exceeded(acc, rpm, 0) {
		t.Fatal("达到 RPM 上限后应判定超限")
	}
}

func TestRateLimiterTPMExceed(t *testing.T) {
	rl := NewRateLimiter()
	const acc int64 = 11
	const tpm = 100
	// 两次请求各消耗 60 token → 累计 120 > 100
	rl.RecordRequest(acc)
	rl.RecordTokens(acc, 60)
	if rl.Exceeded(acc, 0, tpm) {
		t.Fatal("累计 60 token 未达 TPM 上限，不应超限")
	}
	rl.RecordRequest(acc)
	rl.RecordTokens(acc, 60)
	if !rl.Exceeded(acc, 0, tpm) {
		t.Fatal("累计 120 token 超过 TPM 上限，应判定超限")
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	rl := NewRateLimiter()
	const acc int64 = 12
	const rpm = 2
	// 填满第一分钟窗口
	rl.RecordRequest(acc)
	rl.RecordRequest(acc)
	if !rl.Exceeded(acc, rpm, 0) {
		t.Fatal("窗口内应已超限")
	}
	// 模拟窗口过期（直接改底层窗口的 start）
	rl.mu.Lock()
	if w, ok := rl.windows[acc]; ok {
		w.start = w.start.Add(-time.Minute - time.Second)
	}
	rl.mu.Unlock()
	// 过期后窗口应被重置，不再超限
	if rl.Exceeded(acc, rpm, 0) {
		t.Fatal("窗口过期后应重置，不再超限")
	}
}

func TestRateLimiterSkipZeroAccount(t *testing.T) {
	rl := NewRateLimiter()
	// 免费服务商 accountID=0：RecordRequest/RecordTokens 应静默忽略，不 panic 也不计数
	rl.RecordRequest(0)
	rl.RecordTokens(0, 1000)
	if rl.Exceeded(0, 1, 1) {
		t.Fatal("accountID=0 不应被限流")
	}
}
