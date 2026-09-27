// Package handlers provides the models sync handler.
package handlers

import (
	"database/sql"
	"net/http"

	"ai-router-gateway/internal/task"

	"github.com/gin-gonic/gin"
)

// ModelsSyncHandler 提供模型同步的手动触发与状态接口。
type ModelsSyncHandler struct {
	db *sql.DB
}

func NewModelsSyncHandler(db *sql.DB) *ModelsSyncHandler {
	return &ModelsSyncHandler{db: db}
}

// Sync 手动触发一次全量模型同步，返回统计结果（成功/失败服务商数）。
func (h *ModelsSyncHandler) Sync(c *gin.Context) {
	err := task.SyncModelsTask(h.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "同步成功"})
}

// Status 查询当前 auto_sync 配置状态。
func (h *ModelsSyncHandler) Status(c *gin.Context) {
	rows, err := h.db.Query(
		`SELECT id, name, COALESCE(auto_sync, 0) as auto_sync FROM providers`,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()

	type providerStatus struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		AutoSync int    `json:"auto_sync"`
	}
	var list []providerStatus
	for rows.Next() {
		var p providerStatus
		if err := rows.Scan(&p.ID, &p.Name, &p.AutoSync); err != nil {
			continue
		}
		list = append(list, p)
	}
	c.JSON(http.StatusOK, gin.H{"providers": list})
}
