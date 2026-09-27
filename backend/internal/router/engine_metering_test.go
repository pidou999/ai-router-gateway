package router

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newMeteringDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "meter_test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := `CREATE TABLE usage_stats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER,
		date TEXT,
		provider_id INTEGER,
		model TEXT,
		request_count INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		estimated_cost REAL DEFAULT 0.0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func TestEstimateCost(t *testing.T) {
	cases := []struct {
		prompt      int
		completion  int
		ptype       string
		model       string
		want        float64
	}{
		{500_000, 500_000, "paid", "unknown-model", 2.0},   // fallback: $2/1M
		{500_000, 500_000, "free", "gpt-4o", 0.0},
		{500_000, 500_000, "free_trial", "gpt-4o", 0.0},
		{0, 0, "paid", "gpt-4o", 0.0},
	}
	for _, c := range cases {
		got := estimateCost(c.prompt, c.completion, c.ptype, c.model)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("estimateCost(%d,%d,%q,%q)=%v want %v",
				c.prompt, c.completion, c.ptype, c.model, got, c.want)
		}
	}
}

func TestRecordUsageStatsUpsert(t *testing.T) {
	db := newMeteringDB(t)

	// 第一次写入：paid，10 prompt + 5 completion
	recordUsageStats(db, 1, 1, "gpt-4", "paid", 10, 5)
	// 第二次写入：相同 (date,user,provider,model) → 应聚合到同一行
	recordUsageStats(db, 1, 1, "gpt-4", "paid", 20, 10)

	var reqCount, total int
	var cost float64
	if err := db.QueryRow(
		"SELECT request_count, total_tokens, estimated_cost FROM usage_stats WHERE user_id=1 AND provider_id=1 AND model='gpt-4'",
	).Scan(&reqCount, &total, &cost); err != nil {
		t.Fatalf("query: %v", err)
	}
	if reqCount != 2 {
		t.Errorf("request_count = %d, want 2", reqCount)
	}
	if total != 45 { // (10+5)+(20+10)
		t.Errorf("total_tokens = %d, want 45", total)
	}
	wantCost := 45.0 / 1e6 * 2.0
	if math.Abs(cost-wantCost) > 1e-9 {
		t.Errorf("estimated_cost = %v, want %v", cost, wantCost)
	}
}

func TestRecordUsageStatsFreeZeroCost(t *testing.T) {
	db := newMeteringDB(t)
	recordUsageStats(db, 1, 2, "local-model", "free", 100, 50)
	var cost float64
	var total int
	if err := db.QueryRow(
		"SELECT total_tokens, estimated_cost FROM usage_stats WHERE provider_id=2",
	).Scan(&total, &cost); err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 150 {
		t.Errorf("total_tokens = %d, want 150", total)
	}
	if cost != 0 {
		t.Errorf("estimated_cost = %v, want 0 for free tier", cost)
	}
}

func TestRecordUsageStatsSeparateRows(t *testing.T) {
	db := newMeteringDB(t)
	// 不同 model → 不同聚合行
	recordUsageStats(db, 1, 1, "gpt-4", "paid", 10, 5)
	recordUsageStats(db, 1, 1, "gpt-3.5", "paid", 7, 3)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM usage_stats").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("row count = %d, want 2", n)
	}
}

func TestRecordUsageStatsSkipsZero(t *testing.T) {
	db := newMeteringDB(t)
	// 0 token 的请求不应写入聚合
	recordUsageStats(db, 1, 1, "gpt-4", "paid", 0, 0)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM usage_stats").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("row count = %d, want 0 (zero-token requests skipped)", n)
	}
}
