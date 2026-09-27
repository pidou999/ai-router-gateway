package router

import (
	"errors"
	"time"

	"ai-router-gateway/internal/models"
)

// roundRobinSelect 轮询选择一个可用账户。
// skipIf 允许调用方额外屏蔽某些账户（如账号×模型粒度的熔断）。
// 采用 fail-open：若所有账户都被冷却/屏蔽，仍退而求其次返回第一个被跳过的账户，
// 优先保证可用性（对应 9Router 的 fail-open 被动熔断），而不是整体拒绝请求。
func roundRobinSelect(providerID int64, accounts []models.Account, counter *int, skipIf func(models.Account) bool) (models.Account, error) {
	now := time.Now()
	start := *counter

	var firstSkipped models.Account
	hasSkipped := false

	for i := 0; i < len(accounts); i++ {
		*counter++
		idx := *counter % len(accounts)
		acct := accounts[idx]

		if acct.CooldownUntil != nil && acct.CooldownUntil.After(now) {
			if !hasSkipped {
				firstSkipped = acct
				hasSkipped = true
			}
			continue
		}
		if skipIf != nil && skipIf(acct) {
			if !hasSkipped {
				firstSkipped = acct
				hasSkipped = true
			}
			continue
		}

		return acct, nil
	}

	// 全部冷却中 → fail-open：放行第一个被跳过的账户
	if hasSkipped {
		return firstSkipped, nil
	}

	*counter = start
	return models.Account{}, errors.New("所有账户均处于冷却中")
}

func stickySelect(sessionID string, providerID int64, accounts []models.Account, sessions map[string]*StickySession) (models.Account, error) {
	now := time.Now()

	if s, ok := sessions[sessionID]; ok {
		if s.ExpiresAt.After(now) {
			for _, acct := range accounts {
				if acct.ID == s.AccountID && acct.Enabled {
					if acct.CooldownUntil == nil || !acct.CooldownUntil.After(now) {
						s.LastUsed = now
						return acct, nil
					}
					break
				}
			}
		}
		delete(sessions, sessionID)
	}

	var counter int
	acct, err := roundRobinSelect(providerID, accounts, &counter, nil)
	if err != nil {
		return models.Account{}, err
	}

	sessions[sessionID] = &StickySession{
		ProviderID: providerID,
		AccountID:  acct.ID,
		LastUsed:   now,
		ExpiresAt:  now.Add(30 * time.Minute),
	}

	return acct, nil
}

func cleanupExpiredSessions(sessions map[string]*StickySession) {
	now := time.Now()
	for id, s := range sessions {
		if s.ExpiresAt.Before(now) {
			delete(sessions, id)
		}
	}
}
