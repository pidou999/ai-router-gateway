package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"ai-router-gateway/internal/auth"

	"github.com/gin-gonic/gin"
)

type LogHandler struct {
	db *sql.DB
}

func NewLogHandler(db *sql.DB) *LogHandler {
	return &LogHandler{db: db}
}

// statusToCode 将存储的状态字符串转为 HTTP 状态码
func statusToCode(s string) int {
	switch s {
	case "success":
		return 200
	default:
		return 500
	}
}

// extractRequestID 从 request_details JSON 中提取 request_id
func extractRequestID(detailsJSON sql.NullString) string {
	if !detailsJSON.Valid || detailsJSON.String == "" {
		return ""
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(detailsJSON.String), &details); err != nil {
		return ""
	}
	if rid, ok := details["request_id"].(string); ok {
		return rid
	}
	return ""
}

func (h *LogHandler) ListLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	offset := (page - 1) * limit

	modelFilter := c.Query("model")
	statusFilter := c.Query("status")
	startTime := c.Query("start")
	endTime := c.Query("end")

	var conditions []string
	var args []interface{}

	role := auth.GetRole(c)
	currentUserID := auth.GetUserID(c)

	if role != "admin" {
		conditions = append(conditions, "r.user_id = ?")
		args = append(args, currentUserID)
	}

	if modelFilter != "" {
		conditions = append(conditions, "r.model LIKE ?")
		args = append(args, "%"+modelFilter+"%")
	}
	if statusFilter != "" {
		// 前端传数字状态码，转为后端存储格式
		code, _ := strconv.Atoi(statusFilter)
		if code >= 200 && code < 400 {
			conditions = append(conditions, "r.status = 'success'")
		} else if code >= 400 {
			conditions = append(conditions, "r.status = 'error'")
		}
	}
	if startTime != "" {
		conditions = append(conditions, "r.created_at >= ?")
		args = append(args, startTime)
	}
	if endTime != "" {
		conditions = append(conditions, "r.created_at <= ?")
		args = append(args, endTime)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	var total int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM request_logs r %s", whereClause)
	h.db.QueryRow(countQuery, args...).Scan(&total)

	query := fmt.Sprintf(
		`SELECT r.id, r.model, r.picked_model, r.request_tokens, r.response_tokens, r.latency_ms, r.status,
		        r.error_message, r.request_body, r.response_body, r.request_details,
		        r.created_at, p.name AS provider_name
		 FROM request_logs r
		 LEFT JOIN providers p ON r.provider_id = p.id
		 %s ORDER BY r.created_at DESC LIMIT ? OFFSET ?`,
		whereClause,
	)
	args = append(args, limit, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询日志失败"})
		return
	}
	defer rows.Close()

	var logs []gin.H
	for rows.Next() {
		var id int64
		var model, pickedModel, status, errorMessage, requestBody, responseBody, createdAt, providerName sql.NullString
		var requestTokens, responseTokens, latencyMs sql.NullInt64
		var requestDetails sql.NullString

		if err := rows.Scan(&id, &model, &pickedModel, &requestTokens, &responseTokens, &latencyMs, &status,
			&errorMessage, &requestBody, &responseBody, &requestDetails, &createdAt, &providerName); err != nil {
			continue
		}

		promptTokens := int64(0)
		completionTokens := int64(0)
		if requestTokens.Valid {
			promptTokens = requestTokens.Int64
		}
		if responseTokens.Valid {
			completionTokens = responseTokens.Int64
		}

		statusCode := 500
		if status.Valid {
			statusCode = statusToCode(status.String)
		}

		item := gin.H{
			"id":               id,
			"request_id":       extractRequestID(requestDetails),
			"model":            nullString(model),
			"picked_model":     nullString(pickedModel),
			"status":           statusCode,
			"latency_ms":      nullInt64(latencyMs),
			"prompt_tokens":    promptTokens,
			"completion_tokens": completionTokens,
			"cost":             0.0,
			"created_at":       nullString(createdAt),
			"provider_name":    nullString(providerName),
		}
		if errorMessage.Valid && errorMessage.String != "" {
			item["error_message"] = errorMessage.String
		}
		if requestBody.Valid && requestBody.String != "" {
			item["request_body"] = requestBody.String
		}
		if responseBody.Valid && responseBody.String != "" {
			item["response_body"] = responseBody.String
		}
		if requestDetails.Valid && requestDetails.String != "" {
			item["request_details"] = requestDetails.String
		}
		logs = append(logs, item)
	}
	if logs == nil {
		logs = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  logs,
		"total": total,
	})
}

func (h *LogHandler) GetLogDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var logID int64
	var model, pickedModel, status, errorMessage, createdAt sql.NullString
	var requestTokens, responseTokens, latencyMs sql.NullInt64
	var requestBody, responseBody, requestDetails sql.NullString
	err = h.db.QueryRow(
		"SELECT id, model, picked_model, request_tokens, response_tokens, latency_ms, status, error_message, request_body, response_body, request_details, created_at FROM request_logs WHERE id = ?",
		id,
	).Scan(&logID, &model, &pickedModel, &requestTokens, &responseTokens, &latencyMs, &status, &errorMessage, &requestBody, &responseBody, &requestDetails, &createdAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到日志"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询日志详情失败"})
		return
	}

	resp := gin.H{
		"id":               logID,
		"request_id":       extractRequestID(requestDetails),
		"model":            nullString(model),
		"picked_model":     nullString(pickedModel),
		"status":           statusToCode(nullString(status)),
		"latency_ms":      nullInt64(latencyMs),
		"prompt_tokens":    nullInt64(requestTokens),
		"completion_tokens": nullInt64(responseTokens),
		"cost":             0.0,
		"created_at":       nullString(createdAt),
	}
	if errorMessage.Valid && errorMessage.String != "" {
		resp["error_message"] = errorMessage.String
	}
	if requestBody.Valid {
		resp["request_body"] = requestBody.String
	}
	if responseBody.Valid {
		resp["response_body"] = responseBody.String
	}
	if requestDetails.Valid {
		resp["request_details"] = requestDetails.String
	}

	c.JSON(http.StatusOK, resp)
}

// nullString 安全提取 sql.NullString
func nullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// nullInt64 安全提取 sql.NullInt64
func nullInt64(ni sql.NullInt64) int64 {
	if ni.Valid {
		return ni.Int64
	}
	return 0
}
