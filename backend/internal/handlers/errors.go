package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// OpenAI 兼容的错误响应结构。
// 大量客户端（Cursor / Cline / QwenPaw 等）按 OpenAI 规范解析 error 对象，
// 若返回 {"error":"字符串"} 会导致客户端无法提取错误信息、只显示笼统的 "API error"。
type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type apiErrorResponse struct {
	Error apiError `json:"error"`
}

// errorTypeFor 按 HTTP 状态码映射 OpenAI 的 error.type 与 error.code。
func errorTypeFor(status int) (string, string) {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error", "bad_request"
	case http.StatusUnauthorized:
		return "authentication_error", "invalid_api_key"
	case http.StatusForbidden:
		return "permission_error", "forbidden"
	case http.StatusNotFound:
		return "invalid_request_error", "model_not_found"
	case http.StatusTooManyRequests:
		return "rate_limit_error", "rate_limit_exceeded"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "timeout_error", "timeout"
	default:
		if status >= 500 {
			return "api_error", "internal_error"
		}
		return "invalid_request_error", "bad_request"
	}
}

// respondError 以 OpenAI 兼容格式返回错误。
func respondError(c *gin.Context, status int, message string) {
	errType, errCode := errorTypeFor(status)
	c.JSON(status, apiErrorResponse{
		Error: apiError{
			Message: message,
			Type:    errType,
			Code:    errCode,
		},
	})
}
