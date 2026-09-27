package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// 多模态能力探测。
//
// 与 testModelConnectivity（只验证「能否调通」）不同，探测的目的是回答
// 「这个模型到底能吃什么输入」。做法是向上游发一条真实的多模态请求，
// 用上游的接受 / 拒绝来判定能力，比按模型名猜关键词可靠得多。
//
// 判定结果写入 models.probed_caps（JSON，形如 {"vision":true}）：
//   - true  ：上游正常返回，确认支持
//   - false ：上游明确以「不支持该输入类型」为由拒绝
//   - 键缺失：探测未得出结论（限流 / 鉴权失败 / 超时等），保持未知，
//     路由时回退到模型名关键词推断
//
// 探测请求刻意做得极小（16×16 图 + max_tokens=1），把额度消耗压到最低。

// probeImageDataURI 是一张 16×16 的棋盘格 PNG（89 字节）。
// 尺寸取 16×16 而非 1×1：部分服务商会拒收过小的图片，同时又要远低于
// 各家常见的 2048×2048 上限，避免触发「input size exceed limit」。
const probeImageDataURI = "data:image/png;base64," +
	"iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAIElEQVR42mM4gQRskAAucYZBqIEYRcjig1HDaDwMCg0AVguGEIsoZrsAAAAASUVORK5CYII="

// probeTimeout 单次探测的超时。取值大于普通请求：视觉模型首帧通常更慢，
// serverless 冷启动也需要余量。
const probeTimeout = 45 * time.Second

// ProbeResult 是一次能力探测的结论。
type ProbeResult struct {
	// Supported 为 nil 表示未得出结论（限流、鉴权失败、网络异常等），
	// 非 nil 时才写入数据库。
	Supported *bool
	// Message 为判定依据，直接展示给用户便于排查。
	Message string
}

func boolPtr(b bool) *bool { return &b }

// visionUnsupportedMarkers 上游「明确不支持图片输入」的特征串（小写匹配）。
// 命中即判定 vision=false；未命中的其它错误一律视为未知，避免误伤。
var visionUnsupportedMarkers = []string{
	// 通用：内容格式被拒
	"content must be a string", "content should be a string",
	"invalid type for 'messages", "invalid_type", "expected a string",
	"must be of type string", "is not of type 'string'",
	// 明确点名图片 / 视觉 / 多模态
	"does not support image", "not support image", "image input is not supported",
	"image_url is not supported", "unsupported content type", "unsupported message content",
	"does not support vision", "vision is not supported", "not a vision model",
	"multimodal is not supported", "does not support multimodal", "modality",
	"only text is supported", "text-only", "text only model",
	// 中文错误
	"不支持图片", "不支持图像", "不支持多模态", "仅支持文本", "只支持文本",
}

// visionInconclusiveMarkers 与图片能力无关的失败原因，出现即判定为「未得出结论」。
var visionInconclusiveMarkers = []string{
	"rate limit", "too many requests", "quota", "insufficient",
	"balance", "unauthorized", "invalid api key", "authentication",
	"permission", "forbidden", "余额", "限流", "频率", "鉴权", "未授权",
}

// probeVision 向上游发送一条「文字 + 小图」的多模态请求，判定模型是否支持图片输入。
func (h *ProviderHandler) probeVision(baseURL, apiKey, modelID string) ProbeResult {
	chatURL := resolveChatURL(baseURL)

	payload := map[string]any{
		"model": modelID,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "ok"},
				{"type": "image_url", "image_url": map[string]any{"url": probeImageDataURI}},
			},
		}},
		"max_tokens": 1,
		"stream":     false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProbeResult{Message: "构造探测请求失败：" + err.Error()}
	}

	req, err := http.NewRequest(http.MethodPost, chatURL, strings.NewReader(string(body)))
	if err != nil {
		return ProbeResult{Message: "构造探测请求失败：" + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return ProbeResult{Message: "探测超时，未得出结论"}
		}
		return ProbeResult{Message: "无法连接：" + err.Error()}
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	bodyStr := string(raw)
	if len(bodyStr) > 400 {
		bodyStr = bodyStr[:400]
	}
	lower := strings.ToLower(bodyStr)

	// 2xx：上游接受了图片
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 少数网关用 200 包裹错误体，这里再核一次
		if strings.Contains(lower, `"error"`) && containsAnyMarker(lower, visionUnsupportedMarkers) {
			return ProbeResult{Supported: boolPtr(false), Message: "上游以不支持图片为由拒绝：" + bodyStr}
		}
		return ProbeResult{Supported: boolPtr(true), Message: "已接受图片输入"}
	}

	// 与图片能力无关的失败（限流 / 鉴权 / 欠费）→ 不下结论
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 ||
		containsAnyMarker(lower, visionInconclusiveMarkers) {
		return ProbeResult{Message: fmt.Sprintf("HTTP %d，与图片能力无关，未得出结论：%s", resp.StatusCode, bodyStr)}
	}

	// 400/422 等参数类错误：明确点名图片时判定不支持
	if containsAnyMarker(lower, visionUnsupportedMarkers) {
		return ProbeResult{Supported: boolPtr(false), Message: fmt.Sprintf("HTTP %d，明确不支持图片：%s", resp.StatusCode, bodyStr)}
	}

	// 其余 4xx：多数纯文本模型收到数组型 content 会直接报参数错误，
	// 判为不支持并把原始报错带出去，用户可据此人工复核。
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return ProbeResult{Supported: boolPtr(false), Message: fmt.Sprintf("HTTP %d，上游拒绝了图片请求：%s", resp.StatusCode, bodyStr)}
	}

	return ProbeResult{Message: fmt.Sprintf("HTTP %d：%s", resp.StatusCode, bodyStr)}
}

func containsAnyMarker(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// mergeProbedCaps 把本次探测结论合并进历史结果（历史值中未涉及的键保持不变）。
func mergeProbedCaps(existing string, updates map[string]*bool) (string, bool) {
	caps := map[string]bool{}
	if strings.TrimSpace(existing) != "" {
		_ = json.Unmarshal([]byte(existing), &caps)
	}
	changed := false
	for k, v := range updates {
		if v == nil {
			continue // 未得出结论，不覆盖已有值
		}
		if old, ok := caps[k]; !ok || old != *v {
			changed = true
		}
		caps[k] = *v
	}
	if len(caps) == 0 {
		return "", changed
	}
	out, err := json.Marshal(caps)
	if err != nil {
		return existing, false
	}
	return string(out), changed
}
