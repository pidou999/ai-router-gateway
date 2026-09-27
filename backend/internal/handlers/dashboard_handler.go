package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"ai-router-gateway/internal/auth"

	"github.com/gin-gonic/gin"
)

type DashboardHandler struct {
	db *sql.DB
}

func NewDashboardHandler(db *sql.DB) *DashboardHandler {
	return &DashboardHandler{db: db}
}

func (h *DashboardHandler) GetStats(c *gin.Context) {
	userID := auth.GetUserID(c)

	today := time.Now().Format("2006-01-02")
	monthStart := time.Now().Format("2006-01") + "-01"

	var todayRequests, todayTokens, todayCost sql.NullFloat64
	h.db.QueryRow(
		"SELECT COALESCE(SUM(request_count), 0), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(estimated_cost), 0) FROM usage_stats WHERE user_id = ? AND date = ?",
		userID, today,
	).Scan(&todayRequests, &todayTokens, &todayCost)

	var monthRequests, monthTokens, monthCost sql.NullFloat64
	h.db.QueryRow(
		"SELECT COALESCE(SUM(request_count), 0), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(estimated_cost), 0) FROM usage_stats WHERE user_id = ? AND date >= ?",
		userID, monthStart,
	).Scan(&monthRequests, &monthTokens, &monthCost)

	byProviderRows, _ := h.db.Query(
		"SELECT p.name AS provider_name, COALESCE(SUM(u.request_count), 0) AS requests, COALESCE(SUM(u.total_tokens), 0) AS tokens, COALESCE(SUM(u.estimated_cost), 0) AS cost FROM usage_stats u LEFT JOIN providers p ON u.provider_id = p.id WHERE u.user_id = ? AND u.date >= ? GROUP BY u.provider_id, p.name",
		userID, monthStart,
	)
	var byProvider []gin.H
	if byProviderRows != nil {
		defer byProviderRows.Close()
		for byProviderRows.Next() {
			var providerName string
			var requests, tokens, cost float64
			if err := byProviderRows.Scan(&providerName, &requests, &tokens, &cost); err != nil {
				continue
			}
			byProvider = append(byProvider, gin.H{
				"provider": providerName,
				"requests": requests,
				"tokens":   tokens,
				"cost":     cost,
			})
		}
	}
	if byProvider == nil {
		byProvider = []gin.H{}
	}

	var subTokens, lowTokens float64
	h.db.QueryRow(
		"SELECT COALESCE(SUM(u.total_tokens), 0) FROM usage_stats u JOIN providers p ON u.provider_id = p.id WHERE u.user_id = ? AND u.date >= ? AND p.priority < 100",
		userID, monthStart,
	).Scan(&subTokens)
	h.db.QueryRow(
		"SELECT COALESCE(SUM(u.total_tokens), 0) FROM usage_stats u JOIN providers p ON u.provider_id = p.id WHERE u.user_id = ? AND u.date >= ? AND p.priority >= 100 AND p.priority < 200",
		userID, monthStart,
	).Scan(&lowTokens)

	allTokens := subTokens + lowTokens
	estimatedOriginalCost := allTokens / 1000000.0 * 2.0
	actualCost := subTokens/1000000.0*2.0 + lowTokens/1000000.0*0.5
	saved := estimatedOriginalCost - actualCost
	if saved < 0 {
		saved = 0
	}
	percentage := 0.0
	if estimatedOriginalCost > 0 {
		percentage = saved / estimatedOriginalCost * 100
	}

	tr := 0.0
	if todayRequests.Valid {
		tr = todayRequests.Float64
	}
	tt := 0.0
	if todayTokens.Valid {
		tt = todayTokens.Float64
	}
	tc := 0.0
	if todayCost.Valid {
		tc = todayCost.Float64
	}

	c.JSON(http.StatusOK, gin.H{
		"today_requests": tr,
		"today_tokens":   tt,
		"today_cost":     tc,
		"tier_distribution": []gin.H{
			{"name": "订阅层", "value": subTokens},
			{"name": "低成本层", "value": lowTokens},
			{"name": "免费层", "value": 0.0},
		},
		"provider_breakdown": byProvider,
		"cost_savings": gin.H{
			"saved":      saved,
			"percentage": percentage,
		},
	})
}
