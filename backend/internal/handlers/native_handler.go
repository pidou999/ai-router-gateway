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
	"ai-router-gateway/internal/translator"

	"github.com/gin-gonic/gin"
)

// loadRouteSettings 从 settings 表读取路由相关的运行时开关（与 ChatCompletions 共用）。
func loadRouteSettings(db *sql.DB) router.RouteSettings {
	var cavemanEnabled, ponytailEnabled, rtkEnabled, headroomEnabled int
	var cavemanLevel, rtkLevel, headroomLevel int
	var ponytailLevel string

	db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'caveman_enabled' LIMIT 1").Scan(&cavemanEnabled)
	db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'caveman_level' LIMIT 1").Scan(&cavemanLevel)
	db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'ponytail_enabled' LIMIT 1").Scan(&ponytailEnabled)
	db.QueryRow("SELECT COALESCE(value, '') FROM settings WHERE key = 'ponytail_level' LIMIT 1").Scan(&ponytailLevel)
	db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'headroom_enabled' LIMIT 1").Scan(&headroomEnabled)
	db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'headroom_level' LIMIT 1").Scan(&headroomLevel)
	db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'rtk_enabled' LIMIT 1").Scan(&rtkEnabled)
	db.QueryRow("SELECT COALESCE(CAST(value AS INTEGER), 0) FROM settings WHERE key = 'rtk_level' LIMIT 1").Scan(&rtkLevel)

	return router.RouteSettings{
		CavemanEnabled:  cavemanEnabled == 1,
		CavemanLevel:    cavemanLevel,
		PonytailEnabled: ponytailEnabled == 1,
		PonytailLevel:   ponytailLevel,
		HeadroomEnabled: headroomEnabled == 1,
		HeadroomLevel:   headroomLevel,
		RTKEnabled:      rtkEnabled == 1,
		RTKLevel:        rtkLevel,
		TimeoutSeconds:  120,
	}
}

// ---------- Anthropic 原生入口：/v1/messages ----------

// MessagesNative 实现 Anthropic Messages API 原生入口。
// 请求体（Anthropic 形状）经 translator.ParseClaudeRequest 转为内部 ChatRequest，
// 经路由引擎转发后，响应再经 translator.SerializeClaudeResponse / 流式发射器还原为 Anthropic SSE。
func (h *ChatHandler) MessagesNative(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		respondError(c, http.StatusBadRequest, "读取请求体失败："+err.Error())
		return
	}
	chatReq, err := translator.ParseClaudeRequest(body)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(chatReq.Messages) == 0 {
		respondError(c, http.StatusBadRequest, "messages 不能为空")
		return
	}

	userID := auth.GetUserID(c)
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
		"entry":    "anthropic",
	})
	ctx := trace.WithTrace(context.Background(), tr)
	defer tr.Log()

	settings := loadRouteSettings(h.db)
	settings.StreamEnabled = chatReq.Stream

	if chatReq.Stream {
		h.handleAnthropicStream(c, ctx, userID, chatReq, &settings)
	} else {
		h.handleAnthropicNonStream(c, ctx, userID, chatReq, &settings)
	}
}

func (h *ChatHandler) handleAnthropicNonStream(c *gin.Context, ctx context.Context, userID int64, chatReq *models.ChatRequest, settings *router.RouteSettings) {
	resp, err := h.routeEngine.Route(ctx, userID, chatReq, settings)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := translator.SerializeClaudeResponse(resp)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *ChatHandler) handleAnthropicStream(c *gin.Context, ctx context.Context, userID int64, chatReq *models.ChatRequest, settings *router.RouteSettings) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	chunkCh, errCh := h.routeEngine.StreamRoute(ctx, userID, chatReq, settings)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		respondError(c, http.StatusInternalServerError, "当前环境不支持流式传输")
		return
	}

	emitter := translator.NewClaudeStreamEmitter()
	emit := func(ev translator.StreamEvent) {
		data, err := json.Marshal(ev.Data)
		if err != nil {
			return
		}
		if ev.Type != "" {
			_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.Type, string(data))
		} else {
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(data))
		}
		flusher.Flush()
	}

	for {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				return
			}
			for _, ev := range emitter.Emit(chunk) {
				emit(ev)
			}
		case err, ok := <-errCh:
			if ok && err != nil {
				emit(translator.StreamEvent{
					Type: "error",
					Data: map[string]any{
						"type":  "error",
						"error": map[string]any{"type": "api_error", "message": err.Error()},
					},
				})
			}
			return
		}
	}
}

// ---------- Gemini 原生入口：/v1beta/models/:model:generateContent ----------

// GeminiNative 实现 Gemini Generative Language API 原生入口。
// 模型名来自 URL 路径（Gemini 请求体不含 model 字段），经 translator.ParseGeminiRequest
// 转为内部 ChatRequest 后路由；响应再还原为 Gemini GenerateContentResponse（流式为逐片 SSE）。
func (h *ChatHandler) GeminiNative(c *gin.Context) {
	model := c.Param("model")
	// 路径形如 /v1beta/models/{model}:generateContent，Gin 的 *action 通配会把
	// ":generateContent" 段也收进 model 参数，这里只取模型名部分。
	if idx := indexOfAction(model); idx >= 0 {
		model = model[:idx]
	}
	if model == "" {
		respondError(c, http.StatusBadRequest, "model 为必填字段（来自 URL 路径）")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		respondError(c, http.StatusBadRequest, "读取请求体失败："+err.Error())
		return
	}
	chatReq, err := translator.ParseGeminiRequest(body, model)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(chatReq.Messages) == 0 {
		respondError(c, http.StatusBadRequest, "contents 不能为空")
		return
	}

	userID := auth.GetUserID(c)
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
		"entry":    "gemini",
	})
	ctx := trace.WithTrace(context.Background(), tr)
	defer tr.Log()

	settings := loadRouteSettings(h.db)
	settings.StreamEnabled = chatReq.Stream

	if chatReq.Stream {
		h.handleGeminiStream(c, ctx, userID, chatReq, &settings)
	} else {
		h.handleGeminiNonStream(c, ctx, userID, chatReq, &settings)
	}
}

// indexOfAction 找到模型名与动作（":" 开头）的分界位置，返回 ":" 的索引；
// 找不到则返回 -1。
func indexOfAction(model string) int {
	for i := 0; i < len(model); i++ {
		if model[i] == ':' {
			return i
		}
	}
	return -1
}

func (h *ChatHandler) handleGeminiNonStream(c *gin.Context, ctx context.Context, userID int64, chatReq *models.ChatRequest, settings *router.RouteSettings) {
	resp, err := h.routeEngine.Route(ctx, userID, chatReq, settings)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := translator.SerializeGeminiResponse(resp)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *ChatHandler) handleGeminiStream(c *gin.Context, ctx context.Context, userID int64, chatReq *models.ChatRequest, settings *router.RouteSettings) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	chunkCh, errCh := h.routeEngine.StreamRoute(ctx, userID, chatReq, settings)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		respondError(c, http.StatusInternalServerError, "当前环境不支持流式传输")
		return
	}

	emitter := translator.NewGeminiStreamEmitter()
	emit := func(ev translator.StreamEvent) {
		data, err := json.Marshal(ev.Data)
		if err != nil {
			return
		}
		// Gemini 流式只输出 data: 行，无 event: 前缀
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(data))
		flusher.Flush()
	}

	for {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				return
			}
			for _, ev := range emitter.Emit(chunk) {
				emit(ev)
			}
		case err, ok := <-errCh:
			if ok && err != nil {
				emit(translator.StreamEvent{
					Data: map[string]any{
						"error": map[string]any{"code": 500, "message": err.Error()},
					},
				})
			}
			return
		}
	}
}
