// Package oauth 提供 OAuth 2.0 PKCE 自动刷新能力。
// 支持 Codex、Cursor、Claude Code、Kimi、iflow、GitHub、GitLab、Copilot 等主流 AI 编程工具的 OAuth 登录。
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider 定义一个 OAuth 提供商的完整配置。
type Provider struct {
	Name           string        // 提供商名称，如 "codex"、"cursor"、"claude"
	ClientID       string        // OAuth client ID
	ClientSecret   string        // OAuth client secret
	AuthURL        string        // 授权端点 URL
	TokenURL       string        // Token 端点 URL
	Scopes         string        // 请求的 scope
	RedirectURL    string        // 回调 URL
	RefreshTimeout time.Duration // token 请求超时
}

// Config 是用户在管理面板中保存的 OAuth 配置（JSON 字符串）。
type Config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURL  string `json:"redirect_url"`
	AuthURL      string `json:"auth_url,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
}

// Session 是一次 OAuth 授权流程的中间状态（存储在内存 map 中，key = state）。
type Session struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"-"`
	Provider     string    `json:"provider"`
	AccountID    int64     `json:"account_id"`
	UserID       int64     `json:"user_id"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// TokenState 是账号当前的 OAuth token 状态。
type TokenState struct {
	AccessToken  string           `json:"access_token"`
	TokenType    string           `json:"token_type"`
	ExpiresAt    time.Time        `json:"expires_at"`
	ExpiresIn    int              `json:"expires_in"`
	RefreshToken string           `json:"refresh_token,omitempty"`
	Raw          map[string]any   `json:"-"`
}

// IsExpired 检查 token 是否已过期（含 5 分钟缓冲）。
func (t *TokenState) IsExpired() bool {
	if t == nil {
		return true
	}
	return time.Now().Add(5*time.Minute).After(t.ExpiresAt)
}

// IsExpiringSoon 检查 token 是否即将过期（含 1 分钟缓冲）。
func (t *TokenState) IsExpiringSoon() bool {
	if t == nil {
		return true
	}
	return time.Now().Add(1*time.Minute).After(t.ExpiresAt)
}

var (
	ErrNoOAuthConfig   = fmt.Errorf("account has no oauth_config")
	ErrNoAccessToken   = fmt.Errorf("no access token available")
	ErrTokenExpired    = fmt.Errorf("token expired and refresh failed")
	ErrOAuthCode       = fmt.Errorf("missing or invalid authorization code")
	ErrOAuthState      = fmt.Errorf("oauth state mismatch")
)

// ---------- helpers ----------

func generateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ---------- public API ----------

// BuildAuthorizationURL 构建 OAuth 授权 URL（PKCE 流程）。
func BuildAuthorizationURL(p *Provider, session *Session) (string, error) {
	if p == nil || session == nil {
		return "", fmt.Errorf("provider and session are required")
	}
	u, err := url.Parse(p.AuthURL)
	if err != nil {
		return "", fmt.Errorf("invalid auth URL: %w", err)
	}
	params := u.Query()
	params.Set("response_type", "code")
	params.Set("client_id", p.ClientID)
	params.Set("redirect_uri", p.RedirectURL)
	params.Set("state", session.State)
	params.Set("code_challenge", generateCodeChallenge(session.CodeVerifier))
	params.Set("code_challenge_method", "S256")
	if p.Scopes != "" {
		params.Set("scope", p.Scopes)
	}
	u.RawQuery = params.Encode()
	return u.String(), nil
}

// ExchangeCode 用 authorization code 换取 access token。
func ExchangeCode(p *Provider, code, codeVerifier string) (*TokenState, error) {
	if code == "" || codeVerifier == "" {
		return nil, ErrOAuthCode
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.RedirectURL)
	form.Set("client_id", p.ClientID)
	form.Set("client_secret", p.ClientSecret)
	form.Set("code_verifier", codeVerifier)

	client := &http.Client{Timeout: p.RefreshTimeout}
	resp, err := client.Post(p.TokenURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: HTTP %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	return parseTokenResult(result), nil
}

// RefreshAccessToken 用 refresh_token 刷新 access_token。
func RefreshAccessToken(p *Provider, refreshToken string) (*TokenState, error) {
	if refreshToken == "" {
		return nil, ErrNoAccessToken
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("redirect_uri", p.RedirectURL)
	form.Set("client_id", p.ClientID)
	form.Set("client_secret", p.ClientSecret)

	client := &http.Client{Timeout: p.RefreshTimeout}
	resp, err := client.Post(p.TokenURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: HTTP %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}
	return parseTokenResult(result), nil
}

func parseTokenResult(result map[string]any) *TokenState {
	token := &TokenState{Raw: result}
	if v, ok := result["access_token"].(string); ok {
		token.AccessToken = v
	}
	if v, ok := result["token_type"].(string); ok {
		token.TokenType = v
	}
	if v, ok := result["refresh_token"].(string); ok {
		token.RefreshToken = v
	}
	if v, ok := result["expires_in"].(float64); ok {
		token.ExpiresIn = int(v)
		token.ExpiresAt = time.Now().Add(time.Duration(v) * time.Second)
	}
	return token
}

// GetAccessToken 从 OAuth config JSON 中提取 access_token。
func GetAccessToken(oauthConfigJSON, apiKey string) (string, error) {
	if oauthConfigJSON != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(oauthConfigJSON), &raw); err == nil {
			if v, ok := raw["access_token"].(string); ok && v != "" {
				return v, nil
			}
		}
		var str string
		if err := json.Unmarshal([]byte(oauthConfigJSON), &str); err == nil && str != "" {
			return str, nil
		}
	}
	if apiKey != "" {
		return apiKey, nil
	}
	return "", ErrNoAccessToken
}

// GetRefreshToken 从 OAuth config JSON 中提取 refresh_token。
func GetRefreshToken(oauthConfigJSON string) string {
	if oauthConfigJSON == "" {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(oauthConfigJSON), &raw); err != nil {
		return ""
	}
	if v, ok := raw["refresh_token"].(string); ok {
		return v
	}
	return ""
}
