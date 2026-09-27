package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// abortOpenAIError 以 OpenAI 兼容格式返回错误并中断请求。
// /v1/* 面向 OpenAI 兼容客户端，必须返回 {"error":{"message","type","code"}} 结构，
// 否则客户端（Cursor / Cline / QwenPaw 等）无法提取具体错误信息。
func abortOpenAIError(c *gin.Context, status int, message string) {
	errType, errCode := "api_error", "internal_error"
	switch status {
	case http.StatusUnauthorized:
		errType, errCode = "authentication_error", "invalid_api_key"
	case http.StatusForbidden:
		errType, errCode = "permission_error", "forbidden"
	case http.StatusBadRequest:
		errType, errCode = "invalid_request_error", "bad_request"
	}
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"code":    errCode,
		},
	})
}

func JWTAuthMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "缺少 Authorization 请求头"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization 请求头格式无效"})
			return
		}

		claims, err := ValidateToken(parts[1], secret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "令牌无效或已过期"})
			return
		}

		c.Set("userID", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

func APIKeyAuthMiddleware(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var rawKey string
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				rawKey = parts[1]
			}
		}
		if rawKey == "" {
			rawKey = c.GetHeader("x-api-key")
		}
		if rawKey == "" {
			abortOpenAIError(c, http.StatusUnauthorized, "缺少 API 密钥，请在 Authorization 头中提供 Bearer <your-api-key>")
			return
		}

		h := sha256.Sum256([]byte(rawKey))
		keyHash := hex.EncodeToString(h[:])

		f, _ := os.OpenFile("C:/Users/hedou/.qwenpaw/workspaces/default/api-key-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if f != nil {
			fmt.Fprintf(f, "APIKeyAuth: rawKey=%s keyHash=%s\n", rawKey, keyHash)
			f.Close()
		}

		var userID int64
		var username, role string
		err := db.QueryRow(
			"SELECT k.user_id, u.username, u.role FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash = ? AND k.enabled = 1",
			keyHash,
		).Scan(&userID, &username, &role)
		if err == sql.ErrNoRows {
			if f, _ := os.OpenFile("C:/Users/hedou/.qwenpaw/workspaces/default/api-key-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); f != nil {
				fmt.Fprintf(f, "APIKeyAuth: no rows for keyHash=%s\n", keyHash)
				f.Close()
			}
			abortOpenAIError(c, http.StatusUnauthorized, "API 密钥无效或已禁用")
			return
		}
		if err != nil {
			if f, _ := os.OpenFile("C:/Users/hedou/.qwenpaw/workspaces/default/api-key-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); f != nil {
				fmt.Fprintf(f, "APIKeyAuth: db error=%v\n", err)
				f.Close()
			}
			abortOpenAIError(c, http.StatusInternalServerError, "服务器内部错误："+err.Error())
			return
		}

		if f, _ := os.OpenFile("C:/Users/hedou/.qwenpaw/workspaces/default/api-key-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); f != nil {
			fmt.Fprintf(f, "APIKeyAuth: success user_id=%d username=%s role=%s\n", userID, username, role)
			f.Close()
		}

		c.Set("userID", userID)
		c.Set("username", username)
		c.Set("role", role)
		c.Next()
	}
}

func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "上下文中未找到角色"})
			return
		}

		roleStr, ok := userRole.(string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "角色类型无效"})
			return
		}

		for _, r := range roles {
			if r == roleStr {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "权限不足"})
	}
}

func GetUserID(c *gin.Context) int64 {
	v, exists := c.Get("userID")
	if !exists {
		return 0
	}
	id, ok := v.(int64)
	if !ok {
		return 0
	}
	return id
}

func GetUsername(c *gin.Context) string {
	v, exists := c.Get("username")
	if !exists {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func GetRole(c *gin.Context) string {
	v, exists := c.Get("role")
	if !exists {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
