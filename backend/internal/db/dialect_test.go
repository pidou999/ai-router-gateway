package db

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDialectDetectSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d, err := DetectDialect(db)
	if err != nil {
		t.Fatalf("detect sqlite: %v", err)
	}
	if d != DialectSQLite {
		t.Errorf("expected SQLite, got %d", d)
	}
}

func TestDialectDetectEmptyDB(t *testing.T) {
	// 空 in-memory SQLite：无 users 表，DetectDialect 应返回 SQLite 而非报错
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d, err := DetectDialect(db)
	if err != nil {
		t.Fatalf("detect empty sqlite: %v", err)
	}
	if d != DialectSQLite {
		t.Errorf("expected SQLite for empty db, got %d", d)
	}
}

func TestUpsertSettingsSQL(t *testing.T) {
	cases := []struct {
		d   Dialect
		key string
	}{
		{DialectSQLite, "sqlite"},
		{DialectMySQL, "mysql"},
		{DialectPostgreSQL, "pg"},
	}
	for _, c := range cases {
		sql := UpsertSettingsSQL(c.d)
		switch c.d {
		case DialectSQLite:
			if !strings.Contains(sql, "ON CONFLICT(key)") {
				t.Errorf("[%s] missing ON CONFLICT", c.key)
			}
		case DialectMySQL:
			if !strings.Contains(sql, "ON DUPLICATE KEY UPDATE") {
				t.Errorf("[%s] missing ON DUPLICATE KEY UPDATE", c.key)
			}
		case DialectPostgreSQL:
			if !strings.Contains(sql, "ON CONFLICT (key)") {
				t.Errorf("[%s] missing PG ON CONFLICT", c.key)
			}
		}
	}
}

func TestNowFunc(t *testing.T) {
	if got := NowFunc(DialectSQLite); got != "datetime('now')" {
		t.Errorf("sqlite now = %q", got)
	}
	if got := NowFunc(DialectMySQL); got != "CURRENT_TIMESTAMP" {
		t.Errorf("mysql now = %q", got)
	}
	if got := NowFunc(DialectPostgreSQL); got != "CURRENT_TIMESTAMP" {
		t.Errorf("pg now = %q", got)
	}
}

func TestConflictActionSQL(t *testing.T) {
	sqliteSQL := ConflictActionSQL(DialectSQLite)
	if !strings.Contains(sqliteSQL, "INSERT OR IGNORE") {
		t.Errorf("sqlite conflict action wrong: %s", sqliteSQL)
	}
	mysqlSQL := ConflictActionSQL(DialectMySQL)
	if !strings.Contains(mysqlSQL, "INSERT IGNORE") {
		t.Errorf("mysql conflict action wrong: %s", mysqlSQL)
	}
	pgSQL := ConflictActionSQL(DialectPostgreSQL)
	if !strings.Contains(pgSQL, "ON CONFLICT (key) DO NOTHING") {
		t.Errorf("pg conflict action wrong: %s", pgSQL)
	}
}

func TestUpsertCooldownSQL(t *testing.T) {
	sql := UpsertCooldownSQL(DialectMySQL)
	if !strings.Contains(sql, "ON DUPLICATE KEY UPDATE") {
		t.Errorf("mysql cooldown SQL missing upsert: %s", sql)
	}
	sql = UpsertCooldownSQL(DialectPostgreSQL)
	if !strings.Contains(sql, "ON CONFLICT (account_id, model)") {
		t.Errorf("pg cooldown SQL missing upsert: %s", sql)
	}
}

func TestColumnExistsSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE foo (id INTEGER PRIMARY KEY, name TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if !colExists(db, "foo", "name", DialectSQLite) {
		t.Error("expected colExists=true for existing column")
	}
	if colExists(db, "foo", "nonexistent", DialectSQLite) {
		t.Error("expected colExists=false for missing column")
	}
	// 不存在的表也应返回 false（不 panic）
	if colExists(db, "no_such_table", "id", DialectSQLite) {
		t.Error("expected colExists=false for non-existent table")
	}
}
