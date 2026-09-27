package router

import (
	"database/sql"
	"testing"
	"time"

	"ai-router-gateway/internal/db"
	"ai-router-gateway/internal/models"
	_ "modernc.org/sqlite"
)

// newTestBreaker 开一个临时 SQLite，建好熔断所需的两张表，返回可用断路器（避免依赖全量迁移）。
func newTestBreaker(t *testing.T) *CircuitBreaker {
	t.Helper()
	sdb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	stmts := []string{
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY, cooldown_until DATETIME)`,
		`CREATE TABLE account_model_cooldowns (
			account_id INTEGER NOT NULL,
			model TEXT NOT NULL,
			cooldown_until DATETIME,
			consecutive_failures INTEGER DEFAULT 0,
			PRIMARY KEY (account_id, model)
		)`,
	}
	for _, s := range stmts {
		if _, err := sdb.Exec(s); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return NewCircuitBreaker(sdb, db.DialectSQLite)
}

func TestBackoffFor(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second},
		{4, 240 * time.Second},
		{100, 5 * time.Minute}, // 封顶
	}
	for _, c := range cases {
		got := backoffFor(c.failures)
		if got != c.want {
			t.Errorf("backoffFor(%d)=%v, want %v", c.failures, got, c.want)
		}
	}
}

func TestBreakerModelCooling(t *testing.T) {
	cb := newTestBreaker(t)
	const acc int64 = 7

	// 429 触发账号×模型级冷却
	cb.RecordFailure(acc, "gpt-4o", 429)
	if !cb.IsCooling(acc, "gpt-4o") {
		t.Error("429 后 gpt-4o 应处于冷却")
	}
	// 同账号其他模型不受影响（账号×模型粒度）
	if cb.IsCooling(acc, "gpt-3.5") {
		t.Error("gpt-3.5 不应被 gpt-4o 的故障牵连")
	}
	// 整账号级未触发
	if cb.IsAccountCooling(acc) {
		t.Error("429 不应触发整账号冷却")
	}

	// 成功命中后重置
	cb.RecordSuccess(acc, "gpt-4o")
	if cb.IsCooling(acc, "gpt-4o") {
		t.Error("成功后应清除该模型的冷却")
	}
}

func TestBreakerAccountCoolingOnAuthFail(t *testing.T) {
	cb := newTestBreaker(t)
	const acc int64 = 9

	// 401 触发整账号冷却
	cb.RecordFailure(acc, "any-model", 401)
	if !cb.IsAccountCooling(acc) {
		t.Error("401 应触发整账号冷却")
	}
	// 账号下所有模型都被屏蔽
	if !cb.IsCooling(acc, "any-model") {
		t.Error("整账号冷却下任意模型都应被屏蔽")
	}
}

func TestBreakerIgnoresClientError(t *testing.T) {
	cb := newTestBreaker(t)
	const acc int64 = 11
	// 400/404 不应熔断
	cb.RecordFailure(acc, "m", 400)
	cb.RecordFailure(acc, "m", 404)
	if cb.IsCooling(acc, "m") {
		t.Error("4xx 客户端错误不应触发熔断")
	}
}

func TestBreakerExpiryCleanup(t *testing.T) {
	cb := newTestBreaker(t)
	const acc int64 = 13
	// 直接塞一个已过期的 entry（同包可访问私有字段）
	cb.entries[acc] = map[string]*cooldownEntry{
		"m": {until: time.Now().Add(-time.Minute), failures: 3},
	}
	if cb.IsCooling(acc, "m") {
		t.Error("已过期条目应判定为未冷却")
	}
	// 过期条目应被清理
	if _, ok := cb.entries[acc]["m"]; ok {
		t.Error("过期条目应被自动清理")
	}
}

func TestBreakerTransportErrorCooldown(t *testing.T) {
	cb := newTestBreaker(t)
	const acc int64 = 15
	// 状态码 0 表示传输层错误，应冷却该模型
	cb.RecordFailure(acc, "m", 0)
	if !cb.IsCooling(acc, "m") {
		t.Error("传输错误应触发模型级冷却")
	}
}

func TestBreakerPersistAndLoad(t *testing.T) {
	sdb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	for _, s := range []string{
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY, cooldown_until DATETIME)`,
		`CREATE TABLE account_model_cooldowns (
			account_id INTEGER NOT NULL, model TEXT NOT NULL,
			cooldown_until DATETIME, consecutive_failures INTEGER DEFAULT 0,
			PRIMARY KEY (account_id, model))`,
	} {
		if _, err := sdb.Exec(s); err != nil {
			t.Fatal(err)
		}
	}

	cb1 := NewCircuitBreaker(sdb, db.DialectSQLite)
	cb1.RecordFailure(42, "model-x", 429)
	// 落库后，新建一个断路器 Load() 应能恢复
	cb2 := NewCircuitBreaker(sdb, db.DialectSQLite)
	cb2.Load()
	if !cb2.IsCooling(42, "model-x") {
		t.Error("重启 Load 后未能恢复模型熔断状态")
	}
}

func TestRoundRobinFailOpen(t *testing.T) {
	cb := newTestBreaker(t)
	const acc int64 = 21
	// 把所有账户都标记冷却（同包访问私有字段）
	cb.entries[acc] = map[string]*cooldownEntry{
		"m": {until: time.Now().Add(time.Minute), failures: 1},
	}
	// 该账号下任意模型都冷却；roundRobinSelect 应 fail-open 放行其一而非报错
	accounts := []models.Account{
		{ID: acc}, {ID: acc},
	}
	// 用一个 skipIf 让全部被跳过
	counter := 0
	got, err := roundRobinSelect(1, accounts, &counter, func(a models.Account) bool {
		return cb.IsCooling(a.ID, "m")
	})
	if err != nil {
		t.Fatalf("fail-open 下不应报错，got: %v", err)
	}
	if got.ID != acc {
		t.Errorf("fail-open 应返回被跳过的账户，got id=%d", got.ID)
	}
}
