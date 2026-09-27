package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Port          string
	DBType        string // "sqlite" | "mysql" | "postgres"
	DBPath        string // SQLite 文件路径，或 MySQL/PG 的 DSN
	JWTSecret     string
	EncryptionKey string
	DefaultTimeout string
}

func Load() *Config {
	dbType := strings.TrimSpace(getEnv("DB_TYPE", "sqlite"))
	dbPath := strings.TrimSpace(getEnv("DB_PATH", "./data/gateway.db"))
	// DB_PATH 同时承担 DSN 角色（MySQL/PG 时传连接串）
	cfg := &Config{
		Port:           strings.TrimSpace(getEnv("PORT", "5176")),
		DBType:         dbType,
		DBPath:         dbPath,
		JWTSecret:      os.Getenv("JWT_SECRET"),
		EncryptionKey:  strings.TrimSpace(getEnv("ENCRYPTION_KEY", "")),
		DefaultTimeout: strings.TrimSpace(getEnv("DEFAULT_TIMEOUT", "30")),
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 配置错误：%v\n", err)
		os.Exit(1)
	}
	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func (c *Config) Validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("环境变量 JWT_SECRET 未设置（必填，用于签发/验证 JWT）")
	}
	if c.EncryptionKey == "" {
		return fmt.Errorf("环境变量 ENCRYPTION_KEY 未设置（必填，用于 AES-GCM 密钥加密，建议至少 32 字节）")
	}
	if len(c.EncryptionKey) < 32 {
		return fmt.Errorf("ENCRYPTION_KEY 长度不足 32 字节（当前 %d），请设置更长更随机的密钥", len(c.EncryptionKey))
	}
	switch c.DBType {
	case "sqlite", "mysql", "postgres":
		// 合法
	default:
		return fmt.Errorf("DB_TYPE 无效，仅支持 sqlite / mysql / postgres（当前: %s）", c.DBType)
	}
	if c.DBType != "sqlite" && c.DBPath == "" {
		return fmt.Errorf("DB_TYPE=%s 时 DB_PATH 必须提供 DSN 连接串", c.DBType)
	}
	return nil
}
