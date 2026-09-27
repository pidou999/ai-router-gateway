package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-router-gateway/internal/auth"
	"ai-router-gateway/internal/db"

	"github.com/gin-gonic/gin"
)

type AdminHandler struct {
	db *sql.DB
}

func NewAdminHandler(db *sql.DB) *AdminHandler {
	return &AdminHandler{db: db}
}

func (h *AdminHandler) ListUsers(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT u.id, u.username, u.email, u.role, u.team_id, u.created_at, u.updated_at
		FROM users u
		WHERE u.role != 'admin'
		  AND EXISTS (
		    SELECT 1 FROM providers p WHERE p.user_id = u.id
		  )
		ORDER BY u.id ASC`,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id int64
		var teamID sql.NullInt64
		var username, email, role, createdAt, updatedAt string
		if err := rows.Scan(&id, &username, &email, &role, &teamID, &createdAt, &updatedAt); err != nil {
			continue
		}
		users = append(users, gin.H{
			"id":         id,
			"username":   username,
			"email":      email,
			"role":       role,
			"team_id":    teamID.Int64,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}
	if users == nil {
		users = []gin.H{}
	}
	c.JSON(http.StatusOK, users)
}

func (h *AdminHandler) UpdateUserRole(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "角色为必填项"})
		return
	}

	now := time.Now().Format(time.RFC3339)
	result, err := h.db.Exec(
		"UPDATE users SET role = ?, updated_at = ? WHERE id = ?",
		req.Role, now, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新用户角色失败"})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到用户"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "用户角色已更新"})
}

// UpdateUser 管理员修改指定用户：可重置密码（需校验当前密码）与角色。
// 密码修改走与 ChangePassword 相同的路径：需传 currentPassword + newPassword，仅传新密码不生效。
func (h *AdminHandler) UpdateUser(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		Role            string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sets := []string{}
	args := []interface{}{}

	// 密码修改：必须同时提供当前密码和新密码，且新密码至少 6 位
	if req.NewPassword != "" {
		if req.CurrentPassword == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "修改密码需要填写当前密码"})
			return
		}
		if len(req.NewPassword) < 6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "新密码至少 6 位"})
			return
		}
		// 校验当前密码
		var passwordHash string
		if err := h.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", id).Scan(&passwordHash); err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": "未找到用户"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
			return
		}
		if !auth.CheckPassword(passwordHash, req.CurrentPassword) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "当前密码不正确"})
			return
		}
		hash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
			return
		}
		sets = append(sets, "password_hash = ?")
		args = append(args, hash)
	}

	if req.Role != "" {
		if req.Role != "admin" && req.Role != "member" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "角色值无效"})
			return
		}
		sets = append(sets, "role = ?")
		args = append(args, req.Role)
	}
	if len(sets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有提供任何要更新的字段"})
		return
	}
	now := time.Now().Format(time.RFC3339)
	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	args = append(args, id)

	query := "UPDATE users SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	if _, err := h.db.Exec(query, args...); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新用户失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "用户已更新"})
}

// DeleteUser 管理员删除指定用户。禁止删除自己，且禁止删除最后一个管理员。
// 采用软删除策略：先禁用关联账户（保留历史数据可查），再删除 API 密钥与组合配置，最后删除用户。
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}
	if id == auth.GetUserID(c) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除当前登录的账户"})
		return
	}

	var role string
	if err := h.db.QueryRow("SELECT role FROM users WHERE id = ?", id).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到用户"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	if role == "admin" {
		var adminCount int
		_ = h.db.QueryRow("SELECT COUNT(*) FROM users WHERE role = 'admin'").Scan(&adminCount)
		if adminCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除最后一个管理员"})
			return
		}
	}

	// 软删除：禁用该用户所有账户（保留记录），删除 API 密钥和组合配置，最后删除用户。
	if _, err := h.db.Exec("UPDATE accounts SET enabled = 0 WHERE user_id = ?", id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理账户失败"})
		return
	}
	if _, err := h.db.Exec("DELETE FROM api_keys WHERE user_id = ?", id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理 API 密钥失败"})
		return
	}
	if _, err := h.db.Exec("DELETE FROM combos WHERE user_id = ?", id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理组合配置失败"})
		return
	}
	result, err := h.db.Exec("DELETE FROM users WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除用户失败"})
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到用户"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "用户已删除"})
}

func (h *AdminHandler) GetSettings(c *gin.Context) {
	rows, err := h.db.Query("SELECT key, value FROM settings")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询设置失败"})
		return
	}
	defer rows.Close()

	settings := make(map[string]interface{})
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		if b, err := strconv.ParseBool(value); err == nil {
			settings[key] = b
		} else if n, err := strconv.ParseFloat(value, 64); err == nil {
			settings[key] = n
		} else {
			settings[key] = value
		}
	}
	c.JSON(http.StatusOK, settings)
}

func (h *AdminHandler) UpdateSettings(c *gin.Context) {
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().Format(time.RFC3339)
	for key, value := range req {
		strValue := fmt.Sprintf("%v", value)
		_, err := h.db.Exec(
			db.UpsertSettingsSQL(db.DialectSQLite),
			key, strValue, now,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新设置失败"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "设置已更新"})
}
