package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"ai-router-gateway/internal/auth"

	"github.com/gin-gonic/gin"
)

// ComboModel 表示组合中的一个模型条目
type ComboModel struct {
	ID         string `json:"id"`
	ProviderID int64  `json:"provider_id"`
	// Capability 为该模型在组合内承担的角色（智能路由 auto 策略使用）：
	// "" / "auto" = 由模型名自动推断；也可手动指定 vision / code / text / long_context / audio / reasoning。
	Capability string `json:"capability,omitempty"`
}

// ComboConfigRequest 是创建/更新组合时的新格式请求体
type ComboConfigRequest struct {
	Name    string       `json:"name"`
	Config  ComboConfig  `json:"config"`
}

// ComboConfig 是组合配置（新格式：多模型+策略）
type ComboConfig struct {
	Models   []ComboModel `json:"models"`
	Strategy string       `json:"strategy"` // "auto" | "fallback" | "round_robin"
}

// validStrategies 允许的组合策略集合，未知值统一归一为 fallback。
var validStrategies = map[string]bool{
	"auto":        true, // 智能路由：按请求意图（图片/代码/长文本/纯文本）自动挑选组合内对应能力的模型
	"fallback":    true,
	"round_robin": true,
}

// ComboCreateRequest 保留旧接口兼容（config 为 JSON 字符串）
type ComboCreateRequest struct {
	Name   string `json:"name"`
	Config string `json:"config"` // 兼容旧客户端传字符串
}

type ComboHandler struct {
	db *sql.DB
}

func NewComboHandler(db *sql.DB) *ComboHandler {
	return &ComboHandler{db: db}
}

// parseComboConfig 从 gin.Context 中提取并规范化 combo 配置
// 同时支持新旧两种请求格式
func parseComboConfig(c *gin.Context) (name string, configJSON string, err error) {
	// 先尝试新格式
	var newReq ComboConfigRequest
	if bindErr := c.ShouldBindJSON(&newReq); bindErr == nil && len(newReq.Config.Models) > 0 {
		name = newReq.Name
		if !validStrategies[newReq.Config.Strategy] {
			newReq.Config.Strategy = "fallback"
		}
		configBytes, _ := json.Marshal(newReq.Config)
		configJSON = string(configBytes)
		return
	}

	// 回退到旧格式
	var oldReq ComboCreateRequest
	if bindErr := c.ShouldBindJSON(&oldReq); bindErr != nil {
		err = bindErr
		return
	}
	name = oldReq.Name
	configJSON = oldReq.Config
	return
}

func (h *ComboHandler) ListCombos(c *gin.Context) {
	userID := auth.GetUserID(c)
	rows, err := h.db.Query(
		"SELECT id, user_id, name, config, enabled, created_at, updated_at FROM combos WHERE user_id = ? AND enabled = 1 ORDER BY created_at DESC",
		userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询组合失败"})
		return
	}
	defer rows.Close()

	var combos []gin.H
	for rows.Next() {
		var id, userID, enabled int64
		var name, config, createdAt, updatedAt string
		if err := rows.Scan(&id, &userID, &name, &config, &enabled, &createdAt, &updatedAt); err != nil {
			continue
		}
		// 尝试将 config 字符串解析为对象返回给前端
		var configObj interface{}
		json.Unmarshal([]byte(config), &configObj)
		if configObj == nil {
			configObj = config // 解析失败则返回原始字符串
		}
		combos = append(combos, gin.H{
			"id":         id,
			"user_id":    userID,
			"name":       name,
			"config":     configObj,
			"enabled":    enabled,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}
	if combos == nil {
		combos = []gin.H{}
	}
	c.JSON(http.StatusOK, combos)
}

func (h *ComboHandler) CreateCombo(c *gin.Context) {
	userID := auth.GetUserID(c)

	name, configJSON, err := parseComboConfig(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称为必填项"})
		return
	}

	now := time.Now().Format(time.RFC3339)
	result, err := h.db.Exec(
		"INSERT INTO combos (user_id, name, config, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		userID, name, configJSON, now, now,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建组合失败"})
		return
	}
	id, _ := result.LastInsertId()

	// 返回解析后的 config 对象
	var configObj interface{}
	json.Unmarshal([]byte(configJSON), &configObj)
	c.JSON(http.StatusCreated, gin.H{
		"id":      id,
		"user_id": userID,
		"name":    name,
		"config":  configObj,
	})
}

func (h *ComboHandler) UpdateCombo(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	userID := auth.GetUserID(c)

	name, configJSON, err := parseComboConfig(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().Format(time.RFC3339)
	result, err := h.db.Exec(
		"UPDATE combos SET name = ?, config = ?, updated_at = ? WHERE id = ? AND user_id = ?",
		name, configJSON, now, id, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新组合失败"})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到组合"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "组合已更新"})
}

func (h *ComboHandler) DeleteCombo(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	userID := auth.GetUserID(c)

	now := time.Now().Format(time.RFC3339)
	result, err := h.db.Exec(
		"UPDATE combos SET enabled = 0, updated_at = ? WHERE id = ? AND user_id = ?",
		now, id, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除组合失败"})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到组合"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "组合已删除"})
}
