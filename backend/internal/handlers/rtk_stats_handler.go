package handlers

import (
	"net/http"

	"ai-router-gateway/internal/compressor"

	"github.com/gin-gonic/gin"
)

// RTKStatsHandler 处理 RTK 压缩统计请求
type RTKStatsHandler struct{}

// NewRTKStatsHandler 创建 RTK 压缩统计处理器
func NewRTKStatsHandler() *RTKStatsHandler {
	return &RTKStatsHandler{}
}

// GetStats 返回 RTK 压缩统计信息
func (h *RTKStatsHandler) GetStats(c *gin.Context) {
	stats := compressor.GetGlobalStats()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"total_compressions":   stats.TotalCompressions,
			"total_saved_tokens":   stats.TotalSavedTokens,
			"filter_usage":         stats.FilterUsage,
			"recent_compressions":  stats.RecentCompressions,
			"start_time":           stats.StartTime,
		},
	})
}

// ResetStats 重置压缩统计
func (h *RTKStatsHandler) ResetStats(c *gin.Context) {
	compressor.ResetStats()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "统计已重置",
	})
}

// TestCompression 测试压缩效果
func (h *RTKStatsHandler) TestCompression(c *gin.Context) {
	var req struct {
		Content string `json:"content" binding:"required"`
		Level   int    `json:"level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content 是必填字段"})
		return
	}

	if req.Level < 1 || req.Level > 9 {
		req.Level = 1
	}

	r := compressor.NewRTKCompressor()
	filterName := r.AutoDetectFilter(req.Content)
	compressed, origTokens, compTokens := r.Compress(req.Content, "tool_result", req.Level)

	saved := origTokens - compTokens
	if saved < 0 {
		saved = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"filter":          filterName,
			"level":           req.Level,
			"original_size":   len(req.Content),
			"compressed_size": len(compressed),
			"original_tokens": origTokens,
			"compressed_tokens": compTokens,
			"saved_tokens":    saved,
			"compression_ratio": func() float64 {
				if origTokens > 0 {
					return float64(saved) / float64(origTokens) * 100
				}
				return 0
			}(),
			"compressed_content": compressed,
		},
	})
}
