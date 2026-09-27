package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ProxyClient struct {
	client  *http.Client
	timeout time.Duration
}

func NewProxyClient(timeout time.Duration) *ProxyClient {
	return &ProxyClient{
		client: &http.Client{
			Timeout: timeout,
			// 使用系统环境变量中的代理（http_proxy/https_proxy），
			// 让请求可以通过用户配置的代理访问外部 API
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   50,
				MaxConnsPerHost:       50,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
			},
		},
		timeout: timeout,
	}
}

func (p *ProxyClient) ProxyRequest(ctx context.Context, method, url string, headers map[string]string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return p.client.Do(req)
}

func (p *ProxyClient) ProxyStreamRequest(ctx context.Context, method, url string, headers map[string]string, body io.Reader) (*http.Response, error) {
	return p.ProxyRequest(ctx, method, url, headers, body)
}

func SetAPIKey(headers map[string]string, apiKey string) {
	headers["Authorization"] = "Bearer " + apiKey
}

// SubstituteTemplate 把 base URL 模板中的命名占位符（如 {accountId}）替换为 extraConfig
// （JSON 字符串，如 {"accountId":"abc"}）里对应的值。对应 9Router 在 buildUrl 中对
// {accountId} 的处理：Cloudflare 等「需要 ID + Key」的服务商，其 base_url 含占位符，
// 实际值随每条凭证（账户）不同而不同。
// 若 extraConfig 为空或非合法 JSON，则原样返回 baseURL（不做替换）。
func SubstituteTemplate(baseURL, extraConfig string) string {
	baseURL = strings.TrimSpace(baseURL)
	extraConfig = strings.TrimSpace(extraConfig)
	if extraConfig == "" || !strings.Contains(baseURL, "{") {
		return baseURL
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(extraConfig), &m); err != nil || len(m) == 0 {
		return baseURL
	}
	var sb strings.Builder
	sb.Grow(len(baseURL))
	i := 0
	for i < len(baseURL) {
		if baseURL[i] == '{' {
			end := strings.IndexByte(baseURL[i:], '}')
			if end < 0 {
				sb.WriteString(baseURL[i:])
				break
			}
			key := baseURL[i+1 : i+end]
			if val, ok := m[key]; ok {
				// 仅替换字符串/数字/布尔等基本类型
				switch v := val.(type) {
				case string:
					sb.WriteString(v)
				case float64:
					sb.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
				case bool:
					sb.WriteString(strconv.FormatBool(v))
				default:
					// 复杂类型不替换，保留原占位符
					sb.WriteString(baseURL[i : i+end+1])
				}
			} else {
				// 未知占位符原样保留
				sb.WriteString(baseURL[i : i+end+1])
			}
			i += end + 1
			continue
		}
		sb.WriteString(string(baseURL[i]))
		i++
	}
	return sb.String()
}
