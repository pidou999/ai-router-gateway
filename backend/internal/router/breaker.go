package router

import (
	"database/sql"
	"sync"
	"time"

	"ai-router-gateway/internal/db"
)

// 被动熔断（circuit breaker）参数。对应 9Router 的「被动熔断 + fail-open」思路：
// 一次失败即冷却，指数退避，但全部冷却时仍放行其一（优先可用性）。
const (
	cbBaseBackoff = 30 * time.Second
	cbMaxBackoff  = 5 * time.Minute
)

// cooldownEntry 记录某「账号×模型」组合的冷却到期时刻与连续失败次数。
// model 为空字符串 "" 表示整账号级冷却（鉴权失效）。
type cooldownEntry struct {
	until    time.Time
	failures int
}

// CircuitBreaker 维护账号×模型粒度的熔断状态。内存为主、DB 为辅：
//   - 热路径（选路/失败标记）走内存 map，避免每次请求查库；
//   - 状态变化异步落到 account_model_cooldowns（模型级）与 accounts.cooldown_until（账号级），
//     进程重启时由 Load() 恢复到内存，保证熔断跨重启持续生效。
type CircuitBreaker struct {
	mu      sync.Mutex
	entries map[int64]map[string]*cooldownEntry // accountID -> (model -> entry)
	db      *sql.DB
	dialect db.Dialect
}

func NewCircuitBreaker(db *sql.DB, dialect db.Dialect) *CircuitBreaker {
	return &CircuitBreaker{
		entries: make(map[int64]map[string]*cooldownEntry),
		db:      db,
		dialect: dialect,
	}
}

// Load 进程启动时把未过期的熔断记录载入内存，使熔断跨重启持续生效。
func (cb *CircuitBreaker) Load() {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := cb.db.Query(
		"SELECT account_id, model, cooldown_until, consecutive_failures FROM account_model_cooldowns WHERE cooldown_until > ?",
		now,
	)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var accountID int64
		var model, untilStr string
		var failures int
		if err := rows.Scan(&accountID, &model, &untilStr, &failures); err != nil {
			continue
		}
		until, err := time.Parse(time.RFC3339, untilStr)
		if err != nil {
			continue
		}
		if !until.After(time.Now()) {
			continue
		}
		cb.mu.Lock()
		am := cb.entries[accountID]
		if am == nil {
			am = make(map[string]*cooldownEntry)
			cb.entries[accountID] = am
		}
		am[model] = &cooldownEntry{until: until, failures: failures}
		cb.mu.Unlock()
	}
}

// IsCooling 某「账号×模型」当前是否处于冷却中。model 用空串 "" 可查整账号级冷却。
// 整账号级（key=""）冷却会屏蔽该账号下所有模型。已过期会自动清理并返回 false。
func (cb *CircuitBreaker) IsCooling(accountID int64, model string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := time.Now()
	am := cb.entries[accountID]
	if am == nil {
		return false
	}
	// 整账号级冷却：屏蔽该账号下所有模型
	if ent, ok := am[""]; ok {
		if ent.until.After(now) {
			return true
		}
		delete(am, "") // 过期清理
	}
	if model == "" {
		return false
	}
	ent := am[model]
	if ent == nil {
		return false
	}
	if ent.until.After(now) {
		return true
	}
	// 已过期，顺手清理
	delete(am, model)
	return false
}

// IsAccountCooling 整账号级冷却（model=""）。
func (cb *CircuitBreaker) IsAccountCooling(accountID int64) bool {
	return cb.IsCooling(accountID, "")
}

// RecordFailure 记录一次失败，按状态码决定熔断粒度：
//   - 429 / 500 / 502 / 503 / 504 / 0（传输层错误）→ 账号×模型级：仅该模型冷却
//   - 401 / 403 → 整账号级：鉴权失效，账号下所有模型都不可用
//   - 其他 4xx（400/404/413 等）→ 不熔断：换账号也救不了，避免误杀健康凭证
func (cb *CircuitBreaker) RecordFailure(accountID int64, model string, statusCode int) {
	if accountID == 0 {
		return // 免费服务商（NoAuth）不做熔断
	}
	switch statusCode {
	case 401, 403:
		cb.setAccount(accountID)
	case 429, 500, 502, 503, 504, 0:
		cb.increment(accountID, model)
	default:
		// 4xx 客户端错误不熔断
	}
}

// RecordSuccess 某次成功命中后，重置该「账号×模型」的连失败计数（证明已恢复健康）。
func (cb *CircuitBreaker) RecordSuccess(accountID int64, model string) {
	if accountID == 0 {
		return
	}
	cb.mu.Lock()
	am := cb.entries[accountID]
	if am != nil {
		if _, ok := am[model]; ok {
			delete(am, model)
		}
	}
	cb.mu.Unlock()
	// 落库删除，保证重启后不会残留过期熔断
	_, _ = cb.db.Exec(
		"DELETE FROM account_model_cooldowns WHERE account_id = ? AND model = ?",
		accountID, model,
	)
}

// ---- 内部实现 ----

// touch 在锁内更新「账号×模型」的连失败与到期，返回最新 entry。
func (cb *CircuitBreaker) touch(accountID int64, model string) *cooldownEntry {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := time.Now()
	am := cb.entries[accountID]
	if am == nil {
		am = make(map[string]*cooldownEntry)
		cb.entries[accountID] = am
	}
	ent := am[model]
	if ent == nil {
		ent = &cooldownEntry{}
		am[model] = ent
	}
	ent.failures++
	ent.until = now.Add(backoffFor(ent.failures))
	return ent
}

func (cb *CircuitBreaker) increment(accountID int64, model string) {
	ent := cb.touch(accountID, model)
	_, _ = cb.db.Exec(
		db.UpsertCooldownSQL(cb.dialect),
		accountID, model, ent.until.UTC().Format(time.RFC3339), ent.failures,
	)
}

func (cb *CircuitBreaker) setAccount(accountID int64) {
	ent := cb.touch(accountID, "")
	// 整账号级冷却同时写 accounts.cooldown_until，使 roundRobinSelect 的既有
	// Account.CooldownUntil 过滤（DB 重载后）也能生效。
	_, _ = cb.db.Exec(
		"UPDATE accounts SET cooldown_until = ? WHERE id = ?",
		ent.until.UTC().Format(time.RFC3339), accountID,
	)
}

// backoffFor 指数退避：base、base*2、base*4…，上限 maxBackoff。
// 用迭代而非 2^n 幂运算，避免 n 较大时 int64 溢出导致结果变负。
func backoffFor(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	d := cbBaseBackoff
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= cbMaxBackoff {
			return cbMaxBackoff
		}
	}
	if d > cbMaxBackoff {
		return cbMaxBackoff
	}
	return d
}
