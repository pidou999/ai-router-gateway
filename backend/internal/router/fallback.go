package router

import (
	"database/sql"

	"ai-router-gateway/internal/models"
)

const (
	TierSubscription = 0
	TierLowCost      = 1
	TierFree         = 2
)

type TierConfig struct {
	Name      string
	Providers []ProviderConfig
}

type ProviderConfig struct {
	Provider   models.Provider
	Accounts   []models.Account
	NoAuth     bool // 免费服务商无需 API Key
	PricingType string // free / free_trial / paid，用于用量计量时的成本估算
}

// providerRow 用于先读取所有服务商到内存，避免在 Rows 遍历中嵌套查询导致 SQLite 锁死。
type providerRow struct {
	ID           int64
	Name         string
	BaseURL      string
	APIType      string
	Enabled      bool
	Priority     int
	HealthStatus string
	PricingType  string
}

func getTierConfigs(db *sql.DB, userID int64) ([]TierConfig, error) {
	// 第一步：读取所有 enabled providers 到内存，立即关闭 Rows
	rows, err := db.Query(
		`SELECT id, name, base_url, api_type, enabled, priority, health_status,
		        COALESCE(pricing_type,'paid')
		 FROM providers WHERE enabled = 1 ORDER BY priority ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var provRows []providerRow
	for rows.Next() {
		var p providerRow
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIType, &enabled,
			&p.Priority, &p.HealthStatus, &p.PricingType); err != nil {
			return nil, err
		}
		p.Enabled = enabled == 1
		provRows = append(provRows, p)
	}
	// rows 在此关闭（defer），之后所有查询都是独立的

	// 第二步：逐个查 accounts（此时没有嵌套 Rows）
	var allProviders []ProviderConfig
	for _, p := range provRows {
		mp := models.Provider{
			ID: p.ID, Name: p.Name, BaseURL: p.BaseURL,
			APIType: p.APIType, Enabled: p.Enabled,
			Priority: p.Priority, HealthStatus: p.HealthStatus,
		}

		// 上游账户是网关共享的基础设施凭证，按 provider + enabled 选取，
		// 不按调用方 user_id 过滤（否则 A 用户创建的 key 无法被 B 用户的请求使用）。
		accRows, err := db.Query(
			`SELECT id, provider_id, user_id, name, api_key_encrypted,
			        rate_limit_rpm, rate_limit_tpm, enabled, cooldown_until, created_at, updated_at, extra_config
			 FROM accounts WHERE provider_id = ? AND enabled = 1`,
			p.ID)
		if err != nil {
			return nil, err
		}

		var accounts []models.Account
		for accRows.Next() {
			var a models.Account
			var accEnabled int
			if err := accRows.Scan(&a.ID, &a.ProviderID, &a.UserID, &a.Name, &a.APIKeyEncrypted,
				&a.RateLimitRPM, &a.RateLimitTPM, &accEnabled, &a.CooldownUntil, &a.CreatedAt, &a.UpdatedAt, &a.ExtraConfig); err != nil {
				accRows.Close()
				return nil, err
			}
			a.Enabled = accEnabled == 1
			accounts = append(accounts, a)
		}
		accRows.Close()

		noAuth := p.PricingType == "free"

		if len(accounts) > 0 || noAuth {
			allProviders = append(allProviders, ProviderConfig{
				Provider:    mp,
				Accounts:    accounts,
				NoAuth:      noAuth,
				PricingType: p.PricingType,
			})
		}
	}

	// 第三步：按层级分组
	var tiers []TierConfig
	for i := 0; i < 3; i++ {
		tiers = append(tiers, TierConfig{})
	}
	tiers[TierSubscription].Name = "订阅层"
	tiers[TierLowCost].Name = "低成本层"
	tiers[TierFree].Name = "免费层"

	for _, pc := range allProviders {
		switch {
		case pc.Provider.Priority < 100:
			tiers[TierSubscription].Providers = append(tiers[TierSubscription].Providers, pc)
		case pc.Provider.Priority < 200:
			tiers[TierLowCost].Providers = append(tiers[TierLowCost].Providers, pc)
		default:
			tiers[TierFree].Providers = append(tiers[TierFree].Providers, pc)
		}
	}

	return tiers, nil
}

// resolveModelProvider 从 models 表查找模型所属的 provider_id，以及该服务商原生的 model_id。
// 返回的 nativeModel 是上游实际需要的模型标识（通常带版本日期后缀），转发时应使用它而非客户端传入的短名。
func resolveModelProvider(db *sql.DB, modelID string) (int64, string, error) {
	var providerID int64
	var nativeModel string
	err := db.QueryRow(
		`SELECT provider_id, model_id FROM models WHERE enabled = 1
		 AND (display_name = ? OR model_id = ?) LIMIT 1`,
		modelID, modelID).Scan(&providerID, &nativeModel)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return providerID, nativeModel, nil
}
