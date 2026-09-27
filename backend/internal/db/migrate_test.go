package db

import (
	"database/sql"
	"encoding/base64"
	"testing"

	"ai-router-gateway/internal/crypto"
)

// legacyEncrypt 复刻旧版「base64 + 前 N 字节 XOR」编码，模拟存量数据。
func legacyEncrypt(plain, key string) string {
	data := []byte(plain)
	k := []byte(key)
	for i := 0; i < len(data) && i < len(k); i++ {
		data[i] ^= k[i]
	}
	return base64.StdEncoding.EncodeToString(data)
}

func TestMigrateEncryption(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	// 内存库需单连接，避免池内多连接各自独立的 :memory: 实例。
	database.SetMaxOpenConns(1)

	stmts := []string{
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY, api_key_encrypted TEXT)`,
		`CREATE TABLE api_keys (id INTEGER PRIMARY KEY, key_encrypted TEXT)`,
	}
	for _, s := range stmts {
		if _, err := database.Exec(s); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	key := "change-me-in-production"
	legacyAcc := legacyEncrypt("ak-account-secret", key)
	legacyKey := legacyEncrypt("uk-user-secret", key)
	if _, err := database.Exec("INSERT INTO accounts (id, api_key_encrypted) VALUES (1, ?)", legacyAcc); err != nil {
		t.Fatalf("insert accounts: %v", err)
	}
	if _, err := database.Exec("INSERT INTO api_keys (id, key_encrypted) VALUES (1, ?)", legacyKey); err != nil {
		t.Fatalf("insert api_keys: %v", err)
	}

	n, err := MigrateEncryption(database, key)
	if err != nil {
		t.Fatalf("MigrateEncryption: %v", err)
	}
	if n != 2 {
		t.Fatalf("应升级 2 行，实际 %d", n)
	}

	// accounts 已升级为 gcm. 且可解密还原
	var aEnc string
	if err := database.QueryRow("SELECT api_key_encrypted FROM accounts WHERE id=1").Scan(&aEnc); err != nil {
		t.Fatalf("query accounts: %v", err)
	}
	if !crypto.IsGCMFormat(aEnc) {
		t.Fatalf("accounts 未升级为 gcm. 格式: %s", aEnc)
	}
	pt, err := crypto.Decrypt(aEnc, key)
	if err != nil || pt != "ak-account-secret" {
		t.Fatalf("accounts 升级后解密失败: pt=%q err=%v", pt, err)
	}

	// api_keys 同样已升级
	var kEnc string
	if err := database.QueryRow("SELECT key_encrypted FROM api_keys WHERE id=1").Scan(&kEnc); err != nil {
		t.Fatalf("query api_keys: %v", err)
	}
	if !crypto.IsGCMFormat(kEnc) {
		t.Fatalf("api_keys 未升级为 gcm. 格式: %s", kEnc)
	}

	// 二次迁移应幂等（已无旧格式行，升级 0 行）
	n2, err := MigrateEncryption(database, key)
	if err != nil {
		t.Fatalf("MigrateEncryption 2nd: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("二次迁移应幂等为 0，实际 %d", n2)
	}
}

func TestMigrateEncryptionMissingColumn(t *testing.T) {
	// 老库可能不存在 key_encrypted 列，迁移应跳过而非报错。
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)

	if _, err := database.Exec(`CREATE TABLE accounts (id INTEGER PRIMARY KEY, api_key_encrypted TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	key := "change-me-in-production"
	if _, err := database.Exec("INSERT INTO accounts (id, api_key_encrypted) VALUES (1, ?)", legacyEncrypt("x", key)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	n, err := MigrateEncryption(database, key)
	if err != nil {
		t.Fatalf("迁移应容忍缺失列: %v", err)
	}
	if n != 1 {
		t.Fatalf("accounts 表应升级 1 行，实际 %d", n)
	}
}
