package price

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Source 表示模型定价数据来源。
type Source int

const (
	SourceManual Source = iota // 用户手动设置
	SourceSync                 // 从 models.dev 同步
)

// LLMPrice 记录单个模型的每百万 token 价格（美元）。
type LLMPrice struct {
	Input     float64 // 输入价格（$/1M tokens）
	Output    float64 // 输出价格（$/1M tokens）
	Source    Source
	UpdatedAt time.Time
}

const pricesURL = "https://models.dev/api.json"

var (
	mu        sync.RWMutex
	prices    = make(map[string]LLMPrice) // modelID -> price（modelID 小写）
	lastSync  time.Time
	syncError error
)

// SyncFromModelsDev 从 models.dev 拉取最新价格并更新内部缓存。
// 失败时不抛错，更新 lastSync/syncError 供 UI 展示。
func SyncFromModelsDev(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pricesURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ai-router-gateway/price-sync")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		mu.Lock()
		syncError = fmt.Errorf("fetch failed: %w", err)
		lastSync = time.Now()
		mu.Unlock()
		return syncError
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		mu.Lock()
		syncError = fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		lastSync = time.Now()
		mu.Unlock()
		return syncError
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		mu.Lock()
		syncError = fmt.Errorf("read body: %w", err)
		lastSync = time.Now()
		mu.Unlock()
		return syncError
	}

	// models.dev API 格式：{developer: {models: {modelID: {cost: {input: $x/1M, output: $y/1M}}}}}
	var data map[string]struct {
		ID     string `json:"id"`
		Models map[string]struct {
			Cost struct {
				Input  *float64 `json:"input"`
				Output *float64 `json:"output"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		mu.Lock()
		syncError = fmt.Errorf("parse json: %w", err)
		lastSync = time.Now()
		mu.Unlock()
		return syncError
	}

	now := time.Now()
	mu.Lock()
	defer mu.Unlock()

	count := 0
	for developer, devData := range data {
		for modelID, entry := range devData.Models {
			key := strings.ToLower(strings.TrimSpace(modelID))
			if key == "" {
				continue
			}
			// 用 developer/modelID 作为完整 key，避免不同开发者同名模型的冲突
			fullKey := strings.ToLower(strings.TrimSpace(developer) + "/" + key)
			var inp, out float64
			if entry.Cost.Input != nil {
				inp = *entry.Cost.Input
			}
			if entry.Cost.Output != nil {
				out = *entry.Cost.Output
			}
			// 只更新有有效价格的模型（输入或输出 > 0）
			if inp > 0 || out > 0 {
				prices[fullKey] = LLMPrice{
					Input:     inp,
					Output:    out,
					Source:    SourceSync,
					UpdatedAt: now,
				}
				count++
			}
		}
	}
	lastSync = now
	syncError = nil
	return nil
}

// Get 查询指定模型的定价（精确匹配，modelID 小写）。
func Get(modelID string) *LLMPrice {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := prices[strings.ToLower(strings.TrimSpace(modelID))]
	if !ok {
		return nil
	}
	return &p
}

// EstimateOutputCost 根据模型 ID 和输出 token 数估算成本（美元）。
// 找不到价格时返回 0（由调用方 fallback 到 $2/1M 占位费率）。
func EstimateOutputCost(modelID string, completionTokens int) float64 {
	p := Get(modelID)
	if p == nil || p.Output <= 0 {
		return 0
	}
	return float64(completionTokens) / 1e6 * p.Output
}

// EstimateInputCost 根据模型 ID 和输入 token 数估算成本（美元）。
func EstimateInputCost(modelID string, promptTokens int) float64 {
	p := Get(modelID)
	if p == nil || p.Input <= 0 {
		return 0
	}
	return float64(promptTokens) / 1e6 * p.Input
}

// LastSyncTime 返回最近一次同步时间。
func LastSyncTime() time.Time {
	mu.RLock()
	defer mu.RUnlock()
	return lastSync
}

// LastSyncError 返回最近一次同步错误（nil 表示成功）。
func LastSyncError() error {
	mu.RLock()
	defer mu.RUnlock()
	return syncError
}

// Count 返回已缓存的价格条目数。
func Count() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(prices)
}
