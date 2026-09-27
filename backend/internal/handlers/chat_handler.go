package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"ai-router-gateway/internal/auth"
	"ai-router-gateway/internal/models"
	"ai-router-gateway/internal/router"
	"ai-router-gateway/internal/trace"

	"github.com/gin-gonic/gin"
)

type ChatHandler struct {
	db          *sql.DB
	routeEngine *router.RouteEngine
}

func NewChatHandler(db *sql.DB, routeEngine *router.RouteEngine) *ChatHandler {
	return &ChatHandler{db: db, routeEngine: routeEngine}
}

func (h *ChatHandler) ChatCompletions(c *gin.Context) {
	var chatReq models.ChatRequest
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		respondError(c, http.StatusBadRequest, "读取请求体失败："+err.Error())
		return
	}
	// 宽松解析：仅当 JSON 语法本身有误时才拒绝。
	// 字段层面的兼容（content 双形态、未知字段等）由 models.ChatRequest 承担。
	if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
		respondError(c, http.StatusBadRequest, "请求体 JSON 解析失败："+err.Error())
		return
	}
	if chatReq.Model == "" {
		respondError(c, http.StatusBadRequest, "model 为必填字段")
		return
	}
	if len(chatReq.Messages) == 0 {
		respondError(c, http.StatusBadRequest, "messages 不能为空")
		return
	}

	userID := auth.GetUserID(c)

	// 四段式追踪：auth 段记录身份解析结果，整段经 ctx 透传到路由引擎。
	tr := trace.New(userID)
	var authMs int64
	if v, ok := c.Get("req_start"); ok {
		if t, ok := v.(time.Time); ok {
			authMs = time.Since(t).Milliseconds()
		}
	}
	authStatus := "ok"
	if userID == 0 {
		authStatus = "error"
	}
	tr.RecordSegment("auth", authStatus, authMs, map[string]any{
		"user_id":  userID,
		"role":     c.GetString("role"),
		"username": c.GetString("username"),
	})
	ctx := trace.WithTrace(context.Background(), tr)
	defer tr.Log()

	settings := router.RouteSettings{
		StreamEnabled:  chatReq.Stream,
		TimeoutSeconds: 120,
	}

	var cavemanEnabled, ponytailEnabled, rtkEnabled, headroomEnabled int
	var cavemanLevel, rtkLevel, headroomLevel int
	var ponytailLevel string

	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'caveman_enabled' LIMIT 1").Scan(&cavemanEnabled)
	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'caveman_level' LIMIT 1").Scan(&cavemanLevel)
	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'ponytail_enabled' LIMIT 1").Scan(&ponytailEnabled)
	h.db.QueryRow("SELECT COALESCE(value, '') FROM settings WHERE key = 'ponytail_level' LIMIT 1").Scan(&ponytailLevel)
	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'headroom_enabled' LIMIT 1").Scan(&headroomEnabled)
	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'headroom_level' LIMIT 1").Scan(&headroomLevel)
	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'rtk_enabled' LIMIT 1").Scan(&rtkEnabled)
	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'rtk_level' LIMIT 1").Scan(&rtkLevel)

	settings.CavemanEnabled = cavemanEnabled == 1
	settings.CavemanLevel = cavemanLevel
	settings.PonytailEnabled = ponytailEnabled == 1
	settings.PonytailLevel = ponytailLevel
	settings.HeadroomEnabled = headroomEnabled == 1
	settings.HeadroomLevel = headroomLevel
	settings.RTKEnabled = rtkEnabled == 1
	settings.RTKLevel = rtkLevel

	var errorShield int
	h.db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'error_shield' LIMIT 1").Scan(&errorShield)
	settings.ErrorShield = errorShield == 1

	if chatReq.Stream {
		h.handleStream(c, ctx, userID, &chatReq, &settings)
	} else {
		h.handleNonStream(c, ctx, userID, &chatReq, &settings)
	}
}

func (h *ChatHandler) handleNonStream(c *gin.Context, ctx context.Context, userID int64, chatReq *models.ChatRequest, settings *router.RouteSettings) {
	resp, err := h.routeEngine.Route(ctx, userID, chatReq, settings)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ChatHandler) handleStream(c *gin.Context, ctx context.Context, userID int64, chatReq *models.ChatRequest, settings *router.RouteSettings) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	chunkCh, errCh := h.routeEngine.StreamRoute(ctx, userID, chatReq, settings)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		respondError(c, http.StatusInternalServerError, "当前环境不支持流式传输")
		return
	}

	for {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			data, err := json.Marshal(chunk)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(data))
			flusher.Flush()
		case err, ok := <-errCh:
			if ok && err != nil {
				// 流式错误也遵循 OpenAI 结构，便于客户端解析
				errType, errCode := errorTypeFor(http.StatusInternalServerError)
				errData, _ := json.Marshal(apiErrorResponse{
					Error: apiError{Message: err.Error(), Type: errType, Code: errCode},
				})
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(errData))
				flusher.Flush()
			}
			return
		}
	}
}

func (h *ChatHandler) ModelsList(c *gin.Context) {
	type modelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
		Version string `json:"version,omitempty"`
	}

	modelSet := make(map[string]modelItem)
	now := time.Now().Unix()

	// 1) combos（保持原名，无版本）
	comboRows, err := h.db.Query("SELECT name FROM combos WHERE enabled = 1")
	if err == nil {
		defer comboRows.Close()
		for comboRows.Next() {
			var name string
			if err := comboRows.Scan(&name); err != nil {
				continue
			}
			if _, exists := modelSet[name]; !exists {
				modelSet[name] = modelItem{
					ID:      name,
					Object:  "model",
					Created: now,
					OwnedBy: "combo",
				}
			}
		}
	}

	// 2) 真实模型：id 用 display_name（短名），version 存原始 model_id（含日期后缀）
	rows, err := h.db.Query(`
		SELECT m.model_id, COALESCE(m.display_name, m.model_id), COALESCE(p.name, m.owned_by)
		FROM models m
		LEFT JOIN providers p ON p.id = m.provider_id
		WHERE m.enabled = 1
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var modelID, displayName, ownedBy string
			if err := rows.Scan(&modelID, &displayName, &ownedBy); err != nil {
				continue
			}
			// id 用短名；若短名与原始 ID 不同，version 记录完整原始 ID
			shortID := displayName
			version := ""
			if shortID != modelID && len(modelID) > 0 {
				version = modelID
			}
			if _, exists := modelSet[shortID]; !exists {
				modelSet[shortID] = modelItem{
					ID:      shortID,
					Object:  "model",
					Created: now,
					OwnedBy: ownedBy,
					Version: version,
				}
			}
		}
	}

	data := make([]modelItem, 0, len(modelSet))
	for _, m := range modelSet {
		data = append(data, m)
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

// AvailableModels 返回已启用且测试连通的模型列表（供前端组合选择器等 UI 使用，JWT 认证）。
// 与 ModelsList 的区别：此端点在 protected 路由组下（JWT），只返回 enabled=1 的真实模型。
func (h *ChatHandler) AvailableModels(c *gin.Context) {
	type modelItem struct {
		ID          string   `json:"id"`
		DisplayName string   `json:"display_name"`
		OwnedBy     string   `json:"owned_by"`
		ProviderID  int64    `json:"provider_id"`
		// Capabilities 为最终生效的能力标签（text/vision/code/long_context/audio/reasoning）：
		// 以提供商详情页的真实探测结果为准，未探测的部分回退到模型名关键词推断。
		Capabilities []string `json:"capabilities"`
		// Probed 标记该模型是否做过真实的多模态探测，供 UI 区分「实测」与「按名猜测」。
		Probed bool `json:"probed"`
	}

	rows, err := h.db.Query(`
		SELECT m.model_id, COALESCE(m.display_name, m.model_id), COALESCE(p.name, m.owned_by),
		       m.provider_id, COALESCE(m.probed_caps, ''), COALESCE(m.probed_at, '')
		FROM models m
		LEFT JOIN providers p ON p.id = m.provider_id
		WHERE m.enabled = 1 AND p.enabled = 1
		ORDER BY p.name, m.display_name, m.model_id
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模型失败"})
		return
	}
	defer rows.Close()

	var items []modelItem
	for rows.Next() {
		var m modelItem
		var probedCaps, probedAt string
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.OwnedBy, &m.ProviderID, &probedCaps, &probedAt); err != nil {
			continue
		}
		for _, cap := range router.ResolveCapabilities(m.ID, parseProbedCaps(probedCaps)) {
			m.Capabilities = append(m.Capabilities, string(cap))
		}
		m.Probed = probedAt != ""
		items = append(items, m)
	}

	c.JSON(http.StatusOK, gin.H{"data": items})
}
