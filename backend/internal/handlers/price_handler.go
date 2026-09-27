package handlers

import (
	"context"
	"net/http"
	"time"

	"ai-router-gateway/internal/price"

	"github.com/gin-gonic/gin"
)

// PriceHandler 暴露价格同步和查询 API。
type PriceHandler struct{}

func NewPriceHandler() *PriceHandler {
	return &PriceHandler{}
}

// Sync 手动触发一次从 models.dev 的价格同步（仅管理员）。
func (h *PriceHandler) Sync(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	if err := price.SyncFromModelsDev(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"count":        price.Count(),
		"synced_at":    price.LastSyncTime().UTC().Format(time.RFC3339),
		"sync_error":   nil,
	})
}

// Status 返回当前价格缓存状态（无需鉴权，供前端轮询）。
func (h *PriceHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"count":      price.Count(),
		"last_sync":  price.LastSyncTime().UTC().Format(time.RFC3339),
		"sync_error": formatSyncError(price.LastSyncError()),
	})
}

func formatSyncError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
