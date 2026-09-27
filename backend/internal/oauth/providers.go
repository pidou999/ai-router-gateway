// Package oauth 提供 OAuth 2.0 提供商注册表与默认配置。
package oauth

import "sync"

// DefaultProviders 返回常见 AI 编程工具的 OAuth 默认配置。
// 注意：client_id/client_secret 需要在各提供商开发者后台注册后填入。
// 以下为占位值，生产环境必须替换为真实凭证。
func DefaultProviders() map[string]*Provider {
	return map[string]*Provider{
		"codex": {
			Name:         "codex",
			ClientID:     "", // TODO: fill from env or admin config
			ClientSecret: "",
			AuthURL:      "https://openai.com/oauth/authorize",
			TokenURL:     "https://openai.com/oauth/token",
			Scopes:       "read write",
			RedirectURL:  "http://localhost:8080/api/oauth/codex/callback",
			RefreshTimeout: 30 * 10800000000,
		},
		"cursor": {
			Name:         "cursor",
			ClientID:     "",
			ClientSecret: "",
			AuthURL:      "https://auth.cursor.sh/authorize",
			TokenURL:     "https://auth.cursor.sh/token",
			Scopes:       "read write",
			RedirectURL:  "http://localhost:8080/api/oauth/cursor/callback",
			RefreshTimeout: 30 * 10800000000,
		},
		"claude": {
			Name:         "claude",
			ClientID:     "",
			ClientSecret: "",
			AuthURL:      "https://auth.anthropic.com/oauth/authorize",
			TokenURL:     "https://auth.anthropic.com/oauth/token",
			Scopes:       "read write",
			RedirectURL:  "http://localhost:8080/api/oauth/claude/callback",
			RefreshTimeout: 30 * 10800000000,
		},
		"github": {
			Name:         "github",
			ClientID:     "",
			ClientSecret: "",
			AuthURL:      "https://github.com/login/oauth/authorize",
			TokenURL:     "https://github.com/login/oauth/access_token",
			Scopes:       "read:user repo",
			RedirectURL:  "http://localhost:8080/api/oauth/github/callback",
			RefreshTimeout: 30 * 10800000000,
		},
		"kimi": {
			Name:         "kimi",
			ClientID:     "",
			ClientSecret: "",
			AuthURL:      "https://platform.moonshot.cn/oauth/authorize",
			TokenURL:     "https://platform.moonshot.cn/oauth/token",
			Scopes:       "profile openid",
			RedirectURL:  "http://localhost:8080/api/oauth/kimi/callback",
			RefreshTimeout: 30 * 10800000000,
		},
		"iflow": {
			Name:         "iflow",
			ClientID:     "",
			ClientSecret: "",
			AuthURL:      "https://auth.iflow.cn/authorize",
			TokenURL:     "https://auth.iflow.cn/token",
			Scopes:       "read write",
			RedirectURL:  "http://localhost:8080/api/oauth/iflow/callback",
			RefreshTimeout: 30 * 10800000000,
		},
	}
}

// providersLock 保护 providers map（初始化后只读，无需写锁）。
var providersLock sync.RWMutex
var providers map[string]*Provider

// InitProviders 初始化默认提供商注册表（可在 main 中调用，传入自定义 client_id/secret）。
func InitProviders(custom map[string]*Provider) {
	providersLock.Lock()
	defer providersLock.Unlock()
	if custom != nil {
		providers = custom
	} else {
		providers = DefaultProviders()
	}
}

// GetProvider 根据名称获取 OAuth 提供商配置。
func GetProvider(name string) *Provider {
	providersLock.RLock()
	defer providersLock.RUnlock()
	return providers[name]
}

// ListProviders 列出所有已注册的 OAuth 提供商名称。
func ListProviders() []string {
	providersLock.RLock()
	defer providersLock.RUnlock()
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	return names
}
