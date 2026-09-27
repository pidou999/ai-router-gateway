package compressor

import (
	"fmt"
	"sync"
	"time"
)

// RTKStats 压缩统计信息
type RTKStats struct {
	TotalCompressions  int            `json:"total_compressions"`
	TotalSavedTokens   int            `json:"total_saved_tokens"`
	FilterUsage        map[string]int `json:"filter_usage"`
	RecentCompressions []RecentItem   `json:"recent_compressions"`
	StartTime          time.Time      `json:"start_time"`
}

// RecentItem 最近压缩记录
type RecentItem struct {
	Filter         string `json:"filter"`
	SavedTokens    int    `json:"saved_tokens"`
	OriginalSize   int    `json:"original_size"`
	CompressedSize int    `json:"compressed_size"`
	Timestamp      string `json:"timestamp"`
}

var (
	globalStats *RTKStats
	statsMutex  sync.RWMutex
)

func init() {
	globalStats = &RTKStats{
		FilterUsage: make(map[string]int),
		StartTime:   time.Now(),
	}
}

// GetGlobalStats 获取全局压缩统计（返回副本）
func GetGlobalStats() *RTKStats {
	statsMutex.RLock()
	defer statsMutex.RUnlock()
	stats := *globalStats
	return &stats
}

// ResetStats 重置统计（用于测试）
func ResetStats() {
	statsMutex.Lock()
	defer statsMutex.Unlock()
	globalStats = &RTKStats{
		FilterUsage: make(map[string]int),
		StartTime:   time.Now(),
	}
}

// RecordCompression 记录一次压缩操作
func RecordCompression(filterName string, originalSize, compressedSize int) {
	statsMutex.Lock()
	defer statsMutex.Unlock()

	globalStats.TotalCompressions++
	saved := originalSize - compressedSize
	if saved > 0 {
		globalStats.TotalSavedTokens += saved
	}
	globalStats.FilterUsage[filterName]++

	item := RecentItem{
		Filter:         filterName,
		SavedTokens:    saved,
		OriginalSize:   originalSize,
		CompressedSize: compressedSize,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}
	globalStats.RecentCompressions = append(globalStats.RecentCompressions, item)
	if len(globalStats.RecentCompressions) > 10 {
		globalStats.RecentCompressions = globalStats.RecentCompressions[1:]
	}
}

// FormatFilterUsage 格式化过滤器使用统计
func FormatFilterUsage(stats *RTKStats) string {
	if len(stats.FilterUsage) == 0 {
		return "暂无压缩记录"
	}
	var lines []string
	for filter, count := range stats.FilterUsage {
		lines = append(lines, fmt.Sprintf("  %s: %d 次", filter, count))
	}
	return fmt.Sprintf("过滤器使用统计（共 %d 次压缩）:\n%s", stats.TotalCompressions, fmt.Sprintf("%v", lines))
}
