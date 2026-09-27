package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-router-gateway/internal/auth"
	"ai-router-gateway/internal/crypto"
	"ai-router-gateway/internal/logger"

	"github.com/gin-gonic/gin"
)

// isUniqueViolation 判断错误是否为 SQLite 唯一约束冲突。
// 纯驱动无关实现：不同 SQLite 驱动的错误类型不一致，统一按错误文本判定。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "constraint failed")
}

type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type APIKeyRequest struct {
	Name   string `json:"name"`
	Scopes string `json:"scopes"`
}

type TeamInviteRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

type AuthHandler struct {
	db            *sql.DB
	jwtSecret     string
	encryptionKey string
}

func NewAuthHandler(db *sql.DB, jwtSecret, encryptionKey string) *AuthHandler {
	return &AuthHandler{db: db, jwtSecret: jwtSecret, encryptionKey: encryptionKey}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Username == "" || req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名、邮箱和密码均为必填项"})
		return
	}
	// 首个注册用户自动成为管理员，其余为普通成员
	var userCount int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	role := "member"
	if userCount == 0 {
		role = "admin"
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}
	result, err := h.db.Exec(
		"INSERT INTO users (username, email, password_hash, role) VALUES (?, ?, ?, ?)",
		req.Username, req.Email, passwordHash, role,
	)
	if err != nil {
		// 唯一约束冲突才是「已存在」，其余错误必须如实暴露，
		// 否则参数数量不符之类的缺陷会被伪装成 409，长期无人察觉。
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "用户名或邮箱已存在"})
			return
		}
		logger.Error("注册用户失败", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败，请稍后重试"})
		return
	}
	userID, _ := result.LastInsertId()
	token, err := auth.GenerateToken(userID, req.Username, role, h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"token": token,
		"user": gin.H{
			"id":       userID,
			"username": req.Username,
			"email":    req.Email,
			"role":     role,
		},
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Username == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名和密码均为必填项"})
		return
	}
	var id int64
	var email, passwordHash, role string
	err := h.db.QueryRow(
		"SELECT id, email, password_hash, role FROM users WHERE username = ?",
		req.Username,
	).Scan(&id, &email, &passwordHash, &role)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
		return
	}
	if !auth.CheckPassword(passwordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	token, err := auth.GenerateToken(id, req.Username, role, h.jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":       id,
			"username": req.Username,
			"email":    email,
			"role":     role,
		},
	})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID := auth.GetUserID(c)
	var id int64
	var username, email, role string
	err := h.db.QueryRow(
		"SELECT id, username, email, role FROM users WHERE id = ?",
		userID,
	).Scan(&id, &username, &email, &role)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到用户"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":       id,
		"username": username,
		"email":    email,
		"role":     role,
	})
}

// ChangePassword 当前登录用户修改自己的密码（需校验旧密码）。
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	userID := auth.GetUserID(c)
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前密码和新密码均为必填项"})
		return
	}
	if len(req.NewPassword) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码至少 6 位"})
		return
	}

	var passwordHash string
	if err := h.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&passwordHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	if !auth.CheckPassword(passwordHash, req.CurrentPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "当前密码不正确"})
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}
	now := time.Now().Format(time.RFC3339)
	if _, err := h.db.Exec(
		"UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?",
		newHash, now, userID,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "修改密码失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "密码已修改"})
}

func (h *AuthHandler) encryptKey(key string) string {
	enc, err := crypto.Encrypt(key, h.encryptionKey)
	if err != nil {
		return ""
	}
	return enc
}

func (h *AuthHandler) decryptKey(encrypted string) string {
	plain, err := crypto.Decrypt(encrypted, h.encryptionKey)
	if err != nil {
		return encrypted
	}
	return plain
}

func (h *AuthHandler) CreateAPIKey(c *gin.Context) {
	userID := auth.GetUserID(c)

	var req APIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称为必填项"})
		return
	}

	prefix, rawKey, hash := auth.GenerateAPIKey()
	encrypted := h.encryptKey(rawKey)

	result, err := h.db.Exec(
		"INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, scopes) VALUES (?, ?, ?, ?, ?, ?)",
		userID, req.Name, hash, prefix, encrypted, req.Scopes,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建 API 密钥失败"})
		return
	}
	id, _ := result.LastInsertId()

	c.JSON(http.StatusCreated, gin.H{
		"id":         id,
		"name":       req.Name,
		"key":        rawKey,
		"prefix":     prefix,
		"scopes":     req.Scopes,
		"created_at": time.Now(),
	})
}

func (h *AuthHandler) ListAPIKeys(c *gin.Context) {
	userID := auth.GetUserID(c)
	rows, err := h.db.Query(
		"SELECT id, name, key_prefix, scopes, enabled, last_used, created_at FROM api_keys WHERE user_id = ? ORDER BY created_at DESC",
		userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 API 密钥失败"})
		return
	}
	defer rows.Close()

	var keys []gin.H
	for rows.Next() {
		var id int64
		var name, prefix, scopes string
		var enabled int
		var lastUsed sql.NullString
		var createdAt string
		if err := rows.Scan(&id, &name, &prefix, &scopes, &enabled, &lastUsed, &createdAt); err != nil {
			continue
		}
		item := gin.H{
			"id":         id,
			"name":       name,
			"prefix":     prefix,
			"scopes":     scopes,
			"enabled":    enabled,
			"created_at": createdAt,
		}
		if lastUsed.Valid {
			item["last_used"] = lastUsed.String
		}
		keys = append(keys, item)
	}
	if keys == nil {
		keys = []gin.H{}
	}
	c.JSON(http.StatusOK, keys)
}

func (h *AuthHandler) RevokeAPIKey(c *gin.Context) {
	userID := auth.GetUserID(c)
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	result, err := h.db.Exec(
		"DELETE FROM api_keys WHERE id = ? AND user_id = ?",
		keyID, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除 API 密钥失败"})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到 API 密钥"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "API 密钥已删除"})
}

// ToggleAPIKey 切换 API 密钥的启用/停用状态
func (h *AuthHandler) ToggleAPIKey(c *gin.Context) {
	userID := auth.GetUserID(c)
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var req struct {
		IsActive bool `json:"isActive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	newEnabled := 0
	if req.IsActive {
		newEnabled = 1
	}

	result, err := h.db.Exec(
		"UPDATE api_keys SET enabled = ? WHERE id = ? AND user_id = ?",
		newEnabled, keyID, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新 API 密钥状态失败"})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到 API 密钥"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "OK", "isActive": req.IsActive})
}

// GetAPIKey 获取 API 密钥的完整明文（解密返回，仅限本人或管理员）
func (h *AuthHandler) GetAPIKey(c *gin.Context) {
	userID := auth.GetUserID(c)
	role := auth.GetRole(c)
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var encrypted string
	var ownerID int64
	err = h.db.QueryRow("SELECT key_encrypted, user_id FROM api_keys WHERE id = ?", keyID).Scan(&encrypted, &ownerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到 API 密钥"})
		return
	}
	if role != "admin" && ownerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问此密钥"})
		return
	}
	if encrypted == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "该密钥无可恢复的完整内容（旧版哈希存储）"})
		return
	}

	plain := h.decryptKey(encrypted)
	c.JSON(http.StatusOK, gin.H{"api_key": plain})
}
