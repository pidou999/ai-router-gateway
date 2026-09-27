package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ai-router-gateway/internal/crypto"
	"ai-router-gateway/internal/auth"
	"ai-router-gateway/internal/logger"
	"ai-router-gateway/internal/proxy"
	"ai-router-gateway/internal/router"
	"github.com/gin-gonic/gin"
)

// isOpenCodeFreeModel 判定是否为 OpenCode 免费模型（参考 9Router 的 opencode-free 过滤器）。
// 免费模型特征：id 以 "-free" 结尾，或在已知免费列表中。
func isOpenCodeFreeModel(modelID string) bool {
	if strings.HasSuffix(modelID, "-free") {
		return true
	}
	// 9Router 已知的不带 -free 后缀的免费模型
	knownFree := map[string]bool{
		"big-pickle": true,
	}
	return knownFree[modelID]
}

// filterFreeModelsForProvider 根据服务商类型过滤/标记免费模型。
// 返回处理后的模型列表，每个模型带上 is_free 标记。
func filterFreeModelsForProvider(apiType string, models []gin.H) []gin.H {
	if apiType != "opencode" {
		return models // 目前仅对 OpenCode 做免费模型识别
	}
	result := make([]gin.H, 0, len(models))
	for _, m := range models {
		modelID, _ := m["model_id"].(string)
		isFree := isOpenCodeFreeModel(modelID)
		result = append(result, gin.H{
			"model_id":     modelID,
			"display_name": m["display_name"],
			"owned_by":     m["owned_by"],
			"is_free":      isFree,
		})
	}
	return result
}

type ProviderCreateRequest struct {
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	APIType     string `json:"api_type"`
	Priority    int    `json:"priority"`
	PricingType string `json:"pricing_type"`
}

type ProviderUpdateRequest struct {
	Name        *string `json:"name"`
	BaseURL     *string `json:"base_url"`
	APIType     *string `json:"api_type"`
	Priority    *int    `json:"priority"`
	PricingType *string `json:"pricing_type"`
	Enabled     *bool   `json:"enabled"`
	AutoSync    *int    `json:"auto_sync"` // 是否开启模型自动同步
}

type ProviderHandler struct {
	db            *sql.DB
	encryptionKey string
}

func NewProviderHandler(db *sql.DB, encryptionKey string) *ProviderHandler {
	return &ProviderHandler{db: db, encryptionKey: encryptionKey}
}

func (h *ProviderHandler) decryptAPIKey(encrypted string) string {
	plain, err := crypto.Decrypt(encrypted, h.encryptionKey)
	if err != nil {
		return encrypted
	}
	return plain
}

// fetchModels 从服务商的 /models 端点拉取模型列表（OpenAI 兼容格式）。
// baseURL 为完整 chat 端点或前缀；apiKey 为空时不带鉴权。
func (h *ProviderHandler) fetchModels(baseURL, apiKey string) ([]gin.H, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	modelsBase := baseURL
	if strings.HasSuffix(modelsBase, "/chat/completions") {
		modelsBase = modelsBase[:len(modelsBase)-len("/chat/completions")]
	}
	modelsURL := modelsBase + "/models"

	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("状态码 %d：%s", resp.StatusCode, msg)
	}

	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析响应失败：%w", err)
	}

	models := make([]gin.H, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		display := m.Name
		if display == "" {
			display = m.ID
		}
		models = append(models, gin.H{
			"model_id":     m.ID,
			"display_name": display,
			"owned_by":     m.OwnedBy,
		})
	}
	return models, nil
}

// fetchCloudflareModels 从 Cloudflare Workers AI 的 /ai/models/search 端点获取可用模型列表。
// resolvedBase 应为已替换 {accountId} 的 base_url（如 .../accounts/REAL_ID/ai/v1）。
func (h *ProviderHandler) fetchCloudflareModels(resolvedBase, apiKey string) ([]gin.H, error) {
	// 从 base_url 反推 accounts API 前缀：把 /ai/v1 替换为 /ai
	base := strings.TrimRight(resolvedBase, "/")
	if strings.HasSuffix(base, "/v1") {
		base = base[:len(base)-len("/v1")]
	}
	searchURL := base + "/models/search?task=Text+Generation"

	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("状态码 %d：%s", resp.StatusCode, msg)
	}

	var parsed struct {
		Result []struct {
			Name string          `json:"name"`
			Task json.RawMessage `json:"task"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析响应失败：%w", err)
	}

	models := make([]gin.H, 0, len(parsed.Result))
	for _, m := range parsed.Result {
		if m.Name == "" {
			continue
		}
		models = append(models, gin.H{
			"model_id":     m.Name,
			"display_name": m.Name,
			"owned_by":     "cloudflare",
		})
	}
	return models, nil
}

// getProviderAccountKey 返回该服务商下第一个启用账户的明文 API Key（解密后）。
func (h *ProviderHandler) getProviderAccountKey(providerID int64) (string, bool) {
	var enc string
	err := h.db.QueryRow(
		"SELECT api_key_encrypted FROM accounts WHERE provider_id = ? AND COALESCE(enabled, 0) != 0 ORDER BY id ASC LIMIT 1",
		providerID,
	).Scan(&enc)
	if err != nil {
		return "", false
	}
	return h.decryptAPIKey(enc), true
}

// accountCred 封装单个启用账户的凭证信息：解密后的 API Key + extra_config。
// extra_config 用于 URL 模板替换（如 Cloudflare 的 accountId），每个账户独立。
type accountCred struct {
	APIKey      string
	ExtraConfig string
}

// getProviderAccountCreds 返回该服务商下所有启用账户的凭证（API Key + extra_config），按 id 升序。
// 多账号场景下用于轮询分摊请求，避免单账号打满上游速率限制（如 NVIDIA 每账号每分钟 40 次）。
// 每个账户都携带自己的 extra_config，确保 Cloudflare 等「ID+Key」服务商在轮询时
// 使用正确的 accountId 替换 URL 模板，而非固定第一个账户的 accountId。
func (h *ProviderHandler) getProviderAccountCreds(providerID int64) []accountCred {
	rows, err := h.db.Query(
		"SELECT api_key_encrypted, extra_config FROM accounts WHERE provider_id = ? AND COALESCE(enabled, 0) != 0 ORDER BY id ASC",
		providerID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var creds []accountCred
	for rows.Next() {
		var enc, extra string
		if err := rows.Scan(&enc, &extra); err != nil {
			continue
		}
		creds = append(creds, accountCred{
			APIKey:      h.decryptAPIKey(enc),
			ExtraConfig: extra,
		})
	}
	return creds
}

// getProviderAccountKeys 返回该服务商下所有启用账户的明文 API Key（解密后，按 id 升序）。
// 多账号场景下用于轮询分摊请求，避免单账号打满上游速率限制（如 NVIDIA 每账号每分钟 40 次）。
func (h *ProviderHandler) getProviderAccountKeys(providerID int64) []string {
	creds := h.getProviderAccountCreds(providerID)
	keys := make([]string, len(creds))
	for i, c := range creds {
		keys[i] = c.APIKey
	}
	return keys
}

// resolveProviderURL 返回经过 SubstituteTemplate 替换后的 base_url（处理 {accountId} 等占位符）。
// 使用第一个启用账户的 extra_config（兜底单账户场景）。
func (h *ProviderHandler) resolveProviderURL(providerID int64, rawBaseURL string) string {
	var extra string
	err := h.db.QueryRow(
		"SELECT extra_config FROM accounts WHERE provider_id = ? AND COALESCE(enabled, 0) != 0 ORDER BY id ASC LIMIT 1",
		providerID,
	).Scan(&extra)
	if err != nil {
		return proxy.SubstituteTemplate(rawBaseURL, "")
	}
	return proxy.SubstituteTemplate(rawBaseURL, extra)
}

func (h *ProviderHandler) ListProviders(c *gin.Context) {
	rows, err := h.db.Query(
		"SELECT id, name, base_url, api_type, enabled, priority, health_status, pricing_type FROM providers ORDER BY priority DESC",
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询提供商失败"})
		return
	}
	defer rows.Close()

	var providers []gin.H
	for rows.Next() {
		var id int64
		var name, baseURL, apiType, healthStatus, pricingType string
		var enabled, priority int
		if err := rows.Scan(&id, &name, &baseURL, &apiType, &enabled, &priority, &healthStatus, &pricingType); err != nil {
			continue
		}
		providers = append(providers, gin.H{
			"id":            id,
			"name":          name,
			"base_url":      baseURL,
			"api_type":      apiType,
			"enabled":       enabled,
			"priority":      priority,
			"health_status": healthStatus,
			"pricing_type":  pricingType,
		})
	}
	if providers == nil {
		providers = []gin.H{}
	}
	c.JSON(http.StatusOK, providers)
}

func (h *ProviderHandler) GetProvider(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var name, baseURL, apiType, healthStatus, pricingType string
	var enabled, priority int
	err = h.db.QueryRow(
		"SELECT id, name, base_url, api_type, enabled, priority, health_status, pricing_type FROM providers WHERE id = ?",
		id,
	).Scan(&id, &name, &baseURL, &apiType, &enabled, &priority, &healthStatus, &pricingType)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到提供商"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询提供商失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":            id,
		"name":          name,
		"base_url":      baseURL,
		"api_type":      apiType,
		"enabled":       enabled,
		"priority":      priority,
		"health_status": healthStatus,
		"pricing_type":  pricingType,
	})
}

func (h *ProviderHandler) CreateProvider(c *gin.Context) {
	var req ProviderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name == "" || req.BaseURL == "" || req.APIType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称、base_url 和 api_type 均为必填项"})
		return
	}
	pricingType := req.PricingType
	if pricingType == "" {
		pricingType = "paid"
	}
	userID := auth.GetUserID(c)
	result, err := h.db.Exec(
		"INSERT INTO providers (name, base_url, api_type, priority, pricing_type, user_id) VALUES (?, ?, ?, ?, ?, ?)",
		req.Name, req.BaseURL, req.APIType, req.Priority, pricingType, userID,
	)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "提供商名称已存在"})
		return
	}
	id, _ := result.LastInsertId()
	c.JSON(http.StatusCreated, gin.H{
		"id":           id,
		"name":         req.Name,
		"base_url":     req.BaseURL,
		"api_type":     req.APIType,
		"priority":     req.Priority,
		"pricing_type": pricingType,
	})
}

func (h *ProviderHandler) UpdateProvider(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var req ProviderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	now := time.Now().Format(time.RFC3339)
	// 动态构建 SET 子句，只更新前端实际发送的字段，避免清空其他字段为零值。
	var setParts []string
	var args []interface{}

	if req.Name != nil {
		setParts = append(setParts, "name = ?")
		args = append(args, *req.Name)
	}
	if req.BaseURL != nil {
		setParts = append(setParts, "base_url = ?")
		args = append(args, *req.BaseURL)
	}
	if req.APIType != nil {
		setParts = append(setParts, "api_type = ?")
		args = append(args, *req.APIType)
	}
	if req.Priority != nil {
		setParts = append(setParts, "priority = ?")
		args = append(args, *req.Priority)
	}
	if req.PricingType != nil {
		setParts = append(setParts, "pricing_type = ?")
		args = append(args, *req.PricingType)
	}
	if req.Enabled != nil {
		setParts = append(setParts, "enabled = ?")
		args = append(args, *req.Enabled)
	}
	if req.AutoSync != nil {
		setParts = append(setParts, "auto_sync = ?")
		args = append(args, *req.AutoSync)
	}
	setParts = append(setParts, "updated_at = ?")
	args = append(args, now)
	args = append(args, id)

	_, err = h.db.Exec(
		"UPDATE providers SET " + strings.Join(setParts, ", ") + " WHERE id = ?",
		args...,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新提供商失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "提供商已更新"})
}

func (h *ProviderHandler) DeleteProvider(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	now := time.Now().Format(time.RFC3339)
	result, err := h.db.Exec(
		"UPDATE providers SET enabled = 0, updated_at = ? WHERE id = ?",
		now, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除提供商失败"})
		return
	}
	// 同时清理该服务商的模型，避免组合选择器等 UI 仍显示已删除服务商下的模型
	_, _ = h.db.Exec("DELETE FROM models WHERE provider_id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除提供商失败"})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到提供商"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "提供商已删除"})
}

func (h *ProviderHandler) HealthCheck(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var baseURL string
	err = h.db.QueryRow("SELECT base_url FROM providers WHERE id = ?", id).Scan(&baseURL)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到提供商"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询提供商失败"})
		return
	}

	baseURL = strings.TrimRight(baseURL, "/")

	// 轮询所有启用账户：Cloudflare 等「ID+Key」服务商每个账户有自己的 accountId，
	// 必须用对应账户的 extra_config 替换 URL 模板，否则 accountId 不匹配导致 401。
	creds := h.getProviderAccountCreds(id)
	// 无账户时回退匿名探测（如 OpenCode 免费层）
	if len(creds) == 0 {
		creds = []accountCred{{}}
	}

	status := "unhealthy"
	message := ""
	for _, cred := range creds {
		resolvedBase := proxy.SubstituteTemplate(baseURL, cred.ExtraConfig)
		ok, msg := h.tryHealthCheckOnce(id, resolvedBase, cred.APIKey)
		if ok {
			status = "healthy"
			message = msg
			break
		}
		// 记下最后一次失败信息，方便诊断
		message = msg
	}

	h.setHealthStatus(id, status)
	c.JSON(http.StatusOK, gin.H{
		"id":            id,
		"health_status": status,
		"message":       message,
	})
}

// tryHealthCheckOnce 用单个账户的凭证探测一次服务商连通性。
// 策略：优先 GET /models，失败后回退到 POST /chat/completions 极简探测。
func (h *ProviderHandler) tryHealthCheckOnce(providerID int64, baseURL, apiKey string) (bool, string) {
	modelsBase := baseURL
	if strings.HasSuffix(modelsBase, "/chat/completions") {
		modelsBase = modelsBase[:len(modelsBase)-len("/chat/completions")]
	}
	modelsURL := strings.TrimRight(modelsBase, "/") + "/models"

	hasKey := apiKey != ""

	// --- 策略 1：GET /models（OpenAI 兼容 API）---
	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return false, "构造请求失败：" + err.Error()
	}
	if hasKey {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, "无法连接：" + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, "连接成功"
	}

	body, _ := io.ReadAll(resp.Body)
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	detail := fmt.Sprintf("状态码 %d：%s", resp.StatusCode, msg)

	// --- 策略 2：GET 失败 → 回退 POST 极简探测 ---
	// GET /models 对部分服务商返回 401（需鉴权但此端点不支持 Key 鉴权）、
	// 405（POST-only）、404（无 /models 端点）等，均应尝试 POST 路径。
	if resp.StatusCode >= 400 && hasKey {
		firstModel := h.getFirstProviderModel(providerID)
		if firstModel != "" {
			chatURL := strings.TrimRight(baseURL, "/") + "/chat/completions"
			chatPayload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"stream":false}`, firstModel)
			if ok, _ := h.postProbe(chatURL, apiKey, chatPayload); ok {
				return true, "连接成功（POST 探测）"
			}
			// 再试直接 POST baseURL/{model_id}（适用于部分自定义端点）
			fallbackURL := strings.TrimRight(baseURL, "/") + "/" + firstModel
			fallbackPayload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"stream":false}`, firstModel)
			if ok, _ := h.postProbe(fallbackURL, apiKey, fallbackPayload); ok {
				return true, "连接成功（POST 探测）"
			}
		}
	}

	return false, detail
}

func (h *ProviderHandler) setHealthStatus(id int64, status string) {
	now := time.Now().Format(time.RFC3339)
	_, _ = h.db.Exec(
		"UPDATE providers SET health_status = ?, updated_at = ? WHERE id = ?",
		status, now, id,
	)
}

// getFirstProviderModel 返回该服务商已启用的第一个模型 ID（用于健康检查回退探测）。
// 无模型时返回空字符串。
func (h *ProviderHandler) getFirstProviderModel(providerID int64) string {
	var modelID string
	err := h.db.QueryRow(
		"SELECT model_id FROM models WHERE provider_id = ? AND enabled = 1 LIMIT 1",
		providerID,
	).Scan(&modelID)
	if err != nil {
		return ""
	}
	return modelID
}

// postProbe 向指定 URL 发起一次 POST 探测请求，返回是否成功和错误信息。
// 健康检查场景下：任何 HTTP 响应（非网络错误）均视为可达——4xx/5xx 说明端点存在、网络通畅，
// 仅是探测 payload 不完整导致业务错误，不影响健康判定。
func (h *ProviderHandler) postProbe(url, apiKey, payload string) (bool, string) {
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return false, "构造请求失败：" + err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, "无法连接：" + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(body))
	if len(bodyStr) > 200 {
		bodyStr = bodyStr[:200]
	}

	// 2xx = 完全成功；4xx = 端点可达但参数/鉴权问题（健康检查仍视为可达）
	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return true, ""
	}
	return false, fmt.Sprintf("HTTP %d：%s", resp.StatusCode, bodyStr)
}

// GetProviderModels 返回已存储的该服务商模型列表。
func (h *ProviderHandler) GetProviderModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	rows, err := h.db.Query(
		`SELECT model_id, display_name, owned_by, enabled, probed_caps, probed_at
		 FROM models WHERE provider_id = ? ORDER BY model_id ASC`,
		id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模型失败"})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	for rows.Next() {
		var modelID, displayName, ownedBy string
		var enabled int
		var probedCaps, probedAt sql.NullString
		if err := rows.Scan(&modelID, &displayName, &ownedBy, &enabled, &probedCaps, &probedAt); err != nil {
			continue
		}
		models = append(models, gin.H{
			"model_id":     modelID,
			"display_name": displayName,
			"owned_by":     ownedBy,
			"enabled":      enabled,
			// capabilities 为「关键词推断 + 实测结果」合并后的最终能力，供前端直接渲染徽章
			"capabilities": capabilityStrings(modelID, parseProbedCaps(probedCaps.String)),
			// probed_at 非空表示做过真实探测，前端据此区分「实测」与「按名猜测」
			"probed_at": probedAt.String,
		})
	}
	c.JSON(http.StatusOK, models)
}

// parseProbedCaps 解析 models.probed_caps 列（JSON 对象），异常时返回 nil。
func parseProbedCaps(raw string) map[string]bool {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var caps map[string]bool
	if err := json.Unmarshal([]byte(raw), &caps); err != nil {
		return nil
	}
	return caps
}

// capabilityStrings 返回模型最终生效的能力标签（实测结果优先于模型名推断）。
func capabilityStrings(modelID string, probed map[string]bool) []string {
	caps := router.ResolveCapabilities(modelID, probed)
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, string(c))
	}
	return out
}

// ProbeModel 对单个模型做多模态能力探测（发一条含小图的真实请求），并持久化结论。
func (h *ProviderHandler) ProbeModel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model_id 为必填项"})
		return
	}

	var baseURL string
	if err := h.db.QueryRow("SELECT base_url FROM providers WHERE id = ?", id).Scan(&baseURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务商失败"})
		return
	}
	// 轮询所有启用账户：Cloudflare 等「ID+Key」服务商每个账户有自己的 accountId，
	// 必须用对应账户的 extra_config 替换 URL 模板。
	creds := h.getProviderAccountCreds(id)
	if len(creds) == 0 {
		creds = []accountCred{{}}
	}

	var result gin.H
	for _, cred := range creds {
		resolvedBase := proxy.SubstituteTemplate(baseURL, cred.ExtraConfig)
		result = h.probeOne(id, resolvedBase, cred.APIKey, req.ModelID)
		// 如果探测得出明确结论（vision 支持/不支持），停止尝试下一个账户
		if result["conclusive"] == true {
			break
		}
	}
	c.JSON(http.StatusOK, result)
}

// probeOne 探测单个模型并写回数据库，返回可直接下发前端的结果。
func (h *ProviderHandler) probeOne(providerID int64, baseURL, apiKey, modelID string) gin.H {
	res := h.probeVision(baseURL, apiKey, modelID)

	var existing sql.NullString
	h.db.QueryRow(
		"SELECT probed_caps FROM models WHERE provider_id = ? AND model_id = ?",
		providerID, modelID,
	).Scan(&existing)

	merged, _ := mergeProbedCaps(existing.String, map[string]*bool{"vision": res.Supported})
	probedAt := ""
	if res.Supported != nil {
		// 只有得出明确结论才刷新探测时间，避免限流 / 鉴权失败被记成「已探测」
		probedAt = time.Now().Format("2006-01-02 15:04:05")
		h.db.Exec(
			"UPDATE models SET probed_caps = ?, probed_at = ? WHERE provider_id = ? AND model_id = ?",
			merged, probedAt, providerID, modelID,
		)
	}

	out := gin.H{
		"model_id":     modelID,
		"message":      res.Message,
		"capabilities": capabilityStrings(modelID, parseProbedCaps(merged)),
		"probed_at":    probedAt,
		"conclusive":   res.Supported != nil,
	}
	if res.Supported != nil {
		out["vision"] = *res.Supported
	}
	return out
}

// ProbeAllModels 并发探测该服务商下所有已启用模型的多模态能力。
// 只测已启用（连通性测试通过）的模型：未启用的连普通文本请求都发不通，探测无意义且白耗额度。
func (h *ProviderHandler) ProbeAllModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var baseURL string
	if err := h.db.QueryRow("SELECT base_url FROM providers WHERE id = ?", id).Scan(&baseURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务商失败"})
		return
	}
	// 轮询所有启用账户：每个账户有自己的 extra_config，Cloudflare 等「ID+Key」服务商必须用对应账户的 accountId
	creds := h.getProviderAccountCreds(id)
	if len(creds) == 0 {
		creds = []accountCred{{}}
	}
	var nextCred int64 // 账号轮询计数器

	rows, err := h.db.Query("SELECT model_id FROM models WHERE provider_id = ? AND enabled = 1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模型失败"})
		return
	}
	modelIDs := make([]string, 0)
	for rows.Next() {
		var mid string
		if err := rows.Scan(&mid); err == nil {
			modelIDs = append(modelIDs, mid)
		}
	}
	rows.Close()

	if len(modelIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"total": 0, "vision_count": 0, "text_count": 0, "unknown_count": 0, "results": []gin.H{},
		})
		return
	}

	var (
		mu           sync.Mutex
		wg           sync.WaitGroup
		sem          = make(chan struct{}, 2) // 与连通性测试一致，限流保护 serverless 上游
		results      = make([]gin.H, 0, len(modelIDs))
		visionCount  int
		textCount    int
		unknownCount int
	)
	for i, mid := range modelIDs {
		wg.Add(1)
		go func(idx int, modelID string) {
			defer wg.Done()
			if idx > 0 {
				time.Sleep(400 * time.Duration((idx%2)+1) * time.Millisecond)
			}
			sem <- struct{}{}
			defer func() { <-sem }()

			// 轮询选取账号，用该账号的 extra_config + key 探测
			ci := atomic.AddInt64(&nextCred, 1)
			cred := creds[(ci-1)%int64(len(creds))]
			resolvedBase := proxy.SubstituteTemplate(baseURL, cred.ExtraConfig)
			r := h.probeOne(id, resolvedBase, cred.APIKey, modelID)
			mu.Lock()
			if v, ok := r["vision"].(bool); !ok {
				unknownCount++
			} else if v {
				visionCount++
			} else {
				textCount++
			}
			results = append(results, r)
			mu.Unlock()
		}(i, mid)
	}
	wg.Wait()

	c.JSON(http.StatusOK, gin.H{
		"total":         len(modelIDs),
		"vision_count":  visionCount,
		"text_count":    textCount,
		"unknown_count": unknownCount,
		"results":       results,
	})
}

// FetchProviderModels 从服务商实时拉取模型并写入数据库，返回拉取结果。
// 对免费层服务商（如 OpenCode），自动识别免费模型并标记 enabled=1。
func (h *ProviderHandler) FetchProviderModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var baseURL, apiType, pricingType string
	err = h.db.QueryRow("SELECT base_url, api_type, pricing_type FROM providers WHERE id = ?", id).Scan(&baseURL, &apiType, &pricingType)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到提供商"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务商失败"})
		return
	}

	// 轮询所有启用账户：Cloudflare 等「ID+Key」服务商每个账户有自己的 accountId，
	// 必须用对应账户的 extra_config 替换 URL 模板。
	// 优先用第一个账户获取模型列表，失败时自动尝试下一个账户。
	creds := h.getProviderAccountCreds(id)
	if len(creds) == 0 {
		creds = []accountCred{{}}
	}

	var fetched []gin.H
	for _, cred := range creds {
		resolvedBase := proxy.SubstituteTemplate(baseURL, cred.ExtraConfig)
		if apiType == "cloudflare" {
			fetched, err = h.fetchCloudflareModels(resolvedBase, cred.APIKey)
		} else {
			fetched, err = h.fetchModels(resolvedBase, cred.APIKey)
		}
		if err == nil && len(fetched) > 0 {
			break
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": "获取模型失败：" + err.Error(), "models": []gin.H{}})
		return
	}

	// 根据服务商类型过滤/标记免费模型（参考 9Router 的 modelsFetcher + FILTERS 机制）
	fetched = filterFreeModelsForProvider(apiType, fetched)

	if len(fetched) > 0 {
		tx, err := h.db.Begin()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入模型失败(begin): " + err.Error()})
			return
		}
		// 先清掉该服务商旧模型，避免残留
		if _, err := tx.Exec("DELETE FROM models WHERE provider_id = ?", id); err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入模型失败(delete): " + err.Error()})
			return
		}
		// 为免费层服务商自动启用免费模型；其他模型默认 disabled（enabled=0）
		// 同时记录 is_free 标记，便于前端展示和后续筛选
		stmt, err := tx.Prepare("INSERT OR IGNORE INTO models (provider_id, model_id, display_name, owned_by, enabled, is_free) VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入模型失败(prepare): " + err.Error()})
			return
		}
		defer stmt.Close()
		for idx, m := range fetched {
			modelID, _ := m["model_id"].(string)
			displayName, _ := m["display_name"].(string)
			ownedBy, _ := m["owned_by"].(string)
			isFree, _ := m["is_free"].(bool)
			// 拉取后全部默认不启用（enabled=0），由「全部测试」按真实连通性决定启用与否
			if _, err := stmt.Exec(id, modelID, displayName, ownedBy, 0, isFree); err != nil {
				tx.Rollback()
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("写入模型失败(insert#%d %s): %s", idx, modelID, err.Error())})
				return
			}
		}
		if err := tx.Commit(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入模型失败(commit): " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": fmt.Sprintf("成功获取 %d 个模型", len(fetched)),
		"models":  fetched,
	})
}

// resolveChatURL 将服务商 base_url 补全为完整的 chat/completions 端点。
func resolveChatURL(baseURL string) string {
	if baseURL == "" {
		return baseURL
	}
	if strings.Contains(baseURL, "?") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/chat/completions") || strings.HasSuffix(baseURL, "/messages") {
		return baseURL
	}
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

// testModelConnectivity 用指定模型发起一次极简聊天请求，验证该模型是否可用。
// testMaxRetries 是 testModelConnectivity 对可重试错误的最大重试次数。
const testMaxRetries = 3

// testRateLimitPerMinute 返回测试连通性时每个账号每分钟的请求上限；0 表示不限速。
// 仅对上游有硬性速率限制的服务商生效：
//   - nvidia：NVIDIA NIM 每账号每分钟 40 次，这里留 2 次余量，避免边界抖动触发 429。
func testRateLimitPerMinute(apiType string) int {
	switch apiType {
	case "nvidia":
		return 38
	}
	return 0
}

// acctRateLimiter 按账号做固定窗口限速，用于测试连通性路径：没有配额时阻塞等待，
// 而不是像运行时选路那样跳过。窗口粒度固定为 1 分钟，与上游 RPM 口径一致。
type acctRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*acctRLWindow
	limit   int // 每窗口每账号最大请求数；<=0 表示不限速
	window  time.Duration
}

type acctRLWindow struct {
	mu    sync.Mutex
	start time.Time
	count int
}

func newAcctRateLimiter(limit int, window time.Duration) *acctRateLimiter {
	return &acctRateLimiter{
		windows: make(map[string]*acctRLWindow),
		limit:   limit,
		window:  window,
	}
}

// acquire 阻塞直到该账号在窗口内有配额（窗口过期自动重置）。limit<=0 时直接放行。
func (l *acctRateLimiter) acquire(key string) {
	if l.limit <= 0 {
		return
	}
	l.mu.Lock()
	w, ok := l.windows[key]
	if !ok {
		w = &acctRLWindow{start: time.Now()}
		l.windows[key] = w
	}
	l.mu.Unlock()
	for {
		w.mu.Lock()
		now := time.Now()
		if now.Sub(w.start) >= l.window {
			w.start = now
			w.count = 0
		}
		if w.count < l.limit {
			w.count++
			w.mu.Unlock()
			return
		}
		wait := l.window - now.Sub(w.start)
		w.mu.Unlock()
		time.Sleep(wait)
	}
}

// testModelConnectivity 向上游发起一次极简 chat 请求以验证模型可用性。
// 针对 serverless（魔塔 api-inference 等）冷启动场景：
//   - 超时（含 cold start）、429 限流、5xx 服务端错误、返回体含「模型加载中」均可重试
//   - 连接被拒 / DNS / 4xx 认证等持久性错误不重试，直接判定失败
//   - 退避 3s / 6s / 12s，给冷模型留出预热时间
func (h *ProviderHandler) testModelConnectivity(baseURL, apiKey, modelID string, apiTypes ...string) (bool, string) {
	chatURL := resolveChatURL(baseURL)
	payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"stream":false}`, modelID)

	// OpenCode 免费层：未绑定 key 时使用官方匿名凭据 "public"，并带客户端标识头（参考 9Router executor）
	apiType := ""
	if len(apiTypes) > 0 {
		apiType = apiTypes[0]
	}
	if apiType == "opencode" && apiKey == "" {
		apiKey = "public"
	}

	var lastMsg string
	for attempt := 0; attempt <= testMaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 3 * time.Second // 3s, 6s, 12s
			time.Sleep(backoff)
		}

		req, err := http.NewRequest(http.MethodPost, chatURL, strings.NewReader(payload))
		if err != nil {
			return false, "构造请求失败：" + err.Error()
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if apiType == "opencode" {
			req.Header.Set("x-opencode-client", "desktop")
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 60 * time.Second} // serverless 冷启动可能耗时较长
		resp, err := client.Do(req)
		if err != nil {
			// 超时（含 cold start 慢响应）可重试；连接被拒 / DNS 等持久错误不重试
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				lastMsg = "连接超时（疑似冷启动）：" + err.Error()
				continue
			}
			return false, "无法连接：" + err.Error()
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(body)
		if len(bodyStr) > 500 {
			bodyStr = bodyStr[:500]
		}

		// 成功（2xx 且非「加载中」错误体）
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if isModelLoadingError(bodyStr) {
				lastMsg = "模型加载中（冷启动）：" + bodyStr
				if attempt < testMaxRetries {
					continue
				}
				break
			}
			return true, ""
		}

		lastMsg = fmt.Sprintf("HTTP %d：%s", resp.StatusCode, bodyStr)

		// 429 限流 / 5xx 服务端错误：可重试
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < testMaxRetries {
			continue
		}
		// 4xx 其它（认证、参数等）：不可重试，立即返回
		break
	}
	return false, lastMsg
}

// isModelLoadingError 判断响应体是否为 serverless 冷启动导致的「模型加载中」错误。
// 仅当同时含 error 标记与 loading 类关键词时才判定为可重试的加载错误，避免误伤正常响应。
func isModelLoadingError(body string) bool {
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "error") {
		return false
	}
	for _, kw := range []string{
		"loading", "being loaded", "not ready", "still warming",
		"cold start", "cold-start", "try again", "retry later",
		"稍后", "加载", "预热", "启动中",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ToggleModel 启用/停用单个模型。
func (h *ProviderHandler) ToggleModel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
		Enabled int    `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model_id 为必填项"})
		return
	}
	res, err := h.db.Exec(
		"UPDATE models SET enabled = ? WHERE provider_id = ? AND model_id = ?",
		req.Enabled, id, req.ModelID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新模型状态失败"})
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该模型"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "模型状态已更新", "model_id": req.ModelID, "enabled": req.Enabled})
}

// TestModel 测试单个模型的连通性，并据结果自动设置其启用状态。
func (h *ProviderHandler) TestModel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model_id 为必填项"})
		return
	}
	var baseURL, apiType string
	if err := h.db.QueryRow("SELECT base_url, api_type FROM providers WHERE id = ?", id).Scan(&baseURL, &apiType); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务商失败"})
		return
	}
	// 轮询所有启用账户：Cloudflare 等「ID+Key」服务商每个账户有自己的 extra_config。
	// 用速率限流保护避免单账号打满上游限制（如 NVIDIA 每账号每分钟 40 次）。
	creds := h.getProviderAccountCreds(id)
	if len(creds) == 0 {
		creds = []accountCred{{}}
	}
	limiter := newAcctRateLimiter(testRateLimitPerMinute(apiType), time.Minute)

	var ok bool
	var msg string
	for _, cred := range creds {
		resolvedBase := proxy.SubstituteTemplate(baseURL, cred.ExtraConfig)
		limiter.acquire(cred.APIKey)
		ok, msg = h.testModelConnectivity(resolvedBase, cred.APIKey, req.ModelID, apiType)
		if ok {
			break
		}
	}
	enabled := 0
	if ok {
		enabled = 1
	}
	h.db.Exec("UPDATE models SET enabled = ? WHERE provider_id = ? AND model_id = ?", enabled, id, req.ModelID)
	c.JSON(http.StatusOK, gin.H{
		"model_id": req.ModelID,
		"ok":       ok,
		"message":  msg,
		"enabled":  enabled,
	})
}

// TestAllModels 并发测试该服务商下全部模型的连通性，成功者启用（已添加），失败者停用（未添加）。
// 采用 SSE 流式输出：先推 total，每个模型测完实时推一条 result，全部完成推 done，便于前端展示进度条。
func (h *ProviderHandler) TestAllModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var baseURL, apiType string
	if err := h.db.QueryRow("SELECT base_url, api_type FROM providers WHERE id = ?", id).Scan(&baseURL, &apiType); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务商失败"})
		return
	}
	// 轮询所有启用账户：每个账户有自己的 extra_config，Cloudflare 等「ID+Key」服务商
	// 必须用对应账户的 accountId 替换 URL 模板，否则账号轮询的 key 与 accountId 不匹配 → 401。
	creds := h.getProviderAccountCreds(id)
	if len(creds) == 0 {
		creds = []accountCred{{}} // 无账号时保持原行为（如 OpenCode 匿名免费层）
	}
	limiter := newAcctRateLimiter(testRateLimitPerMinute(apiType), time.Minute)
	var nextCred int64 // 账号轮询计数器（atomic 安全）

	// 调试日志：打印轮询账号数量与脱敏 key 前缀，便于排查"只用一个号"问题
	for i, c := range creds {
		prefix := ""
		if len(c.APIKey) > 4 {
			prefix = c.APIKey[:2] + "****" + c.APIKey[len(c.APIKey)-2:]
		}
		logger.Debug("TestAllModels 轮询账号", "provider_id", id, "idx", i, "prefix", prefix, "extra_config", c.ExtraConfig)
	}

	rows, err := h.db.Query("SELECT model_id, display_name FROM models WHERE provider_id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模型失败"})
		return
	}
	type modelRow struct {
		modelID     string
		displayName string
	}
	ms := make([]modelRow, 0)
	for rows.Next() {
		var mid, dn string
		if err := rows.Scan(&mid, &dn); err != nil {
			continue
		}
		ms = append(ms, modelRow{mid, dn})
	}
	rows.Close()

	if len(ms) == 0 {
		c.JSON(http.StatusOK, gin.H{"total": 0, "ok_count": 0, "fail_count": 0, "results": []gin.H{}})
		return
	}

	// 切换到 SSE 流式输出
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var writeMu sync.Mutex
	writeEvent := func(ev gin.H) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		b, err := json.Marshal(ev)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		w.Flush()
		return true
	}

	// 先推总量，前端进度条分母确定
	if !writeEvent(gin.H{"type": "start", "total": len(ms)}) {
		return
	}

	// 并发度动态计算：基于账号数量 × 每个账号每分钟的测试配额，
	// 取一个合理的并发上限（最多 50），避免对 serverless 服务商（魔塔等）造成 pile-up。
	baseSem := len(creds) * testRateLimitPerMinute(apiType)
	if baseSem <= 0 {
		baseSem = 10 // 无速率限制时默认10并发
	}
	// 对需要限速的服务商（nvidia），按账号数分摊并发槽；对无限制的服务商用固定上限
	var semCapacity int
	if testRateLimitPerMinute(apiType) > 0 {
		semCapacity = len(creds) * 5 // 每个账号预留5个并发槽，避免单次打满
	} else {
		semCapacity = min(baseSem, 20)
	}
	if semCapacity < 1 {
		semCapacity = 1
	}
	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		sem       = make(chan struct{}, semCapacity)
		okCount   int
		failCount int
	)
	for i, m := range ms {
		wg.Add(1)
		go func(idx int, m modelRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// 轮询选取账号并等待该账号窗口配额（阻塞式限速，避免单账号 429）
			ci := atomic.AddInt64(&nextCred, 1)
			cred := creds[(ci-1)%int64(len(creds))]
			prefix := ""
			if len(cred.APIKey) > 4 {
				prefix = cred.APIKey[:2] + "****" + cred.APIKey[len(cred.APIKey)-2:]
			}
			logger.Debug("TestAllModels 分配账号", "provider_id", id, "model", m.modelID, "counter", ci, "idx", (ci-1)%int64(len(creds)), "prefix", prefix)
			// 用该账号独有的 extra_config 替换 URL 模板（如 Cloudflare 的 accountId）
			modelResolvedBase := proxy.SubstituteTemplate(baseURL, cred.ExtraConfig)
			limiter.acquire(cred.APIKey)
			ok, msg := h.testModelConnectivity(modelResolvedBase, cred.APIKey, m.modelID, apiType)
			enabled := 0
			if ok {
				enabled = 1
			}
			h.db.Exec("UPDATE models SET enabled = ? WHERE provider_id = ? AND model_id = ?", enabled, id, m.modelID)
			mu.Lock()
			if ok {
				okCount++
			} else {
				failCount++
			}
			mu.Unlock()
			// 实时推送单个模型结果（客户端断开时停止推送，不再阻塞）
			writeEvent(gin.H{
				"type":         "result",
				"model_id":     m.modelID,
				"display_name": m.displayName,
				"ok":           ok,
				"message":      msg,
				"enabled":      enabled,
			})
		}(i, m)
	}
	wg.Wait()

	writeEvent(gin.H{
		"type":       "done",
		"total":      len(ms),
		"ok_count":   okCount,
		"fail_count": failCount,
	})
}
