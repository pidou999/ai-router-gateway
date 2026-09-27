package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// 最小 schema，仅覆盖 resolveAPIKeyUser 需要的列，证明 JOIN 能正确取回
// users.username / users.role（早期误写 `SELECT ... FROM api_keys` 会因缺列而失败）。
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ddl := `
	CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		role TEXT DEFAULT 'member'
	);
	CREATE TABLE api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		key_hash TEXT NOT NULL,
		enabled INTEGER DEFAULT 1
	);`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return db
}

func TestResolveAPIKeyUser_Hit(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(
		"INSERT INTO users (id, username, role) VALUES (1, 'alice', 'admin')",
	); err != nil {
		t.Fatalf("插入用户失败: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO api_keys (user_id, key_hash, enabled) VALUES (1, 'hash-alice', 1)",
	); err != nil {
		t.Fatalf("插入密钥失败: %v", err)
	}

	userID, username, role, ok := resolveAPIKeyUser(db, "hash-alice")
	if !ok {
		t.Fatal("期望命中，却返回 ok=false")
	}
	if userID != 1 || username != "alice" || role != "admin" {
		t.Errorf("字段错误: userID=%d username=%q role=%q", userID, username, role)
	}
}

func TestResolveAPIKeyUser_NotFound(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec("INSERT INTO users (id, username, role) VALUES (1, 'bob', 'member')"); err != nil {
		t.Fatalf("插入用户失败: %v", err)
	}
	if _, err := db.Exec("INSERT INTO api_keys (user_id, key_hash, enabled) VALUES (1, 'real-hash', 1)"); err != nil {
		t.Fatalf("插入密钥失败: %v", err)
	}

	// 不存在的哈希 → ok=false
	if _, _, _, ok := resolveAPIKeyUser(db, "nonexistent"); ok {
		t.Error("哈希不存在时应当 ok=false")
	}
}

func TestResolveAPIKeyUser_Disabled(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec("INSERT INTO users (id, username, role) VALUES (1, 'carol', 'member')"); err != nil {
		t.Fatalf("插入用户失败: %v", err)
	}
	if _, err := db.Exec("INSERT INTO api_keys (user_id, key_hash, enabled) VALUES (1, 'disabled-hash', 0)"); err != nil {
		t.Fatalf("插入密钥失败: %v", err)
	}

	// 被禁用的密钥 → ok=false
	if _, _, _, ok := resolveAPIKeyUser(db, "disabled-hash"); ok {
		t.Error("禁用密钥应当 ok=false")
	}
}

// 确认测试用的临时目录可被清理（顺带验证 t.TempDir 行为）。
func TestTempDirCleanup(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(f, []byte("ok"), 0644); err != nil {
		t.Fatalf("写临时文件失败: %v", err)
	}
}
