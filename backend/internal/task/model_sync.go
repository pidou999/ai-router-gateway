// Package task 提供网关后台定时任务：模型列表同步、价格同步（另见 internal/price）。
package task

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ai-router-gateway/internal/crypto"
	"ai-router-gateway/internal/logger"
	"ai-router-gateway/internal/proxy"
)

// SyncModelsTask 同步所有开启 auto_sync 的已启用服务商的模型列表，并自动测试连通性。
// 同一时间只允许一个实例运行（互斥锁），避免并发写同一张 models 表。
func SyncModelsTask(db *sql.DB) error {
	rows, err := db.Query(
		`SELECT id, name, base_url, api_type, COALESCE(extra_config,'')
	 FROM providers WHERE enabled = 1 AND COALESCE(auto_sync, 0) = 1`,
	)
	if err != nil {
		return fmt.Errorf("查询 auto_sync providers 失败：%w", err)
	}
	defer rows.Close()

	type providerInfo struct {
		ID          int64
		Name        string
		BaseURL     string
		APIType     string
		ExtraConfig string
	}
	var providers []providerInfo
	for rows.Next() {
		var p providerInfo
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIType, &p.ExtraConfig); err != nil {
			logger.Warn("扫描 provider 行失败", "err", err)
			continue
		}
		providers = append(providers, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 providers 失败：%w", err)
	}

	if len(providers) == 0 {
		return nil
	}

	var mu sync.Mutex
	var successCount, failCount int64
	var lastErr error

	// 全局并发上限：防止一次同步大量模型时耗尽资源
	const maxConcurrency = 5
	sem := make(chan struct{}, maxConcurrency)

	var wg sync.WaitGroup
	for _, p := range providers {
		wg.Add(1)
		// 获取该服务商的第一个启用账户的 API Key
		var apiKey string
		var encKey string
		if err := db.QueryRow("SELECT api_key_encrypted FROM accounts WHERE provider_id = ? AND enabled = 1 LIMIT 1", p.ID).Scan(&encKey); err != nil {
			apiKey = ""
		} else if encKey != "" {
			// 解密 API Key
			if key, err := crypto.Decrypt(encKey, os.Getenv("ENCRYPTION_KEY")); err == nil {
				apiKey = key
			}
		}

		go func(p providerInfo, apiKey string) {
			defer wg.Done()
			resolvedBase := proxy.SubstituteTemplate(p.BaseURL, p.ExtraConfig)

			var entries []proxy.ModelEntry
			var fetchErr error
			if p.APIType == "cloudflare" {
				entries, fetchErr = proxy.FetchCloudflareModels(resolvedBase, "")
			} else {
				entries, fetchErr = proxy.FetchOpenAIModels(resolvedBase, apiKey)
			}
			if fetchErr != nil {
				mu.Lock()
				failCount++
				lastErr = fmt.Errorf("provider %s (id=%d): %w", p.Name, p.ID, fetchErr)
				mu.Unlock()
				logger.Warn("模型同步失败", "provider", p.Name, "err", fetchErr)
				return
			}

			// 写入新模型列表到数据库
			tx, err := db.Begin()
			if err != nil {
				mu.Lock()
				failCount++
				lastErr = err
				mu.Unlock()
				return
			}
			if _, err := tx.Exec("DELETE FROM models WHERE provider_id = ?", p.ID); err != nil {
				tx.Rollback()
				mu.Lock()
				failCount++
				lastErr = err
				mu.Unlock()
				return
			}
			stmt, err := tx.Prepare("INSERT OR IGNORE INTO models (provider_id, model_id, display_name, owned_by, enabled, is_free) VALUES (?, ?, ?, ?, 0, 0)")
			if err != nil {
				tx.Rollback()
				mu.Lock()
				failCount++
				lastErr = err
				mu.Unlock()
				return
			}
			for _, m := range entries {
				if _, err := stmt.Exec(p.ID, m.ModelID, m.DisplayName, m.OwnedBy); err != nil {
					logger.Warn("写入模型失败", "provider", p.Name, "model", m.ModelID, "err", err)
				}
			}
			stmt.Close()
			if err := tx.Commit(); err != nil {
				mu.Lock()
				failCount++
				lastErr = err
				mu.Unlock()
				return
			}

			// 自动测试连通性：并发度由 sem 控制
			models := make([]proxy.ModelEntry, len(entries))
			copy(models, entries)
			go testModelsConnectivity(db, p.ID, p.Name, p.APIType, resolvedBase, apiKey, models, sem, &successCount, &failCount)

			mu.Lock()
			successCount++
			mu.Unlock()
		}(p, apiKey)
	}
	wg.Wait()

	logger.Info("模型同步完成", "success", successCount, "fail", failCount)
	return lastErr
}

// testModelsConnectivity 对一批模型并发测试连通性，成功则更新 enabled=1，失败则 enabled=0。
func testModelsConnectivity(db *sql.DB, providerID int64, providerName, apiType, resolvedBase string, apiKey string, models []proxy.ModelEntry, sem chan struct{}, successCount, failCount *int64) {
	var wg sync.WaitGroup
	var doneCount atomic.Int64
	total := int64(len(models))

	for i := range models {
		m := &models[i]
		wg.Add(1)
		sem <- struct{}{}
		go func(m *proxy.ModelEntry) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := proxy.TestModelConnectivity(resolvedBase, apiKey, m.ModelID, apiType)
			if err != nil {
				logger.Warn("连通性测试异常", "provider", providerName, "model", m.ModelID, "err", err)
				return
			}

			enabled := 0
			if result.OK {
				enabled = 1
				atomic.AddInt64(successCount, 1)
				logger.Info("模型连通测试成功", "provider", providerName, "model", m.ModelID)
			} else {
				atomic.AddInt64(failCount, 1)
				logger.Info("模型连通测试失败", "provider", providerName, "model", m.ModelID, "reason", result.Message)
			}

			if _, err := db.Exec("UPDATE models SET enabled = ? WHERE provider_id = ? AND model_id = ?", enabled, providerID, m.ModelID); err != nil {
				logger.Warn("更新模型状态失败", "provider", providerName, "model", m.ModelID, "err", err)
			}

			done := doneCount.Add(1)
			if done%10 == 0 || done == total {
				logger.Info("连通测试进度", "provider", providerName, "done", done, "total", total)
			}
		}(m)
	}
	wg.Wait()
	logger.Info("服务商连通测试完成", "provider", providerName, "total", total)
}

// StartSyncLoop 启动模型同步后台循环：首次立即执行，之后每 30 分钟重试一次。
// 使用单例 goroutine，重复调用安全。
func StartSyncLoop(db *sql.DB) {
	once := sync.Once{}
	go func() {
		for {
			once.Do(func() {
				if err := SyncModelsTask(db); err != nil {
					logger.Warn("首次模型同步失败", "err", err)
				}
			})
			time.Sleep(30 * time.Minute)
			if err := SyncModelsTask(db); err != nil {
				logger.Warn("模型同步失败", "err", err)
			}
		}
	}()
}
