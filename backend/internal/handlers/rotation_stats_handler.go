package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RotationStats 轮换统计数据
type RotationStats struct {
	AccountRotation AccountRotationStats `json:"account_rotation"`
	ModelRotation   ModelRotationStats   `json:"model_rotation"`
}

type AccountRotationStats struct {
	EnabledProviders      int                         `json:"enabled_providers"`
	TotalAccounts         int                         `json:"total_accounts"`
	MultiAccountProviders []MultiAccountProviderItem  `json:"multi_account_providers"`
}

type ModelRotationStats struct {
	EnabledProviders    int                       `json:"enabled_providers"`
	TotalModels         int                       `json:"total_models"`
	MultiModelProviders []MultiModelProviderItem  `json:"multi_model_providers"`
}

type MultiAccountProviderItem struct {
	ID           int64 `json:"id"`
	Name         string `json:"name"`
	AccountCount int   `json:"account_count"`
}

type MultiModelProviderItem struct {
	ID         int64 `json:"id"`
	Name       string `json:"name"`
	ModelCount int   `json:"model_count"`
}

// RotationStatsHandler 账号轮换和模型轮换统计处理器
type RotationStatsHandler struct {
	db *sql.DB
}

// NewRotationStatsHandler 创建轮换统计处理器
func NewRotationStatsHandler(db *sql.DB) *RotationStatsHandler {
	return &RotationStatsHandler{db: db}
}

// GetStats 返回账号轮换和模型轮换的统计数据
func (h *RotationStatsHandler) GetStats(c *gin.Context) {
	stats := RotationStats{}

	// 查询多账号服务商
	rows, err := h.db.Query(`
		SELECT p.id, p.name, COUNT(a.id) as acct_count
		FROM providers p
		JOIN accounts a ON a.provider_id = p.id AND a.enabled = 1
		GROUP BY p.id
		HAVING acct_count > 1
		ORDER BY acct_count DESC
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item MultiAccountProviderItem
			if err := rows.Scan(&item.ID, &item.Name, &item.AccountCount); err == nil {
				stats.AccountRotation.MultiAccountProviders = append(
					stats.AccountRotation.MultiAccountProviders, item,
				)
				stats.AccountRotation.EnabledProviders++
				stats.AccountRotation.TotalAccounts += item.AccountCount
			}
		}
	}

	// 查询多模型服务商
	rows2, err := h.db.Query(`
		SELECT p.id, p.name, COUNT(m.id) as model_count
		FROM providers p
		JOIN models m ON m.provider_id = p.id AND m.enabled = 1
		GROUP BY p.id
		HAVING model_count > 1
		ORDER BY model_count DESC
	`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var item MultiModelProviderItem
			if err := rows2.Scan(&item.ID, &item.Name, &item.ModelCount); err == nil {
				stats.ModelRotation.MultiModelProviders = append(
					stats.ModelRotation.MultiModelProviders, item,
				)
				stats.ModelRotation.EnabledProviders++
				stats.ModelRotation.TotalModels += item.ModelCount
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}
