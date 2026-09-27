package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// Dialect 代表底层数据库方言。
type Dialect int

const (
	DialectSQLite Dialect = iota
	DialectMySQL
	DialectPostgreSQL
)

// DetectDialect 通过无害查询确定当前连接的方言。
// 优先级：PRAGMA（SQLite 独有）> information_schema（MySQL/PG 共有）。
// 空库时保守返回 SQLite，由调用方根据 DB_TYPE 覆盖。
func DetectDialect(db *sql.DB) (Dialect, error) {
	// 1) PRAGMA 是 SQLite 独有；MySQL/PG 会报 "no such function" 或类似错误
	var pragmaOK int
	if err := db.QueryRow("PRAGMA table_info(users)").Scan(&pragmaOK); err == nil {
		return DialectSQLite, nil
	}

	// 2) information_schema.columns → MySQL / PostgreSQL 都支持
	var colName string
	if err := db.QueryRow(
		"SELECT COLUMN_NAME FROM information_schema.columns WHERE table_name='users' LIMIT 1",
	).Scan(&colName); err == nil {
		// 区分 MySQL 和 PostgreSQL：MySQL 有 TABLE_OPTIONS/ENGINE，PG 没有
		var engine string
		if err := db.QueryRow(
			"SELECT TABLE_OPTIONS FROM information_schema.tables WHERE table_name='users'",
		).Scan(&engine); err == nil && strings.Contains(strings.ToLower(engine), "engine") {
			return DialectMySQL, nil
		}
		return DialectPostgreSQL, nil
	}

	// 3) 都没有（空库）：保守返回 SQLite
	return DialectSQLite, nil
}

// UpsertSettingsSQL 返回「INSERT 或更新 settings 表」的方言感知 SQL。
// 参数 placeholders 使用 ?（所有方言都支持）；excluded 在各方言中的写法不同。
func UpsertSettingsSQL(d Dialect) string {
	switch d {
	case DialectMySQL:
		// MySQL: INSERT ... ON DUPLICATE KEY UPDATE
		return `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		        ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)`
	case DialectPostgreSQL:
		// PostgreSQL: INSERT ... ON CONFLICT DO UPDATE（ USING 别名 conflict 而非 excluded ）
		return `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
		        ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`
	default:
		// SQLite: INSERT ... ON CONFLICT DO UPDATE
		return `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		        ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`
	}
}

// UpsertUsageStatsSQL 返回「INSERT 或更新 usage_stats 表」的方言感知 SQL。
func UpsertUsageStatsSQL(d Dialect) string {
	switch d {
	case DialectMySQL:
		return `INSERT INTO usage_stats (user_id, date, provider_id, model, request_count, total_tokens, estimated_cost)
		        VALUES (?, ?, ?, ?, 1, ?, ?)
		        ON DUPLICATE KEY UPDATE
		          request_count = request_count + 1,
		          total_tokens  = total_tokens + VALUES(total_tokens),
		          estimated_cost = estimated_cost + VALUES(estimated_cost)`
	case DialectPostgreSQL:
		return `INSERT INTO usage_stats (user_id, date, provider_id, model, request_count, total_tokens, estimated_cost)
		        VALUES ($1, $2, $3, $4, 1, $5, $6)
		        ON CONFLICT (user_id, date, provider_id, model) DO UPDATE SET
		          request_count = usage_stats.request_count + 1,
		          total_tokens  = usage_stats.total_tokens + EXCLUDED.total_tokens,
		          estimated_cost = usage_stats.estimated_cost + EXCLUDED.estimated_cost`
	default:
		return `INSERT INTO usage_stats (user_id, date, provider_id, model, request_count, total_tokens, estimated_cost)
		        VALUES (?, ?, ?, ?, 1, ?, ?)
		        ON CONFLICT(user_id, date, provider_id, model) DO UPDATE SET
		          request_count = excluded.request_count + 1,
		          total_tokens  = excluded.total_tokens + usage_stats.total_tokens,
		          estimated_cost = excluded.estimated_cost + usage_stats.estimated_cost`
	}
}

// NowFunc 返回方言感知的「当前时间」函数。
func NowFunc(d Dialect) string {
	switch d {
	case DialectMySQL, DialectPostgreSQL:
		return "CURRENT_TIMESTAMP"
	default:
		return "datetime('now')"
	}
}

// ConflictActionSQL 返回「UPSERT 失败时直接忽略」的方言 SQL（用于幂等插入，如 incremental seeds 标记）。
// MySQL 用 INSERT IGNORE，PostgreSQL 用 ON CONFLICT DO NOTHING，SQLite 用 INSERT OR IGNORE。
func ConflictActionSQL(d Dialect) string {
	switch d {
	case DialectMySQL:
		return "INSERT IGNORE INTO settings (key, value, updated_at) VALUES (?, 'done', CURRENT_TIMESTAMP)"
	case DialectPostgreSQL:
		return `INSERT INTO settings (key, value, updated_at) VALUES ($1, 'done', CURRENT_TIMESTAMP)
		        ON CONFLICT (key) DO NOTHING`
	default:
		return "INSERT OR IGNORE INTO settings (key, value, updated_at) VALUES (?, 'done', datetime('now'))"
	}
}

// ColumnExistsSQL 返回「检查列是否存在」的方言 SQL。
func ColumnExistsSQL(d Dialect, table, col string) string {
	switch d {
	case DialectPostgreSQL:
		return fmt.Sprintf(`SELECT COUNT(*) FROM information_schema.columns
		                     WHERE table_name = %s AND column_name = %s AND table_schema = 'public'`,
			sqlQuote(table), sqlQuote(col))
	case DialectMySQL:
		return fmt.Sprintf("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = %s AND column_name = %s",
			sqlQuote(table), sqlQuote(col))
	default:
		// SQLite: PRAGMA table_info
		return fmt.Sprintf("PRAGMA table_info(%s)", table)
	}
}

// AddColumnIfNotExistsSQL 返回「若列不存在则 ALTER TABLE ADD COLUMN」的方言 SQL。
// 注意：MySQL/PG 不支持 IF NOT EXISTS 直接写在 ALTER 里，需要应用层判断后执行。
// 此函数仅用于 SQLite（方言内原子操作），其他方言由调用方先查 columnExists 再决定。
func AddColumnIfNotExistsSQL(d Dialect, table, colDef string) string {
	if d == DialectSQLite {
		return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, colDef)
	}
	// MySQL/PG：调用方应先用 ColumnExistsSQL 检查，再决定是否执行 ADD COLUMN
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, colDef)
}

// UpsertCooldownSQL 返回「INSERT 或更新 account_model_cooldowns」的方言 SQL。
func UpsertCooldownSQL(d Dialect) string {
	switch d {
	case DialectMySQL:
		return `INSERT INTO account_model_cooldowns (account_id, model, cooldown_until, consecutive_failures)
		        VALUES (?, ?, ?, ?)
		        ON DUPLICATE KEY UPDATE
		          cooldown_until = VALUES(cooldown_until),
		          consecutive_failures = VALUES(consecutive_failures)`
	case DialectPostgreSQL:
		return `INSERT INTO account_model_cooldowns (account_id, model, cooldown_until, consecutive_failures)
		        VALUES ($1, $2, $3, $4)
		        ON CONFLICT (account_id, model) DO UPDATE SET
		          cooldown_until = EXCLUDED.cooldown_until,
		          consecutive_failures = EXCLUDED.consecutive_failures`
	default:
		return `INSERT INTO account_model_cooldowns (account_id, model, cooldown_until, consecutive_failures)
		        VALUES (?, ?, ?, ?)
		        ON CONFLICT(account_id, model) DO UPDATE SET
		          cooldown_until = excluded.cooldown_until,
		          consecutive_failures = excluded.consecutive_failures`
	}
}

// sqlQuote 对标识符做最小化处理（用双引号包裹，防止 SQL 注入）。
// 仅用于内部生成的 SQL，传入值应来自代码常量，不来自用户输入。
func sqlQuote(s string) string {
	return fmt.Sprintf("'%s'", strings.ReplaceAll(s, "'", "''"))
}
