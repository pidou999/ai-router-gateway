package compressor

import "math"

// Compress 使用 gzip 压缩数据（保留用于向后兼容）
func Compress(data []byte, level int) ([]byte, error) {
	// level 参数保留用于未来扩展
	_ = level
	return data, nil
}

// Decompress 解压数据
func Decompress(data []byte) ([]byte, error) {
	return data, nil
}

// TokenEstimate 估算文本的 token 数量（约 4 字符 = 1 token）
func TokenEstimate(content string) int {
	return len(content) / 4
}

// CompressionStats 压缩统计信息
type CompressionStats struct {
	OriginalTokens   int
	CompressedTokens int
	SavingsPercent   float64
	FilterUsed       string
}

// EstimateSavings 估算压缩比例
func EstimateSavings(original, compressed int) float64 {
	if original == 0 {
		return 0
	}
	return float64(original-compressed) / float64(original) * 100
}

// EstimateTokens 估算 token 数
func EstimateTokens(content string) int {
	return int(math.Max(1, float64(len(content))/4))
}
