package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	// MySQL driver
	_ "github.com/go-sql-driver/mysql"
	// PostgreSQL driver
	_ "github.com/lib/pq"
	// SQLite driver（纯 Go，无 CGO）
	_ "modernc.org/sqlite"
)

// driverName 返回给定 dialect 对应的 database/sql driver name。
func driverName(d string) string {
	switch d {
	case "mysql":
		return "mysql"
	case "postgres", "postgresql":
		return "postgres"
	default:
		return "sqlite"
	}
}

// InitDB 打开数据库连接并按 dialect 执行迁移。
// dbType: "sqlite" | "mysql" | "postgres"
// dsn:    SQLite 时为文件路径；MySQL/PG 时为 DSN 连接串（如 "user:pass@tcp(host:port)/dbname"）。
func InitDB(dbType, dsn string) (*sql.DB, error) {
	driver := driverName(dbType)
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 数据库失败：%w", driver, err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库 Ping 失败：%w", err)
	}

	// 连接池配置（MySQL/PG 需要连接池，SQLite 保持单连接避免 WAL 竞争）
	switch driver {
	case "sqlite":
		db.SetMaxOpenConns(1)
	default:
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(5 * time.Minute)
	}

	// 检测实际方言（DSN 可能与 DB_TYPE 不一致时兜底）
	dialect, detectErr := DetectDialect(db)
	if detectErr != nil {
		// 降级：按 DB_TYPE 推断
		switch dbType {
		case "mysql":
			dialect = DialectMySQL
		case "postgres", "postgresql":
			dialect = DialectPostgreSQL
		default:
			dialect = DialectSQLite
		}
	}

	if err := RunMigrations(db, dialect); err != nil {
		db.Close()
		return nil, fmt.Errorf("执行数据库迁移失败：%w", err)
	}

	return db, nil
}

// RunMigrations 执行 DDL 建表 + 兼容旧库的 ALTER TABLE。
// dialect 由调用方传入（来自 InitDB 的检测），避免重复探测。
func RunMigrations(db *sql.DB, dialect Dialect) error {
	statements := sqliteStatements // 以 SQLite 为参考 schema（所有方言共享同一逻辑结构）
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			// MySQL/PG 下某些 SQLite 特有语法会报错，需要方言适配
			if dialect != DialectSQLite {
				if alt := dialectAwareDDL(stmt, dialect); alt != "" {
					if _, err2 := db.Exec(alt); err2 != nil {
						return fmt.Errorf("执行语句失败 [%s]：%w（原始：%s）", dialectLabel(dialect), err, stmt)
					}
					continue
				}
			}
			return err
		}
	}

	// 兼容旧库：增量 ALTER TABLE（按方言处理）
	if dialect == DialectSQLite {
		return runSQLiteAlters(db)
	}
	return runCrossDBAlters(db, dialect)
}

// ---- SQLite 原生 DDL（CREATE TABLE IF NOT EXISTS 在所有方言通用）----

var sqliteStatements = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		role TEXT DEFAULT 'member',
		team_id INTEGER,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		key_hash TEXT NOT NULL,
		key_prefix TEXT,
		scopes TEXT,
		enabled INTEGER DEFAULT 1,
		last_used DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`,
	`CREATE TABLE IF NOT EXISTS providers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		base_url TEXT NOT NULL,
		api_type TEXT NOT NULL,
		enabled INTEGER DEFAULT 1,
		priority INTEGER DEFAULT 0,
		health_status TEXT DEFAULT 'unknown',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		api_key_encrypted TEXT NOT NULL,
		rate_limit_rpm INTEGER,
		rate_limit_tpm INTEGER,
		enabled INTEGER DEFAULT 1,
		oauth_config TEXT,
		token_expiry DATETIME,
		cooldown_until DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (provider_id) REFERENCES providers(id),
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`,
	`CREATE TABLE IF NOT EXISTS account_model_cooldowns (
		account_id INTEGER NOT NULL,
		model TEXT NOT NULL,
		cooldown_until DATETIME,
		consecutive_failures INTEGER DEFAULT 0,
		PRIMARY KEY (account_id, model)
	)`,
	`CREATE TABLE IF NOT EXISTS combos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		config TEXT,
		enabled INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`,
	`CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER,
		provider_id INTEGER,
		account_id INTEGER,
		model TEXT,
		picked_model TEXT,
		request_tokens INTEGER,
		response_tokens INTEGER,
		latency_ms INTEGER,
		status TEXT,
		error_message TEXT,
		request_body TEXT,
		response_body TEXT,
		request_details TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		key TEXT UNIQUE NOT NULL,
		value TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS usage_stats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER,
		date TEXT,
		provider_id INTEGER,
		model TEXT,
		request_count INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		estimated_cost REAL DEFAULT 0.0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS models (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_id INTEGER NOT NULL,
		model_id TEXT NOT NULL,
		display_name TEXT,
		owned_by TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(provider_id, model_id),
		FOREIGN KEY (provider_id) REFERENCES providers(id)
	)`,
}

// runSQLiteAlters 执行仅 SQLite 需要的增量 ALTER（列不存在时添加）。
func runSQLiteAlters(db *sql.DB) error {
	alts := []string{
		"ALTER TABLE models ADD COLUMN enabled INTEGER DEFAULT 1",
		"ALTER TABLE models ADD COLUMN probed_caps TEXT",
		"ALTER TABLE models ADD COLUMN probed_at DATETIME",
		"ALTER TABLE models ADD COLUMN is_free INTEGER DEFAULT 0",
		"ALTER TABLE providers ADD COLUMN user_id INTEGER DEFAULT 1",
		"ALTER TABLE providers ADD COLUMN pricing_type TEXT DEFAULT 'paid'",
		"ALTER TABLE api_keys ADD COLUMN key_encrypted TEXT",
		"ALTER TABLE request_logs ADD COLUMN request_details TEXT",
		"ALTER TABLE request_logs ADD COLUMN picked_model TEXT",
		"ALTER TABLE accounts ADD COLUMN extra_config TEXT DEFAULT ''",
		"ALTER TABLE providers ADD COLUMN auto_sync INTEGER DEFAULT 0",
	}
	for _, stmt := range alts {
		// SQLite ALTER 列已存在时会报错，忽略即可
		if _, err := db.Exec(stmt); err != nil {
			// 忽略 "duplicate column" 类错误
			if !strings.Contains(err.Error(), "duplicate column") &&
				!strings.Contains(err.Error(), "already exists") {
				return err
			}
		}
	}
	return nil
}

// runCrossDBAlters 对 MySQL/PostgreSQL 执行兼容旧库的列添加（先查后加）。
func runCrossDBAlters(db *sql.DB, dialect Dialect) error {
	type alterReq struct {
		table string
		col   string
		def   string
	}
	alts := []alterReq{
		{"models", "enabled", "INTEGER DEFAULT 1"},
		{"models", "probed_caps", "TEXT"},
		{"models", "probed_at", "DATETIME"},
		{"models", "is_free", "INTEGER DEFAULT 0"},
		{"providers", "user_id", "INTEGER DEFAULT 1"},
		{"providers", "pricing_type", "TEXT DEFAULT 'paid'"},
		{"api_keys", "key_encrypted", "TEXT"},
		{"request_logs", "request_details", "TEXT"},
		{"request_logs", "picked_model", "TEXT"},
		{"accounts", "extra_config", "TEXT DEFAULT ''"},
	}
	for _, a := range alts {
		if colExists(db, a.table, a.col, dialect) {
			continue
		}
		colDef := a.col + " " + a.def
		if _, err := db.Exec("ALTER TABLE " + a.table + " ADD COLUMN " + colDef); err != nil {
			return fmt.Errorf("添加列 %s.%s 失败：%w", a.table, a.col, err)
		}
	}

	// 回填 providers.pricing_type（仅 SQLite 有此逻辑；MySQL/PG 通过 DEFAULT 已处理）
	if dialect == DialectSQLite {
		db.Exec(`UPDATE providers SET pricing_type = CASE
			WHEN api_type = 'ollama' THEN 'free'
			WHEN api_type IN ('openai','deepseek','qwen','siliconflow','moonshot','zhipu','glm','minimax','alibaba','volcengine','gemini','github','codebuddy') THEN 'free_trial'
			ELSE 'paid'
		END WHERE pricing_type IS NULL OR pricing_type = '' OR pricing_type = 'paid'`)
	}
	return nil
}

// dialectAwareDDL 对特定 SQLite DDL 生成 MySQL/PG 等效语句。
// 返回空字符串表示无需特殊处理（原语句可直接执行）。
func dialectAwareDDL(sqlStmt string, dialect Dialect) string {
	// SQLite 的 AUTOINCREMENT 在 MySQL/PG 中不需要（自增主键默认处理）
	if strings.Contains(sqlStmt, "AUTOINCREMENT") {
		stmt := strings.ReplaceAll(sqlStmt, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGINT PRIMARY KEY AUTO_INCREMENT")
		if dialect == DialectPostgreSQL {
			stmt = strings.ReplaceAll(stmt, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
			stmt = strings.ReplaceAll(stmt, "BIGINT PRIMARY KEY AUTO_INCREMENT", "BIGSERIAL PRIMARY KEY")
			// PG 的 DATETIME DEFAULT CURRENT_TIMESTAMP → TIMESTAMPTZ DEFAULT now()
			stmt = strings.ReplaceAll(stmt, "DATETIME DEFAULT CURRENT_TIMESTAMP", "TIMESTAMPTZ DEFAULT now()")
			stmt = strings.ReplaceAll(stmt, "INTEGER", "INTEGER") // 保持其他 INTEGER
		} else {
			// MySQL: DATETIME DEFAULT CURRENT_TIMESTAMP 保持不变；TEXT 保持不变
			stmt = strings.ReplaceAll(stmt, "DATETIME DEFAULT CURRENT_TIMESTAMP", "DATETIME DEFAULT CURRENT_TIMESTAMP")
		}
		return stmt
	}
	return ""
}

func dialectLabel(d Dialect) string {
	switch d {
	case DialectMySQL:
		return "mysql"
	case DialectPostgreSQL:
		return "postgres"
	default:
		return "sqlite"
	}
}

// colExists 跨方言检测列是否存在。
func colExists(db *sql.DB, table, col string, dialect Dialect) bool {
	query := ColumnExistsSQL(dialect, table, col)
	var count int
	if dialect == DialectSQLite {
		// PRAGMA 返回多行，每行 (cid, name, type, notnull, dflt_value, pk)
		// 注意：dflt_value 可以为 NULL，必须用 sql.NullString Scan
		rows, err := db.Query(query)
		if err != nil {
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dfltVal sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltVal, &pk); err != nil {
				return false
			}
			if name == col {
				return true
			}
		}
		return false
	}
	if err := db.QueryRow(query).Scan(&count); err != nil {
		return false
	}
	return count > 0
}
