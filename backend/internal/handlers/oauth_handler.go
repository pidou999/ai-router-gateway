// Package handlers 提供 OAuth 2.0 授权流程的 HTTP 处理器。
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"ai-router-gateway/internal/logger"
	"ai-router-gateway/internal/oauth"

	"github.com/gin-gonic/gin"
)

// sessionStore 内存存储 OAuth 授权中间状态（state → Session）。
var (
	sessionMu    sync.RWMutex
	sessionStore = make(map[string]*oauth.Session)
)

// OAuthHandler 处理 OAuth 相关 HTTP 请求。
type OAuthHandler struct {
	db     interface{} // *sql.DB, 用 interface 避免循环依赖
}

func NewOAuthHandler(db interface{}) *OAuthHandler {
	return &OAuthHandler{db: db}
}

// GenerateState 生成随机 state 字符串（用于防 CSRF）。
func generateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// StartOAuth 发起 OAuth 授权流程。
func (h *OAuthHandler) StartOAuth(c *gin.Context) {
	var req struct {
		Provider  string `json:"provider"`
		AccountID int64  `json:"account_id"`
		UserID    int64  `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	provider := oauth.GetProvider(req.Provider)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown provider: %s", req.Provider)})
		return
	}

	state, err := generateState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}

	session := &oauth.Session{
		State:     state,
		Provider:  req.Provider,
		AccountID: req.AccountID,
		UserID:    req.UserID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	// Generate code_verifier for PKCE
	verifier, verr := generateCodeVerifier()
	if verr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate code verifier"})
		return
	}
	session.CodeVerifier = verifier

	sessionMu.Lock()
	sessionStore[state] = session
	sessionMu.Unlock()

	authURL, err := oauth.BuildAuthorizationURL(provider, session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to build auth URL: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"authorization_url": authURL,
		"state":            state,
		"provider":         req.Provider,
	})
}

// OAuthCallback 处理 OAuth 授权回调。
func (h *OAuthHandler) OAuthCallback(c *gin.Context) {
	providerName := c.Param("provider")
	code := c.Query("code")
	state := c.Query("state")

	if code == "" || state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code or state"})
		return
	}

	sessionMu.RLock()
	session, exists := sessionStore[state]
	sessionMu.RUnlock()

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired state"})
		return
	}

	if session.Provider != providerName {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider mismatch"})
		return
	}

	if time.Now().After(session.ExpiresAt) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session expired"})
		return
	}

	provider := oauth.GetProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown provider: %s", providerName)})
		return
	}

	token, err := oauth.ExchangeCode(provider, code, session.CodeVerifier)
	if err != nil {
		logger.Error("OAuth code exchange failed", "provider", providerName, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("OAuth exchange failed: %v", err)})
		return
	}
	_ = token // token 将在回调处理中用于更新数据库

	// Clean up session
	sessionMu.Lock()
	delete(sessionStore, state)
	sessionMu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"provider": providerName,
		"message":  "OAuth authorization successful",
	})
}

// RefreshToken 手动触发 token 刷新。
func (h *OAuthHandler) RefreshToken(c *gin.Context) {
	var req struct {
		AccountID int64 `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// 这里简化实现：实际需要查询数据库获取 oauth_config
	// 生产环境应注入 db 连接
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"account_id": req.AccountID,
		"message":    "Token refresh not yet implemented - need database connection",
	})
}

// ListProviders 列出所有支持的 OAuth 提供商。
func (h *OAuthHandler) ListProviders(c *gin.Context) {
	providers := oauth.ListProviders()
	c.JSON(http.StatusOK, gin.H{
		"providers": providers,
	})
}

// GetTokenStatus 查询账号的 token 状态。
func (h *OAuthHandler) GetTokenStatus(c *gin.Context) {
	accountID := c.Query("account_id")
	if accountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id required"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"account_id": accountID,
		"has_token":  false,
		"message":    "Token status endpoint - need database integration",
	})
}

// generateCodeVerifier 生成 PKCE code_verifier
func generateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
