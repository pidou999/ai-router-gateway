package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-router-gateway/internal/auth"
	"ai-router-gateway/internal/crypto"
	"ai-router-gateway/internal/proxy"

	"github.com/gin-gonic/gin"
)

type AccountCreateRequest struct {
	ProviderID   int64  `json:"provider_id"`
	Name         string `json:"name"`
	APIKey       string `json:"api_key"`
	RateLimitRPM int    `json:"rate_limit_rpm"`
	RateLimitTPM int    `json:"rate_limit_tpm"`
	OAuthConfig  string `json:"oauth_config"`
	// ExtraConfig 存储服务商私有配置（JSON 字符串），如 Cloudflare 的 accountId。
	ExtraConfig  string `json:"extra_config"`
}

type AccountUpdateRequest struct {
	Name         *string `json:"name"`
	APIKey       string  `json:"api_key"`
	RateLimitRPM *int    `json:"rate_limit_rpm"`
	RateLimitTPM *int    `json:"rate_limit_tpm"`
	Enabled      *bool   `json:"enabled"`
	OAuthConfig  *string `json:"oauth_config"`
	// ExtraConfig 存储服务商私有配置（JSON 字符串），如 Cloudflare 的 accountId。
	ExtraConfig *string `json:"extra_config"`
}

type AccountHandler struct {
	db            *sql.DB
	encryptionKey string
}

func NewAccountHandler(db *sql.DB, encryptionKey string) *AccountHandler {
	return &AccountHandler{db: db, encryptionKey: encryptionKey}
}

func (h *AccountHandler) encryptAPIKey(key string) string {
	enc, err := crypto.Encrypt(key, h.encryptionKey)
	if err != nil {
		return ""
	}
	return enc
}

func (h *AccountHandler) decryptAPIKey(encrypted string) string {
	plain, err := crypto.Decrypt(encrypted, h.encryptionKey)
	if err != nil {
		return encrypted
	}
	return plain
}

func (h *AccountHandler) apiKeyPrefix(encrypted string) string {
	plain := h.decryptAPIKey(encrypted)
	if len(plain) > 8 {
		return plain[len(plain)-8:]
	}
	return plain
}

func (h *AccountHandler) ListAccounts(c *gin.Context) {
	role := auth.GetRole(c)
	userID := auth.GetUserID(c)

	var rows *sql.Rows
	var err error
	if role == "admin" {
		rows, err = h.db.Query(
			"SELECT a.id, a.provider_id, a.user_id, a.name, a.api_key_encrypted, a.rate_limit_rpm, a.rate_limit_tpm, a.enabled, a.oauth_config, a.created_at, p.name AS provider_name, p.base_url, a.extra_config "+
				"FROM accounts a LEFT JOIN providers p ON a.provider_id = p.id ORDER BY a.created_at DESC",
		)
	} else {
		rows, err = h.db.Query(
			"SELECT a.id, a.provider_id, a.user_id, a.name, a.api_key_encrypted, a.rate_limit_rpm, a.rate_limit_tpm, a.enabled, a.oauth_config, a.created_at, p.name AS provider_name, p.base_url, a.extra_config "+
				"FROM accounts a LEFT JOIN providers p ON a.provider_id = p.id WHERE a.user_id = ? ORDER BY a.created_at DESC",
			userID,
		)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询账户失败"})
		return
	}
	defer rows.Close()

	var accounts []gin.H
	for rows.Next() {
		var id, providerID, userID, rateLimitRPM, rateLimitTPM, enabled int64
		var name, apiKeyEncrypted, oauthConfig, createdAt, providerName, baseURL, extraConfig string
		if err := rows.Scan(&id, &providerID, &userID, &name, &apiKeyEncrypted, &rateLimitRPM, &rateLimitTPM, &enabled, &oauthConfig, &createdAt, &providerName, &baseURL, &extraConfig); err != nil {
			continue
		}
		accounts = append(accounts, gin.H{
			"id":             id,
			"provider_id":    providerID,
			"user_id":        userID,
			"name":           name,
			"provider_name":  providerName,
			"base_url":       baseURL,
			"api_key_prefix": h.apiKeyPrefix(apiKeyEncrypted),
			"rate_limit_rpm": rateLimitRPM,
			"rate_limit_tpm": rateLimitTPM,
			"enabled":        enabled,
			"oauth_config":   oauthConfig,
			"extra_config":   extraConfig,
			"created_at":     createdAt,
		})
	}
	if accounts == nil {
		accounts = []gin.H{}
	}
	c.JSON(http.StatusOK, accounts)
}

type AccountTestRequest struct {
	ProviderID int64  `json:"provider_id"`
	APIKey     string `json:"api_key"`
	BaseURL    string `json:"base_url"`
	// ExtraConfig 服务商私有配置（JSON），如 Cloudflare 的 accountId，用于替换 base_url 模板占位符
	ExtraConfig string `json:"extra_config"`
}

// TestAccount 用用户填写的 API Key 探测服务商连通性。
// 探测策略：优先尝试 GET /models（OpenAI 兼容），失败时回退到 POST /chat/completions 的极简请求。
func (h *AccountHandler) TestAccount(c *gin.Context) {
	var req AccountTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ProviderID == 0 || req.APIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider_id 和 api_key 均为必填项"})
		return
	}

	var baseURL string
	if req.BaseURL != "" {
		baseURL = req.BaseURL
	} else {
		err := h.db.QueryRow("SELECT base_url FROM providers WHERE id = ?", req.ProviderID).Scan(&baseURL)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到服务商"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务商失败"})
			return
		}
	}

	// 替换 base_url 模板中的命名占位符（如 {accountId}），确保包含 accountId 的服务商也能正确探测
	baseURL = proxy.SubstituteTemplate(baseURL, req.ExtraConfig)

	// 统一处理：去掉尾部斜杠，确保 URL 干净
	baseURL = strings.TrimRight(baseURL, "/")

	// 策略 1：尝试 GET /models 端点
	modelsURL := baseURL
	if strings.HasSuffix(modelsURL, "/chat/completions") {
		modelsURL = modelsURL[:len(modelsURL)-len("/chat/completions")]
	}
	modelsURL = modelsURL + "/models"

	var probeErr string
	if ok, msg := h.probeGET(modelsURL, req.APIKey); ok {
		c.JSON(http.StatusOK, gin.H{"ok": true, "message": "连接成功，/models 端点可用"})
		return
	} else {
		probeErr = msg
	}

	// 策略 2：回退到 POST /chat/completions 极简请求（覆盖 /models 不存在的服务商）
	chatURL := baseURL
	if !strings.HasSuffix(chatURL, "/chat/completions") {
		chatURL = chatURL + "/chat/completions"
	}
	if ok, _ := h.probeChat(chatURL, req.APIKey); ok {
		c.JSON(http.StatusOK, gin.H{"ok": true, "message": "连接成功，聊天端点可用"})
		return
	}

	// 两种策略都失败，返回最详细的错误
	c.JSON(http.StatusOK, gin.H{
		"ok":      false,
		"message": fmt.Sprintf("连接失败：%s（已尝试 /models 和 /chat/completions 两种探测）", probeErr),
	})
}

func (h *AccountHandler) probeGET(urlStr, apiKey string) (bool, string) {
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		return false, "构造请求失败：" + err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if len(bodyStr) > 300 {
		bodyStr = bodyStr[:300] + "..."
	}

	if resp.StatusCode == http.StatusOK {
		return true, ""
	}
	return false, fmt.Sprintf("GET %s → HTTP %d（%s）", urlStr, resp.StatusCode, bodyStr)
}

func (h *AccountHandler) probeChat(urlStr, apiKey string) (bool, string) {
	payload := `{"model":"test","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`
	req, err := http.NewRequest(http.MethodPost, urlStr, strings.NewReader(payload))
	if err != nil {
		return false, "构造请求失败：" + err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if len(bodyStr) > 300 {
		bodyStr = bodyStr[:300] + "..."
	}

	// 聊天端点：2xx 成功；鉴权类 4xx（401/403/407）必须判失败；
	// 其余 4xx（400/404/422/429 等）说明端点可达且鉴权已通过，仅 payload/模型名不完整，
	// 视为连通成功（例如 Cloudflare Workers AI 对未知模型返回 404，但鉴权有效）。
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ""
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
		resp.StatusCode != http.StatusUnauthorized &&
		resp.StatusCode != http.StatusForbidden &&
		resp.StatusCode != http.StatusProxyAuthRequired {
		return true, ""
	}
	return false, fmt.Sprintf("POST %s → HTTP %d（%s）", urlStr, resp.StatusCode, bodyStr)
}

func (h *AccountHandler) GetAccount(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var accountID, providerID, userID, rateLimitRPM, rateLimitTPM, enabled int64
	var name, apiKeyEncrypted, oauthConfig, createdAt, extraConfig string
	err = h.db.QueryRow(
		"SELECT id, provider_id, user_id, name, api_key_encrypted, rate_limit_rpm, rate_limit_tpm, enabled, oauth_config, created_at, extra_config FROM accounts WHERE id = ?",
		id,
	).Scan(&accountID, &providerID, &userID, &name, &apiKeyEncrypted, &rateLimitRPM, &rateLimitTPM, &enabled, &oauthConfig, &createdAt, &extraConfig)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到账户"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询账户失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":             accountID,
		"provider_id":    providerID,
		"user_id":        userID,
		"name":           name,
		"api_key_prefix": h.apiKeyPrefix(apiKeyEncrypted),
		"rate_limit_rpm": rateLimitRPM,
		"rate_limit_tpm": rateLimitTPM,
		"enabled":        enabled,
		"oauth_config":   oauthConfig,
		"extra_config":   extraConfig,
		"created_at":     createdAt,
	})
}

// GetAccountKey 返回指定账户的完整明文 API Key（仅管理员）
func (h *AccountHandler) GetAccountKey(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var apiKeyEncrypted string
	err = h.db.QueryRow("SELECT api_key_encrypted FROM accounts WHERE id = ? AND COALESCE(enabled, 0) != 0", id).Scan(&apiKeyEncrypted)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到账户"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"api_key": h.decryptAPIKey(apiKeyEncrypted),
	})
}

func (h *AccountHandler) CreateAccount(c *gin.Context) {
	userID := auth.GetUserID(c)

	var req AccountCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name == "" || req.APIKey == "" || req.ProviderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称、api_key 和 provider_id 均为必填项"})
		return
	}

	encryptedKey := h.encryptAPIKey(req.APIKey)
	now := time.Now().Format(time.RFC3339)

	result, err := h.db.Exec(
		"INSERT INTO accounts (provider_id, user_id, name, api_key_encrypted, rate_limit_rpm, rate_limit_tpm, oauth_config, extra_config, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		req.ProviderID, userID, req.Name, encryptedKey, req.RateLimitRPM, req.RateLimitTPM, req.OAuthConfig, req.ExtraConfig, 1, now, now,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建账户失败"})
		return
	}
	id, _ := result.LastInsertId()
	c.JSON(http.StatusCreated, gin.H{
		"id":             id,
		"provider_id":    req.ProviderID,
		"name":           req.Name,
		"api_key_prefix": h.apiKeyPrefix(encryptedKey),
		"rate_limit_rpm": req.RateLimitRPM,
		"rate_limit_tpm": req.RateLimitTPM,
		"enabled":        1,
		"oauth_config":   req.OAuthConfig,
		"extra_config":   req.ExtraConfig,
	})
}

func (h *AccountHandler) UpdateAccount(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var req AccountUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().Format(time.RFC3339)
	// 动态构建 SET 子句，只更新前端实际发送的字段，避免清空其他字段为零值。
	var setParts []string
	var args []interface{}

	if req.APIKey != "" {
		encryptedKey := h.encryptAPIKey(req.APIKey)
		setParts = append(setParts, "api_key_encrypted = ?")
		args = append(args, encryptedKey)
	}
	if req.Name != nil {
		setParts = append(setParts, "name = ?")
		args = append(args, *req.Name)
	}
	if req.RateLimitRPM != nil {
		setParts = append(setParts, "rate_limit_rpm = ?")
		args = append(args, *req.RateLimitRPM)
	}
	if req.RateLimitTPM != nil {
		setParts = append(setParts, "rate_limit_tpm = ?")
		args = append(args, *req.RateLimitTPM)
	}
	if req.Enabled != nil {
		setParts = append(setParts, "enabled = ?")
		args = append(args, *req.Enabled)
	}
	if req.OAuthConfig != nil {
		setParts = append(setParts, "oauth_config = ?")
		args = append(args, *req.OAuthConfig)
	}
	if req.ExtraConfig != nil {
		setParts = append(setParts, "extra_config = ?")
		args = append(args, *req.ExtraConfig)
	}
	setParts = append(setParts, "updated_at = ?")
	args = append(args, now)
	args = append(args, id)

	if len(setParts) == 1 {
		// 只有 updated_at，没有任何业务字段需要更新
		c.JSON(http.StatusOK, gin.H{"message": "账户已更新"})
		return
	}

	_, err = h.db.Exec(
		"UPDATE accounts SET " + strings.Join(setParts, ", ") + " WHERE id = ?",
		args...,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新账户失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "账户已更新"})
}

func (h *AccountHandler) DeleteAccount(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	result, err := h.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除账户失败"})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到账户"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "账户已删除"})
}
