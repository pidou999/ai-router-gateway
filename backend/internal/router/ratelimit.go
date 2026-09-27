package router

import (
	"sync"
	"time"
)

// RateLimiter 按账户执行 RPM（每分钟请求数）/ TPM（每分钟 token 数）限流。
// 采用「固定窗口 + 内存计数」：每个账户维护一个按分钟滚动的窗口，窗口内累计
// 请求数与 token 数，超出配置阈值即视为超限，选路时通过 roundRobinSelect 的
// skipIf 谓词跳过该账户（与熔断共用同一跳过机制）。
//
// 设计取舍（对应 9Router 的用量双表思路，这里只需执行限流、不必落库）：
//   - 限流是瞬时控制面，按分钟窗口自然过期，无需跨重启持久化（与熔断的冷却不同）；
//   - 热路径全走内存 map，避免每次请求查库；
//   - 限流维度全为「账户级」（不区分模型）：账户的 RPM/TPM 配额天然是整账号口径。
type RateLimiter struct {
	mu      sync.Mutex
	windows map[int64]*rlWindow
}

type rlWindow struct {
	start time.Time
	rpm   int // 窗口内已发起的请求数
	tpm   int // 窗口内已消耗的 token 数
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: make(map[int64]*rlWindow)}
}

// window 返回账户当前有效窗口；若窗口已过期（>= 1 分钟）则重置。
func (rl *RateLimiter) window(accountID int64, now time.Time) *rlWindow {
	w := rl.windows[accountID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		w = &rlWindow{start: now}
		rl.windows[accountID] = w
	}
	return w
}

// Exceeded 只读检查：该账户当前是否已超限。某维度 limit<=0 表示该维度不限流。
// 必须在 skipIf 谓词里以只读方式调用，因为谓词会对每个候选账户求值，
// 只有最终被选中的账户才应被 RecordRequest 计入。
func (rl *RateLimiter) Exceeded(accountID int64, rpmLimit, tpmLimit int) bool {
	if rpmLimit <= 0 && tpmLimit <= 0 {
		return false
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	w := rl.window(accountID, now)
	if rpmLimit > 0 && w.rpm >= rpmLimit {
		return true
	}
	if tpmLimit > 0 && w.tpm >= tpmLimit {
		return true
	}
	return false
}

// RecordRequest 一次实际转发前调用，累加该账户窗口内的请求计数。
func (rl *RateLimiter) RecordRequest(accountID int64) {
	if accountID == 0 {
		return // 免费服务商（NoAuth）不计数
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	w := rl.window(accountID, now)
	w.rpm++
}

// RecordTokens 一次成功响应后调用，累加该账户窗口内的 token 消耗（用于 TPM 限流）。
func (rl *RateLimiter) RecordTokens(accountID int64, tokens int) {
	if accountID == 0 {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	w := rl.window(accountID, now)
	w.tpm += tokens
}
