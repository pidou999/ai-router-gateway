package db

import (
	"database/sql"

	"ai-router-gateway/internal/crypto"
)

// MigrateEncryption 将存量落库的弱加密密钥（旧版 base64+XOR）就地升级为 AES-GCM。
// 覆盖列：
//   - accounts.api_key_encrypted（服务商账号密钥，建表时即存在）
//   - api_keys.key_encrypted（用户 API Key 完整密钥，由 db.go ALTER 补列）
//
// 幂等：已带 "gcm." 前缀的行直接跳过；可重复调用。返回成功升级的行数。
// 任一列不存在（老库结构）时跳过该列而非报错，保证向前兼容。
func MigrateEncryption(database *sql.DB, masterKey string) (int, error) {
	total := 0

	tables := []struct {
		table string
		col   string
	}{
		{"accounts", "api_key_encrypted"},
		{"api_keys", "key_encrypted"},
	}

	for _, t := range tables {
		if !columnExists(database, t.table, t.col) {
			continue
		}
		n, err := migrateColumn(database, masterKey, t.table, t.col)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// columnExists 通过 PRAGMA table_info 判断表是否存在指定列（SQLite 兼容）。
func columnExists(database *sql.DB, table, col string) bool {
	rows, err := database.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}

// migrateColumn 把单列中仍为旧格式的密文逐行升级为新格式。
func migrateColumn(database *sql.DB, masterKey, table, col string) (int, error) {
	query := "SELECT id, " + col + " FROM " + table
	rows, err := database.Query(query)
	if err != nil {
		return 0, err
	}

	type row struct {
		id  int64
		val string
	}
	var pending []row
	for rows.Next() {
		var id int64
		var val sql.NullString
		if err := rows.Scan(&id, &val); err != nil {
			rows.Close()
			return 0, err
		}
		if !val.Valid || val.String == "" || crypto.IsGCMFormat(val.String) {
			continue
		}
		pending = append(pending, row{id: id, val: val.String})
	}
	rows.Close()

	count := 0
	for _, r := range pending {
		plain, err := crypto.Decrypt(r.val, masterKey)
		if err != nil {
			// 解密失败（如密钥已轮换）跳过，避免破坏数据；双格式回退仍可读取。
			continue
		}
		enc, err := crypto.Encrypt(plain, masterKey)
		if err != nil {
			continue
		}
		upd := "UPDATE " + table + " SET " + col + " = ? WHERE id = ?"
		if _, err := database.Exec(upd, enc, r.id); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
