package router

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-router-gateway/internal/compressor"
	"ai-router-gateway/internal/crypto"
	"ai-router-gateway/internal/db"
	"ai-router-gateway/internal/logger"
	"ai-router-gateway/internal/models"
	"ai-router-gateway/internal/modes"
	"ai-router-gateway/internal/oauth"
	"ai-router-gateway/internal/price"
	"ai-router-gateway/internal/proxy"
	"ai-router-gateway/internal/trace"
	"ai-router-gateway/internal/translator"
)

type StickySession struct {
	ProviderID int64
	AccountID  int64
	LastUsed   time.Time
	ExpiresAt  time.Time
}

// 自动路由（auto）策略下，精准路由按"选中的候选模型 → 各自服务商"的顺序尝试。
// 组合模型较多时（如几十到上百个），不应穷举所有条目——按 maxAutoTargets 上限控制，
// 并在 timeout 到达时提前终止，避免在客户端超时之前把整个组合跑完。
const maxAutoTargets = 20

// 通用兜底（fallback）策略可穷举整个组合，因为 fallback 本就是顺序尝试。

type RouteSettings struct {
	CavemanEnabled  bool
	CavemanLevel    int
	PonytailEnabled bool
	PonytailLevel   string
	HeadroomEnabled bool
	HeadroomLevel   int
	RTKEnabled      bool
	RTKLevel        int
	StreamEnabled   bool
	TimeoutSeconds  int
	ErrorShield     bool // 错误屏蔽：上游失败时不暴露原始错误，返回聚合友好提示
}

type RouteEngine struct {
	db                 *sql.DB
	proxy              *proxy.ProxyClient
	encryptionKey      string
	mu                 sync.Mutex
	roundRobinCounters map[int64]int
	stickySessions     map[string]*StickySession
	// breaker 账号×模型粒度的被动熔断器（对应 9Router 的 account×model circuit breaker）。
	breaker *CircuitBreaker
	// rateLimiter 按账户执行 RPM/TPM 限流（accounts 表的 rate_limit_rpm/tpm 此前从不生效）。
	rateLimiter *RateLimiter
	// refreshMutex 保护 token 刷新并发（防止同一账号多线程同时刷新）。
	refreshMutex sync.Map // accountID -> *sync.Mutex
}

func NewRouteEngine(db *sql.DB, proxyClient *proxy.ProxyClient, encryptionKey string, dialect db.Dialect) *RouteEngine {
	breaker := NewCircuitBreaker(db, dialect)
	breaker.Load()
	return &RouteEngine{
		db:                 db,
		proxy:              proxyClient,
		encryptionKey:      encryptionKey,
		roundRobinCounters: make(map[int64]int),
		stickySessions:     make(map[string]*StickySession),
		breaker:            breaker,
		rateLimiter:        NewRateLimiter(),
	}
}

// decryptAPIKey 解密账号 API Key（AES-GCM，兼容旧版 base64+XOR 格式）。
func (e *RouteEngine) decryptAPIKey(encrypted string) string {
	plain, err := crypto.Decrypt(encrypted, e.encryptionKey)
	if err != nil {
		return encrypted
	}
	return plain
}

// resolveAuthHeader 选择认证头值：优先用 OAuth access_token（自动刷新），
// 回退到明文 API Key。同时更新 accounts.token_expiry（如果 OAuth 刷新了 token）。
func (e *RouteEngine) resolveAuthHeader(acct *models.Account, providerType string) (headerName, headerValue string) {
	headerName = "Authorization"
	// 1. 尝试从 oauth_config 取 access_token
	if acct.OAuthConfig != "" {
		token, err := oauth.GetAccessToken(acct.OAuthConfig, "")
		if err == nil && token != "" {
			// 检查是否需要刷新
			if oauthConfigNeedsRefresh(acct) {
				e.tryRefreshToken(acct)
				// 刷新后重新读取
				if refreshedToken, refreshErr := oauth.GetAccessToken(acct.OAuthConfig, ""); refreshErr == nil && refreshedToken != "" {
					token = refreshedToken
				}
			}
			switch providerType {
			case "anthropic", "claude":
				return "x-api-key", token
			case "gemini":
				return "x-goog-api-key", token
			default:
				return "Authorization", "Bearer " + token
			}
		}
	}
	// 2. 回退到 api_key
	key := e.decryptAPIKey(acct.APIKeyEncrypted)
	if key != "" {
		switch providerType {
		case "anthropic", "claude":
			return "x-api-key", key
		case "gemini":
			return "x-goog-api-key", key
		default:
			return "Authorization", "Bearer " + key
		}
	}
	// 3. OpenCode 免费层：未绑定 key 时使用官方匿名凭据 "public"（参考 9Router executor）
	if providerType == "opencode" {
		return "Authorization", "Bearer public"
	}
	return "Authorization", ""
}

// oauthConfigNeedsRefresh 判断账号是否需要刷新 token。
func oauthConfigNeedsRefresh(acct *models.Account) bool {
	if acct.TokenExpiry == nil || acct.OAuthConfig == "" {
		return false
	}
	// 已过期或 5 分钟内即将过期 → 需要刷新
	return time.Until(*acct.TokenExpiry) < 5*time.Minute
}

// tryRefreshToken 异步尝试刷新 token，不阻塞请求流程。
func (e *RouteEngine) tryRefreshToken(acct *models.Account) {
	refresher, _ := e.refreshMutex.LoadOrStore(acct.ID, &sync.Mutex{})
	mu := refresher.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 双重检查：另一协程可能已经刷新了
	if !oauthConfigNeedsRefresh(acct) {
		return
	}
	go func() {
		refreshToken := oauth.GetRefreshToken(acct.OAuthConfig)
		if refreshToken == "" {
			return
		}
		provider := oauth.GetProvider("") // 默认用第一个已知 provider
		if provider == nil {
			return
		}
		// 用 refresh_token 换新的 access_token
		newToken, err := oauth.RefreshAccessToken(provider, refreshToken)
		if err != nil {
			logger.Error("OAuth token refresh failed", "account_id", acct.ID, "err", err)
			return
		}
		// 构造新的 oauth_config JSON，更新到数据库
		var raw map[string]any
		if err := json.Unmarshal([]byte(acct.OAuthConfig), &raw); err == nil {
			raw["access_token"] = newToken.AccessToken
			raw["refresh_token"] = newToken.RefreshToken
			raw["expires_at"] = newToken.ExpiresAt.Format(time.RFC3339)
		}
		newConfig, _ := json.Marshal(raw)
		_, err = e.db.Exec(
			"UPDATE accounts SET oauth_config = ?, token_expiry = ? WHERE id = ?",
			string(newConfig), newToken.ExpiresAt, acct.ID,
		)
		if err != nil {
			logger.Error("OAuth token expiry update failed", "account_id", acct.ID, "err", err)
		} else {
			logger.Info("OAuth token refreshed", "account_id", acct.ID)
		}
	}()
}

// compressToolResults 压缩消息历史中的 tool_result 消息，减少 token 消耗。
// 对应 9Router 的 tool_result compression：自动检测内容类型并应用相应过滤器。
func (e *RouteEngine) compressToolResults(messages *[]models.Message, rtkLevel int) {
	if messages == nil || len(*messages) == 0 {
		return
	}
	rtk := compressor.NewRTKCompressor()
	for i := range *messages {
		if (*messages)[i].Role == "tool" || (*messages)[i].Role == "tool_result" {
			compressed, _, _ := rtk.Compress((*messages)[i].Content.String(), "tool_result", rtkLevel)
			(*messages)[i].Content = models.MessageContent(compressed)
		}
	}
}

func (e *RouteEngine) Route(ctx context.Context, userID int64, req *models.ChatRequest, settings *RouteSettings) (*models.ChatResponse, error) {
	if tr := trace.FromContext(ctx); tr != nil {
		tr.StartSegment("route")
	}
	routedReq := *req

	if settings.CavemanEnabled {
		routedReq.Messages = modes.ApplyCaveman(routedReq.Messages, settings.CavemanLevel)
	}

	if settings.PonytailEnabled {
		routedReq.Messages = modes.ApplyPonytail(routedReq.Messages, settings.PonytailLevel)
	}

	if settings.HeadroomEnabled {
		routedReq.Messages = modes.ApplyHeadroom(routedReq.Messages, settings.HeadroomLevel)
	}

	// RTK 压缩独立启用，不受 Headroom 模式影响
	if settings.RTKEnabled {
		e.compressToolResults(&routedReq.Messages, settings.RTKLevel)
	}

	// 图片缩放：检测到图片尺寸超过 2048×2048 时自动缩小到 2048×2048 以内
	// 避免上游视觉模型因输入尺寸超限制而拒绝请求
	e.resizeImagesIfNeeded(&routedReq)

	// 组合解析：auto（按请求意图智能选模型）/ round_robin / fallback。
	// 返回的候选列表已按策略排序，天然支持"组合内逐个回退"。
	comboItems, comboStrategy, comboErr := e.resolveComboCandidates(routedReq.Model, userID, &routedReq)
	isCombo := comboErr == nil && len(comboItems) > 0
	if isCombo {
		routedReq.Model = comboItems[0].ID
		if tr := trace.FromContext(ctx); tr != nil {
			tr.SetDetail("route", map[string]any{
				"combo":          req.Model,
				"combo_strategy": comboStrategy,
				"intent":         intentsLabel(ClassifyIntents(req)),
				"picked_model":   comboItems[0].ID,
			})
		}
	}

	tiers, err := getTierConfigs(e.db, userID)
	if err != nil {
		return nil, fmt.Errorf("获取层级配置失败：%w", err)
	}

	// 1) 精准路由：组合按候选顺序逐个尝试；非组合请求从 models 表模糊查找所属服务商
	var targets []comboTarget
	if isCombo {
		targets = e.resolveTargets(comboItems)
	} else if pid, nm, lookupErr := resolveModelProvider(e.db, routedReq.Model); lookupErr == nil && pid > 0 {
		targets = []comboTarget{{ProviderID: pid, NativeModel: nm}}
	}
	// auto 策略只试前 N 个候选（组合模型较多时避免穷举）；fallback 策略穷举全部。
	maxTargets := maxAutoTargets
	if !isCombo || comboStrategy != "auto" {
		maxTargets = len(targets)
	}
	// timeout 安全网：全局请求超时（默认 120s）减去预留的 5s，作为组合内模型尝试的总时间预算
	timeoutSec := time.Duration(settings.TimeoutSeconds) * time.Second
	routeDeadline := time.Now().Add(timeoutSec - 5*time.Second)
	// auto/round_robin 策略下，组合候选已经确定了 provider 和 model，直接按 targets 列表顺序尝试
	// 不再通过 tier 过滤（tier 机制用于 fallback 策略的层级遍历，对精准路由场景不适用）
	// 组合内所有模型都属于同一服务商时，直接遍历 targets 即可
	for i, target := range targets {
		if i >= maxTargets {
			break
		}
		if time.Now().After(routeDeadline) {
			break
		}

		// 能力过滤：请求含图片时，跳过纯文本模型（无 vision 能力）
		// 避免 image_url 发到 deepseek/Qwen 等纯文本模型上直接报错
		reqHasImage := false
		for _, msg := range req.Messages {
			msgRaw := string(msg.Content)
			if len(msgRaw) > 0 && msgRaw[0] == '[' {
				msgLower := strings.ToLower(msgRaw)
				if strings.Contains(msgLower, `"image_url"`) || strings.Contains(msgLower, `"input_image"`) {
					reqHasImage = true
					break
				}
			}
		}
		if reqHasImage {
			modelCaps := InferCapabilities(target.NativeModel)
			if !hasCapability(modelCaps, CapVision) {
				continue // 该模型不支持图片，跳过
			}
		}
		// 直接用 combo 配置的 provider + model 尝试
		preciseReq := routedReq
		if target.NativeModel != "" {
			preciseReq.Model = target.NativeModel
		}
		// 从 tier 中匹配对应 provider，如果找不到则跳过（该 target 无效）
		matched := false
		for _, tier := range tiers {
			for _, pc := range tier.Providers {
				if pc.Provider.ID == target.ProviderID {
					matched = true
					if resp, directErr := e.tryProviders(ctx, userID, &preciseReq, settings, []ProviderConfig{pc}); directErr == nil {
						return resp, nil
					}
				}
			}
		}
		if !matched {
			// provider 不存在于任何层级，跳过该 target
			continue
		}
		// 该 target 失败，继续尝试下一个
	}

	// 2) 通用层级路由（按优先级遍历）—— 仅 fallback 策略需要兜底遍历
	if !isCombo || comboStrategy != "auto" {
		for _, tier := range tiers {
			resp, err := e.tryProviders(ctx, userID, &routedReq, settings, tier.Providers)
			if err == nil {
				return resp, nil
			}
		}
	}

	return nil, errors.New("所有层级均已耗尽，没有可用的提供商")
}

// resolveChatURL 在实际发起转发时补全目标端点。
// 服务商配置里很多用户只填了 base（如 https://xxx/api/v3），
// 而本网关把 BaseURL 当作完整 POST 目标直接转发，因此在这里兜底补全端点。
//
// 规则：
//   - gemini：在 base（.../models）后追加 /{model}:generateContent 或 :streamGenerateContent?alt=sse
//   - anthropic / claude：载荷已由 translator 转成 Anthropic Messages 格式，端点必须是 /messages，
//     只填 base（如 https://api.anthropic.com/v1）时补全为 .../v1/messages；
//     若误补 /chat/completions，真实 Anthropic 会直接 404。
//   - 已含 /chat/completions → 原样返回
//   - 已含 /messages（Anthropic 等，交由 translator 处理）→ 原样返回
//   - 含查询参数 ?（如 Azure 部署 URL）→ 原样返回
//   - 其余 → 去除尾部斜杠后追加 /chat/completions
func resolveChatURL(apiType, baseURL, model string, stream bool) string {
	if baseURL == "" {
		return baseURL
	}
	if apiType == "gemini" {
		base := strings.TrimRight(baseURL, "/")
		if stream {
			return base + "/" + model + ":streamGenerateContent?alt=sse"
		}
		return base + "/" + model + ":generateContent"
	}
	if strings.Contains(baseURL, "?") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/chat/completions") || strings.HasSuffix(baseURL, "/messages") {
		return baseURL
	}
	if apiType == "anthropic" || apiType == "claude" {
		return strings.TrimRight(baseURL, "/") + "/messages"
	}
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

// resizeImagesIfNeeded 检测请求消息中的图片，超过 2048×2048 时自动缩小。
// 图片以 base64 data URI 嵌入在 content 的 JSON 数组中。
func (e *RouteEngine) resizeImagesIfNeeded(req *models.ChatRequest) {
	const maxDim = 2048
	for i := range req.Messages {
		content := string(req.Messages[i].Content)
		if len(content) == 0 || content[0] != '[' {
			continue
		}
		var parts []json.RawMessage
		if err := json.Unmarshal([]byte(content), &parts); err != nil {
			continue
		}
		modified := false
		for j := range parts {
			var part map[string]json.RawMessage
			if err := json.Unmarshal(parts[j], &part); err != nil {
				continue
			}
			partType := ""
			if t, ok := part["type"]; ok {
				json.Unmarshal(t, &partType)
			}
			if partType != "image_url" && partType != "input_image" && partType != "image" {
				continue
			}
			var urlStr string
			if urlField, ok := part["image_url"]; ok {
				var urlObj map[string]json.RawMessage
				if err := json.Unmarshal(urlField, &urlObj); err == nil {
					if u, ok := urlObj["url"]; ok {
						json.Unmarshal(u, &urlStr)
					}
				}
			}
			if urlStr == "" {
				if dataField, ok := part["data"]; ok {
					json.Unmarshal(dataField, &urlStr)
				}
			}
			if !strings.HasPrefix(urlStr, "data:") {
				continue
			}
			encoded := strings.TrimPrefix(urlStr, "data:")
			encoded = encoded[strings.Index(encoded, ",")+1:]
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				continue
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				continue
			}
			bounds := img.Bounds()
			w, h := bounds.Dx(), bounds.Dy()
			if w <= maxDim && h <= maxDim {
				continue
			}
			ratio := float64(w)/float64(h)
			newW, newH := w, h
			if float64(w) > float64(maxDim) || float64(h) > float64(maxDim) {
				if w >= h {
					newW = maxDim
					newH = int(float64(maxDim) / ratio)
				} else {
					newH = maxDim
					newW = int(float64(maxDim) * ratio)
				}
			}
			// Bilinear resize
			offsetX, offsetY := img.(interface{ PixOffset(int, int) (int, int) }).PixOffset(0, 0)
			src := img.(interface{ Pix() []uint8; Stride() int })
			pix := src.Pix()
			stride := src.Stride()
			dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
			for y := 0; y < newH; y++ {
				for x := 0; x < newW; x++ {
					sx := float64(x) * float64(w) / float64(newW)
					sy := float64(y) * float64(h) / float64(newH)
					sx0 := int(sx)
					sy0 := int(sy)
					if sx0 >= w-1 {
						sx0 = w - 2
					}
					if sy0 >= h-1 {
						sy0 = h - 2
					}
					dx := sx - float64(sx0)
					dy := sy - float64(sy0)
					getPixel := func(px, py int) [4]uint8 {
						i := (py+offsetY)*stride + (px+offsetX)*4
						return [4]uint8{pix[i], pix[i+1], pix[i+2], pix[i+3]}
					}
					c00 := getPixel(sx0, sy0)
					c10 := getPixel(sx0+1, sy0)
					c01 := getPixel(sx0, sy0+1)
					c11 := getPixel(sx0+1, sy0+1)
					r := float64(c00[0])*(1-dx)*(1-dy) + float64(c10[0])*dx*(1-dy) + float64(c01[0])*(1-dx)*dy + float64(c11[0])*dx*dy
					g := float64(c00[1])*(1-dx)*(1-dy) + float64(c10[1])*dx*(1-dy) + float64(c01[1])*(1-dx)*dy + float64(c11[1])*dx*dy
					b := float64(c00[2])*(1-dx)*(1-dy) + float64(c10[2])*dx*(1-dy) + float64(c01[2])*(1-dy)*dy + float64(c11[2])*dx*dy
					a := float64(c00[3])*(1-dx)*(1-dy) + float64(c10[3])*dx*(1-dy) + float64(c01[3])*(1-dx)*dy + float64(c11[3])*dx*dy
					idx := y*dst.Stride + x*4
					dst.Pix[idx] = uint8(r)
					dst.Pix[idx+1] = uint8(g)
					dst.Pix[idx+2] = uint8(b)
					dst.Pix[idx+3] = uint8(a)
				}
			}
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
				continue
			}
			newDataURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
			urlStr = newDataURI
			if urlField, ok := part["image_url"]; ok {
				var urlObj map[string]json.RawMessage
				json.Unmarshal(urlField, &urlObj)
				urlObj["url"], _ = json.Marshal(newDataURI)
				part["image_url"], _ = json.Marshal(urlObj)
			}
			if _, ok := part["data"]; ok {
				part["data"], _ = json.Marshal(newDataURI)
			}
			parts[j], _ = json.Marshal(part)
			modified = true
		}
		if modified {
			newContent, _ := json.Marshal(parts)
			req.Messages[i].Content = models.MessageContent(newContent)
		}
	}
}

func (e *RouteEngine) tryProviders(ctx context.Context, userID int64, req *models.ChatRequest, settings *RouteSettings, providers []ProviderConfig) (*models.ChatResponse, error) {
	tr := trace.FromContext(ctx)
	if tr != nil {
		tr.RecordModel(req.Model)
	}
	for _, pc := range providers {
		// 免费服务商无需账户选择和 API Key
		var acct models.Account
		if pc.NoAuth {
			acct = models.Account{ID: 0} // 占位，日志用
		} else {
			e.mu.Lock()
			counter := e.roundRobinCounters[pc.Provider.ID]
			e.mu.Unlock()

			selectedAcct, err := roundRobinSelect(pc.Provider.ID, pc.Accounts, &counter,
				func(a models.Account) bool {
					return e.breaker.IsCooling(a.ID, req.Model) || e.breaker.IsAccountCooling(a.ID) ||
						e.rateLimiter.Exceeded(a.ID, a.RateLimitRPM, a.RateLimitTPM)
				})
			if err != nil {
				if tr != nil {
					tr.AddAttempt(trace.Attempt{
						ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name,
						Model: req.Model, APIType: pc.Provider.APIType, Status: "error", Error: "无可用账户",
					})
				}
				continue
			}

			e.mu.Lock()
			e.roundRobinCounters[pc.Provider.ID] = counter
			e.mu.Unlock()
			acct = selectedAcct
			e.rateLimiter.RecordRequest(acct.ID)

			// 账号轮换日志
			logger.Debug("账号轮询选中",
				"provider_id", pc.Provider.ID,
				"provider_name", pc.Provider.Name,
				"account_id", acct.ID,
				"model", req.Model,
				"counter", counter,
				"total_accounts", len(pc.Accounts),
			)
			}

		// route 段：记录本次选中的服务商/账户/模型（胜出尝试最终定稿）
		if tr != nil {
			tr.SetDetail("route", map[string]any{
				"provider_id":   pc.Provider.ID,
				"provider_name": pc.Provider.Name,
				"account_id":    acct.ID,
				"model":         req.Model,
				"api_type":      pc.Provider.APIType,
			})
		}

		transl := translator.GetTranslator(pc.Provider.APIType)

		var reqBody []byte
		var headers map[string]string

		// ---- translate（请求）----
		var translateReqMs int64
		var reqInBytes, reqOutBytes int
		if b, err := json.Marshal(req); err == nil {
			reqInBytes = len(b)
		}
		if transl != nil {
			tT := time.Now()
			translatedBody, hdrs, err := transl.TranslateRequest(req)
			translateReqMs = time.Since(tT).Milliseconds()
			if err != nil {
				if tr != nil {
					tr.AddAttempt(trace.Attempt{
						ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name, AccountID: acct.ID,
						Model: req.Model, APIType: pc.Provider.APIType, Status: "error", Error: "请求翻译失败: " + err.Error(),
					})
				}
				continue
			}
			headers = hdrs
			reqBody, err = json.Marshal(translatedBody)
			if err != nil {
				continue
			}
		} else {
			var err error
			reqBody, err = json.Marshal(req)
			if err != nil {
				continue
			}
		}
		reqOutBytes = len(reqBody)

		if headers == nil {
			headers = make(map[string]string)
		}
		if !pc.NoAuth {
			hdrName, hdrVal := e.resolveAuthHeader(&acct, pc.Provider.APIType)
			if hdrVal != "" {
				headers[hdrName] = hdrVal
			}
		}
		if pc.Provider.APIType == "opencode" {
			headers["x-opencode-client"] = "desktop"
		}
		headers["Content-Type"] = "application/json"

		startTime := time.Now()
		// Cloudflare 等「ID + Key」服务商：base_url 含 {accountId} 占位符，
		// 用当前账户的 extra_config 替换后再补全端点。
		resolvedBase := proxy.SubstituteTemplate(pc.Provider.BaseURL, acct.ExtraConfig)
		chatURL := resolveChatURL(pc.Provider.APIType, resolvedBase, req.Model, false)
		httpResp, err := e.proxy.ProxyRequest(ctx, http.MethodPost, chatURL, headers, bytes.NewReader(reqBody))
		if err != nil {
			upstreamMs := int(time.Since(startTime).Milliseconds())
			logRequest(e.db, ctx, userID, pc.Provider.ID, acct.ID, req.Model, 0, 0, upstreamMs, "error", err.Error())
			e.breaker.RecordFailure(acct.ID, req.Model, 0)
			if tr != nil {
				tr.AddAttempt(trace.Attempt{
					ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name, AccountID: acct.ID,
					Model: req.Model, APIType: pc.Provider.APIType, Status: "error", Error: err.Error(), LatencyMs: int64(upstreamMs),
				})
				tr.RecordSegment("upstream", "error", int64(upstreamMs), map[string]any{
					"provider_id": pc.Provider.ID, "account_id": acct.ID, "model": req.Model,
					"error": err.Error(),
				})
			}
			continue
		}

		respBytes, err := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if err != nil {
			upstreamMs := int(time.Since(startTime).Milliseconds())
			logRequest(e.db, ctx, userID, pc.Provider.ID, acct.ID, req.Model, 0, 0, upstreamMs, "error", err.Error())
			e.breaker.RecordFailure(acct.ID, req.Model, 0)
			if tr != nil {
				tr.AddAttempt(trace.Attempt{
					ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name, AccountID: acct.ID,
					Model: req.Model, APIType: pc.Provider.APIType, Status: "error", Error: err.Error(), LatencyMs: int64(upstreamMs),
				})
			}
			continue
		}

		if httpResp.StatusCode >= 400 {
			upstreamMs := int(time.Since(startTime).Milliseconds())
			logRequest(e.db, ctx, userID, pc.Provider.ID, acct.ID, req.Model, 0, 0, upstreamMs, "error", string(respBytes))
			e.breaker.RecordFailure(acct.ID, req.Model, httpResp.StatusCode)
			if tr != nil {
				tr.AddAttempt(trace.Attempt{
					ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name, AccountID: acct.ID,
					Model: req.Model, APIType: pc.Provider.APIType, StatusCode: httpResp.StatusCode, Status: "error",
					Error: string(respBytes), LatencyMs: int64(upstreamMs),
				})
			}
			continue
		}

		// ---- translate（响应）----
		var translateRespMs int64
		var respInBytes, respOutBytes int
		respInBytes = len(respBytes)
		var chatResp *models.ChatResponse
		if transl != nil {
			tR := time.Now()
			var rawResp any
			if err := json.Unmarshal(respBytes, &rawResp); err != nil {
				continue
			}
			chatResp, err = transl.TranslateResponse(rawResp)
			translateRespMs = time.Since(tR).Milliseconds()
			if err != nil {
				continue
			}
		} else {
			chatResp = &models.ChatResponse{}
			if err := json.Unmarshal(respBytes, chatResp); err != nil {
				continue
			}
		}
		if b, err := json.Marshal(chatResp); err == nil {
			respOutBytes = len(b)
		}

		if settings.RTKEnabled {
			finalBytes, err := json.Marshal(chatResp)
			if err == nil {
				compressed, err := compressor.Compress(finalBytes, settings.RTKLevel)
				if err == nil {
					_ = json.Unmarshal(compressed, chatResp)
				}
			}
		}

		upstreamMs := int(time.Since(startTime).Milliseconds())
		// 四段追踪定稿（胜出尝试）
		if tr != nil {
			tr.EndSegment("route", "ok", nil)
			tr.RecordSegment("translate", "ok", translateReqMs+translateRespMs, map[string]any{
				"api_type":       pc.Provider.APIType,
				"translated":     transl != nil,
				"model_in":       req.Model,
				"model_out":      req.Model,
				"req_in_bytes":   reqInBytes,
				"req_out_bytes":  reqOutBytes,
				"resp_in_bytes":  respInBytes,
				"resp_out_bytes": respOutBytes,
			})
			tr.RecordSegment("upstream", "ok", int64(upstreamMs), map[string]any{
				"provider_id":    pc.Provider.ID,
				"account_id":     acct.ID,
				"model":          req.Model,
				"status_code":    httpResp.StatusCode,
				"response_bytes": len(respBytes),
			})
		}

		logRequest(e.db, ctx, userID, pc.Provider.ID, acct.ID, req.Model,
			chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens,
			upstreamMs, "success", "")
		recordUsageStats(e.db, userID, pc.Provider.ID, req.Model, pc.PricingType,
			chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens)
		e.breaker.RecordSuccess(acct.ID, req.Model)
		e.rateLimiter.RecordTokens(acct.ID, chatResp.Usage.PromptTokens+chatResp.Usage.CompletionTokens)
		return chatResp, nil
	}

	// 当前层级所有服务商均失败：四段追踪以 error 定稿
	if tr != nil {
		tr.EndSegment("route", "error", map[string]any{"error": "所有层级均已耗尽，没有可用的提供商"})
		tr.RecordSegment("translate", "skip", 0, map[string]any{"reason": "无成功尝试"})
		tr.RecordSegment("upstream", "error", 0, map[string]any{"error": "所有服务商均失败"})
	}
	return nil, errors.New("当前层级没有可用的提供商")
}

func (e *RouteEngine) StreamRoute(ctx context.Context, userID int64, req *models.ChatRequest, settings *RouteSettings) (<-chan *models.StreamChunk, <-chan error) {
	if tr := trace.FromContext(ctx); tr != nil {
		tr.StartSegment("route")
	}
	chunkCh := make(chan *models.StreamChunk, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(chunkCh)
		defer close(errCh)

		routedReq := *req
		routedReq.Stream = true

		if settings.CavemanEnabled {
			routedReq.Messages = modes.ApplyCaveman(routedReq.Messages, settings.CavemanLevel)
		}

		if settings.PonytailEnabled {
			routedReq.Messages = modes.ApplyPonytail(routedReq.Messages, settings.PonytailLevel)
		}

		if settings.HeadroomEnabled {
			routedReq.Messages = modes.ApplyHeadroom(routedReq.Messages, settings.HeadroomLevel)
		}

		// RTK 压缩独立启用，不受 Headroom 模式影响
		if settings.RTKEnabled {
			e.compressToolResults(&routedReq.Messages, settings.RTKLevel)
		}

		// 组合解析：auto（按请求意图智能选模型）/ round_robin / fallback
		comboItems, comboStrategy, comboErr := e.resolveComboCandidates(routedReq.Model, userID, &routedReq)
		isCombo := comboErr == nil && len(comboItems) > 0
		if isCombo {
			routedReq.Model = comboItems[0].ID
			if tr := trace.FromContext(ctx); tr != nil {
				tr.SetDetail("route", map[string]any{
					"combo":          req.Model,
					"combo_strategy": comboStrategy,
					"intent":         intentsLabel(ClassifyIntents(req)),
					"picked_model":   comboItems[0].ID,
				})
			}
		}

		tiers, err := getTierConfigs(e.db, userID)
		if err != nil {
			errCh <- fmt.Errorf("get tier configs: %w", err)
			return
		}

		// 精准路由：组合按候选顺序逐个尝试；非组合请求从 models 表模糊查找所属服务商
		var targets []comboTarget
		if isCombo {
			targets = e.resolveTargets(comboItems)
		} else if pid, nm, lookupErr := resolveModelProvider(e.db, routedReq.Model); lookupErr == nil && pid > 0 {
			targets = []comboTarget{{ProviderID: pid, NativeModel: nm}}
		}
		// auto 策略只试前 N 个候选；fallback 策略穷举全部。
		maxT := maxAutoTargets
		if !isCombo || comboStrategy != "auto" {
			maxT = len(targets)
		}
		timeoutSec := time.Duration(settings.TimeoutSeconds) * time.Second
		streamDeadline := time.Now().Add(timeoutSec - 5*time.Second)
		for i, target := range targets {
			if i >= maxT {
				break
			}
			if time.Now().After(streamDeadline) {
				break
			}

			// 能力过滤：请求含图片时跳过纯文本模型
			streamHasImage := false
			for _, msg := range routedReq.Messages {
				msgRaw := string(msg.Content)
				if len(msgRaw) > 0 && msgRaw[0] == '[' {
					msgLower := strings.ToLower(msgRaw)
					if strings.Contains(msgLower, `"image_url"`) || strings.Contains(msgLower, `"input_image"`) {
						streamHasImage = true
						break
					}
				}
			}
			if streamHasImage {
				streamCaps := InferCapabilities(target.NativeModel)
				if !hasCapability(streamCaps, CapVision) {
					continue
				}
			}

			preciseReq := routedReq
			if target.NativeModel != "" {
				preciseReq.Model = target.NativeModel
			}
			matched := false
			for _, tier := range tiers {
				for _, pc := range tier.Providers {
					if pc.Provider.ID == target.ProviderID {
						matched = true
						if ok := e.streamProviders(ctx, userID, &preciseReq, settings, []ProviderConfig{pc}, chunkCh, errCh); ok {
							return
						}
					}
				}
			}
			if !matched {
				continue
			}
		}

		// 仅 fallback 策略需要兜底遍历
		if !isCombo || comboStrategy != "auto" {
			for _, tier := range tiers {
				if ok := e.streamProviders(ctx, userID, &routedReq, settings, tier.Providers, chunkCh, errCh); ok {
					return
				}
			}
		}

		errCh <- errors.New("所有层级均已耗尽，没有可用的提供商")
	}()

	return chunkCh, errCh
}

func (e *RouteEngine) streamProviders(ctx context.Context, userID int64, req *models.ChatRequest, settings *RouteSettings, providers []ProviderConfig, chunkCh chan<- *models.StreamChunk, errCh chan<- error) bool {
	tr := trace.FromContext(ctx)
	if tr != nil {
		tr.RecordModel(req.Model)
	}
	var lastErrStatus int
	var lastErrMsg string
	for _, pc := range providers {
		var acct models.Account
		if pc.NoAuth {
			acct = models.Account{ID: 0}
		} else {
			e.mu.Lock()
			counter := e.roundRobinCounters[pc.Provider.ID]
			e.mu.Unlock()

			selectedAcct, err := roundRobinSelect(pc.Provider.ID, pc.Accounts, &counter,
				func(a models.Account) bool {
					return e.breaker.IsCooling(a.ID, req.Model) || e.breaker.IsAccountCooling(a.ID) ||
						e.rateLimiter.Exceeded(a.ID, a.RateLimitRPM, a.RateLimitTPM)
				})
			if err != nil {
				if tr != nil {
					tr.AddAttempt(trace.Attempt{
						ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name,
						Model: req.Model, APIType: pc.Provider.APIType, Status: "error", Error: "无可用账户",
					})
				}
				continue
			}

			e.mu.Lock()
			e.roundRobinCounters[pc.Provider.ID] = counter
			e.mu.Unlock()
			acct = selectedAcct
			e.rateLimiter.RecordRequest(acct.ID)

			// 账号轮换日志
			logger.Debug("账号轮询选中",
				"provider_id", pc.Provider.ID,
				"provider_name", pc.Provider.Name,
				"account_id", acct.ID,
				"model", req.Model,
				"counter", counter,
				"total_accounts", len(pc.Accounts),
			)
			}

		// route 段：记录本次选中的服务商/账户/模型（胜出尝试最终定稿）
		if tr != nil {
			tr.SetDetail("route", map[string]any{
				"provider_id":   pc.Provider.ID,
				"provider_name": pc.Provider.Name,
				"account_id":    acct.ID,
				"model":         req.Model,
				"api_type":      pc.Provider.APIType,
			})
		}

		transl := translator.GetTranslator(pc.Provider.APIType)

		var reqBody []byte
		var headers map[string]string

		// ---- translate（请求）----
		var translateReqMs int64
		var reqInBytes, reqOutBytes int
		if b, err := json.Marshal(req); err == nil {
			reqInBytes = len(b)
		}
		if transl != nil {
			tT := time.Now()
			translatedBody, hdrs, err := transl.TranslateRequest(req)
			translateReqMs = time.Since(tT).Milliseconds()
			if err != nil {
				if tr != nil {
					tr.AddAttempt(trace.Attempt{
						ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name, AccountID: acct.ID,
						Model: req.Model, APIType: pc.Provider.APIType, Status: "error", Error: "请求翻译失败: " + err.Error(),
					})
				}
				continue
			}
			headers = hdrs
			reqBody, err = json.Marshal(translatedBody)
			if err != nil {
				continue
			}
		} else {
			var err error
			reqBody, err = json.Marshal(req)
			if err != nil {
				continue
			}
		}
		reqOutBytes = len(reqBody)

		if headers == nil {
			headers = make(map[string]string)
		}
		if !pc.NoAuth {
			hdrName, hdrVal := e.resolveAuthHeader(&acct, pc.Provider.APIType)
			if hdrVal != "" {
				headers[hdrName] = hdrVal
			}
		}
		if pc.Provider.APIType == "opencode" {
			headers["x-opencode-client"] = "desktop"
		}
		headers["Content-Type"] = "application/json"
		headers["Accept"] = "text/event-stream"

		startTime := time.Now()
		// Cloudflare 等「ID + Key」服务商：base_url 含 {accountId} 占位符，
		// 用当前账户的 extra_config 替换后再补全端点。
		resolvedBase := proxy.SubstituteTemplate(pc.Provider.BaseURL, acct.ExtraConfig)
		chatURL := resolveChatURL(pc.Provider.APIType, resolvedBase, req.Model, true)
		httpResp, err := e.proxy.ProxyStreamRequest(ctx, http.MethodPost, chatURL, headers, bytes.NewReader(reqBody))
		if err != nil {
			upstreamMs := int(time.Since(startTime).Milliseconds())
			logRequest(e.db, ctx, userID, pc.Provider.ID, acct.ID, req.Model, 0, 0, upstreamMs, "error", err.Error())
			e.breaker.RecordFailure(acct.ID, req.Model, 0)
			if tr != nil {
				tr.AddAttempt(trace.Attempt{
					ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name, AccountID: acct.ID,
					Model: req.Model, APIType: pc.Provider.APIType, Status: "error", Error: err.Error(), LatencyMs: int64(upstreamMs),
				})
				tr.RecordSegment("upstream", "error", int64(upstreamMs), map[string]any{
					"provider_id": pc.Provider.ID, "account_id": acct.ID, "model": req.Model,
					"error": err.Error(),
				})
			}
			continue
		}

		if httpResp.StatusCode >= 400 {
			upstreamMs := int(time.Since(startTime).Milliseconds())
			respBytes, _ := io.ReadAll(httpResp.Body)
			httpResp.Body.Close()
			logRequest(e.db, ctx, userID, pc.Provider.ID, acct.ID, req.Model, 0, 0, upstreamMs, "error", string(respBytes))
			e.breaker.RecordFailure(acct.ID, req.Model, httpResp.StatusCode)
			// 记录最后错误（用于 error_shield 聚合展示）
			lastErrStatus = httpResp.StatusCode
			lastErrMsg = string(respBytes)
			if tr != nil {
				tr.AddAttempt(trace.Attempt{
					ProviderID: pc.Provider.ID, ProviderName: pc.Provider.Name, AccountID: acct.ID,
					Model: req.Model, APIType: pc.Provider.APIType, StatusCode: httpResp.StatusCode, Status: "error",
					Error: string(respBytes), LatencyMs: int64(upstreamMs),
				})
			}
			continue
		}

		var streamTransl translator.StreamTranslator
		if transl != nil {
			streamTransl = transl.NewStream()
		}

		var finalUsage models.Usage
		// 流式没有单一响应体，用「转发分片数 + 上游 SSE 字节数」刻画上游产出规模，
		// 供 requestDetails 观测（此前该字段恒为 0，无任何信息量）。
		var chunkCount, sseBytes int
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				httpResp.Body.Close()
				if tr != nil {
					tr.RecordSegment("upstream", "error", int64(time.Since(startTime).Milliseconds()), map[string]any{
						"provider_id": pc.Provider.ID, "account_id": acct.ID, "model": req.Model,
						"error": "客户端断开",
					})
				}
				return true
			default:
			}

			line := scanner.Text()
			sseBytes += len(line) + 1
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk models.StreamChunk
			if streamTransl != nil {
				var rawChunk any
				if err := json.Unmarshal([]byte(data), &rawChunk); err != nil {
					continue
				}
				sc, err := streamTransl.TranslateChunk(rawChunk)
				if err != nil {
					continue
				}
				if sc == nil {
					continue
				}
				chunk = *sc
			} else {
				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					continue
				}
			}

			// 捕获流式结尾的 usage（OpenAI/Zhipu 在 finish chunk 携带；Gemini 在最后一个
			// chunk 的 usageMetadata；Claude 在 message_delta 的 usage）。
			if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 || chunk.Usage.TotalTokens != 0 {
				finalUsage = chunk.Usage
			}

			select {
			case chunkCh <- &chunk:
				chunkCount++
			case <-ctx.Done():
				httpResp.Body.Close()
				if tr != nil {
					tr.RecordSegment("upstream", "error", int64(time.Since(startTime).Milliseconds()), map[string]any{
						"provider_id": pc.Provider.ID, "account_id": acct.ID, "model": req.Model,
						"error": "客户端断开",
					})
				}
				return true
			}
		}

		httpResp.Body.Close()
		upstreamMs := int(time.Since(startTime).Milliseconds())
		// 流式四段追踪定稿（胜出尝试）：upstream 跨整段流，translate 记请求翻译部分
		if tr != nil {
			tr.EndSegment("route", "ok", nil)
			tr.RecordSegment("translate", "ok", translateReqMs, map[string]any{
				"api_type":      pc.Provider.APIType,
				"translated":    transl != nil,
				"stream":        true,
				"model_in":      req.Model,
				"model_out":     req.Model,
				"req_in_bytes":  reqInBytes,
				"req_out_bytes": reqOutBytes,
			})
			tr.RecordSegment("upstream", "ok", int64(upstreamMs), map[string]any{
				"provider_id": pc.Provider.ID,
				"account_id":  acct.ID,
				"model":       req.Model,
				"status_code": httpResp.StatusCode,
				"stream":      true,
				"chunks":      chunkCount,
				"sse_bytes":   sseBytes,
			})
		}

		logRequest(e.db, ctx, userID, pc.Provider.ID, acct.ID, req.Model,
			finalUsage.PromptTokens, finalUsage.CompletionTokens,
			upstreamMs, "success", "")
		recordUsageStats(e.db, userID, pc.Provider.ID, req.Model, pc.PricingType,
			finalUsage.PromptTokens, finalUsage.CompletionTokens)
		e.breaker.RecordSuccess(acct.ID, req.Model)
		e.rateLimiter.RecordTokens(acct.ID, finalUsage.PromptTokens+finalUsage.CompletionTokens)
		return true
	}

	if tr != nil {
		tr.EndSegment("route", "error", map[string]any{"error": "所有层级均已耗尽，没有可用的提供商"})
		tr.RecordSegment("translate", "skip", 0, map[string]any{"reason": "无成功尝试"})
		tr.RecordSegment("upstream", "error", 0, map[string]any{"error": "所有服务商均失败"})
	}
	// 错误屏蔽：如果 combos.error_shield=true，把原始上游错误聚合为友好消息
	if lastErrStatus > 0 && settings != nil && settings.ErrorShield {
		shielded := shieldError(lastErrStatus, lastErrMsg)
		errCh <- fmt.Errorf("上游服务暂时不可用（%s）", shielded)
	} else {
		errCh <- errors.New("所有层级均已耗尽，没有可用的提供商")
	}
	return false
}

// comboConfig 表示组合的完整配置（新格式）
type comboConfig struct {
	Models   []comboModelItem `json:"models"`
	Strategy string           `json:"strategy"` // "fallback" | "round_robin" | "auto"
	// 旧格式兼容
	ModelMapping string `json:"model_mapping"`
	// ErrorShield 控制「上游 HTTP 4xx/5xx 时是否将错误透传给调用方」：
	//   true  = 透传（旧行为，兼容存量用户）
	//   false = 错误屏蔽：记录日志并走 fallback 链，调用方只看到最后一条兜底结果
	//          或「全部失败」聚合错误，而非某个中间上游的 429/500
	ErrorShield bool `json:"error_shield,omitempty"`
}

type comboModelItem struct {
	ID         string `json:"id"`
	ProviderID int64  `json:"provider_id"`
	// Capability 为用户手动指定的能力标签（vision / code / text / long_context / audio / reasoning）。
	// 留空或 "auto" 时由 InferCapabilities 依据模型名自动推断。
	Capability string `json:"capability,omitempty"`

	// probedCaps 为该模型的真实探测结果（models.probed_caps），仅在运行期由
	// loadProbedCaps 注入，不参与组合配置的 JSON 序列化。
	// 实测结论优先于模型名关键词推断，用于纠正「名字像视觉模型但实际不收图片」之类的误判。
	probedCaps map[string]bool
}

// resolveComboCandidates 解析组合配置，返回按策略排序的候选模型列表。
//
// 策略：
//   - auto        ：智能路由。先由 ClassifyIntents 判定请求所需的「能力集合」（图片 / 语音 / 长文本 / 代码 / 推理 / 纯文本），
//     再按「覆盖度」把同时具备多种能力的模型排到最前；未匹配的模型仍保留在末尾作为兜底。
//   - round_robin ：按请求计数轮转起点，实现负载分摊。
//   - fallback    ：保持用户配置顺序，首个失败后依次回退。
//
// 返回的列表天然支持"组合内回退"：调用方依次尝试候选，直到某个成功为止。
func (e *RouteEngine) resolveComboCandidates(comboName string, userID int64, req *models.ChatRequest) ([]comboModelItem, string, error) {
	var comboID int64
	var configJSON string
	err := e.db.QueryRow(
		`SELECT id, config FROM combos WHERE name = ? AND user_id = ? AND enabled = 1`,
		comboName, userID,
	).Scan(&comboID, &configJSON)
	if err != nil {
		return nil, "", err
	}

	var config comboConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, "", err
	}

	// 新格式：多模型数组
	if len(config.Models) > 0 {
		items := make([]comboModelItem, 0, len(config.Models))
		for _, m := range config.Models {
			if strings.TrimSpace(m.ID) != "" {
				items = append(items, m)
			}
		}
		if len(items) == 0 {
			return nil, config.Strategy, fmt.Errorf("combo %q has no valid model config", comboName)
		}

		switch config.Strategy {
		case "auto":
			// 智能路由依赖能力判定，先注入探测过的真实能力再排序
			e.loadProbedCaps(items)
			intents := ClassifyIntents(req)
			items = SelectByIntent(items, intents)
		case "round_robin":
			// 基于组合维度的请求计数轮转（复用 roundRobinCounters，用负数 key 避免与服务商 ID 冲突）
			e.mu.Lock()
			key := -comboID
			start := e.roundRobinCounters[key]
			e.roundRobinCounters[key] = (start + 1) % len(items)
			e.mu.Unlock()
			rotated := make([]comboModelItem, 0, len(items))
			for i := 0; i < len(items); i++ {
				rotated = append(rotated, items[(start+i)%len(items)])
			}
			items = rotated
			// 模型轮换日志
			logger.Debug("模型轮询选中",
				"combo_id", comboID,
				"combo_name", comboName,
				"strategy", config.Strategy,
				"start_index", start,
				"total_models", len(items),
				"selected_model", items[0].ID,
			)
		}
		// 修改为随机洗牌：每次请求打乱候选顺序，避免固定顺序总是失败后一直碰不到可用模型
		rand.Seed(time.Now().UnixNano())
		// Fisher-Yates 洗牌算法
		for i := len(items) - 1; i > 0; i-- {
			j := rand.Intn(i + 1)
			items[i], items[j] = items[j], items[i]
		}
		if len(items) > 1 {
			logger.Debug("模型随机洗牌",
				"combo_id", comboID,
				"combo_name", comboName,
				"strategy", config.Strategy,
				"total_models", len(items),
				"first_model", items[0].ID,
			)
		}
		return items, config.Strategy, nil
	}

	// 旧格式兼容: { model_mapping: "xxx" }
	if config.ModelMapping != "" {
		return []comboModelItem{{ID: config.ModelMapping}}, config.Strategy, nil
	}

	return nil, config.Strategy, fmt.Errorf("combo %q has no valid model config", comboName)
}

// loadProbedCaps 就地为候选条目填充 models.probed_caps 中的真实探测结果。
// 逐条查询（组合内模型通常个位数），任一条失败都静默跳过——探测结果只是增强信息，
// 缺失时 itemCapabilities 会自动回退到模型名关键词推断。
func (e *RouteEngine) loadProbedCaps(items []comboModelItem) {
	for i := range items {
		var raw sql.NullString
		var err error
		if items[i].ProviderID > 0 {
			err = e.db.QueryRow(
				`SELECT probed_caps FROM models WHERE provider_id = ? AND model_id = ?`,
				items[i].ProviderID, items[i].ID,
			).Scan(&raw)
		} else {
			err = e.db.QueryRow(
				`SELECT probed_caps FROM models WHERE model_id = ? AND probed_caps IS NOT NULL LIMIT 1`,
				items[i].ID,
			).Scan(&raw)
		}
		if err != nil || !raw.Valid || raw.String == "" {
			continue
		}
		var caps map[string]bool
		if json.Unmarshal([]byte(raw.String), &caps) == nil && len(caps) > 0 {
			items[i].probedCaps = caps
		}
	}
}

// comboTarget 是一个已解析到具体服务商的候选目标。
type comboTarget struct {
	ProviderID  int64
	NativeModel string
}

// resolveTargets 把组合候选逐个解析为 (服务商ID, 原生模型ID)。
// 条目未带 provider_id 时回退到 models 表模糊匹配；匹配不到的则搜索所有 providers 查找同名模型。
func (e *RouteEngine) resolveTargets(items []comboModelItem) []comboTarget {
	targets := make([]comboTarget, 0, len(items))
	for _, item := range items {
		providerID := item.ProviderID
		nativeModel := item.ID
		if providerID <= 0 {
			// 第一步：从 models 表按 display_name / model_id 精确查找
			pid, nm, err := resolveModelProvider(e.db, item.ID)
			if err == nil && pid > 0 {
				providerID, nativeModel = pid, nm
			} else {
				// 第二步：搜索所有 providers 表，找 model_id 匹配的 provider
				// 用 model_id 和 provider_id 的模糊匹配，找任意一个有该模型的 provider
				var provID int64
				var modelFromProvider string
				err2 := e.db.QueryRow(
					`SELECT p.id, m.model_id FROM providers p
					 INNER JOIN models m ON m.provider_id = p.id
					 WHERE p.enabled = 1 AND m.enabled = 1
					 AND (m.model_id = ? OR m.display_name = ?)
					 LIMIT 1`,
					item.ID, item.ID,
				).Scan(&provID, &modelFromProvider)
				if err2 == nil && provID > 0 {
					providerID, nativeModel = provID, modelFromProvider
				}
			}
		}
		if providerID <= 0 {
			continue
		}
		targets = append(targets, comboTarget{ProviderID: providerID, NativeModel: nativeModel})
	}
	return targets
}

func stringInSlice(s string, slice []string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// logRequest 写入请求明细（request_logs）。注意：request_tokens / response_tokens 存的是
// 真实 token 数（prompt / completion），而非字节数——早期版本误把字节数塞进这两列，
// 导致明细与后续聚合失真。request_details 列保存四段式 requestDetails 追踪的 JSON（来自 ctx）。
func logRequest(db *sql.DB, ctx context.Context, userID, providerID, accountID int64, model string, requestTokens, responseTokens, latency int, status, errMsg string) {
	details := ""
	pickedModel := ""
	if tr := trace.FromContext(ctx); tr != nil {
		details = tr.JSON()
		pickedModel = tr.PickedModel()
	}
	_, _ = db.Exec(
		`INSERT INTO request_logs (user_id, provider_id, account_id, model, picked_model, request_tokens, response_tokens, latency_ms, status, error_message, request_details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, providerID, accountID, model, pickedModel, requestTokens, responseTokens, latency, status, errMsg, details, time.Now().Format(time.RFC3339),
	)
}

// estimateCost 用量成本估算：优先用 price 包的同步定价，找不到则 fallback 到 $2/1M。
func estimateCost(promptTokens, completionTokens int, pricingType, model string) float64 {
	if pricingType == "free" || pricingType == "free_trial" {
		return 0
	}
	// price 包已按 model 精确匹配；免费模型返回 0（不是 $2/1M）
	inpCost := price.EstimateInputCost(model, promptTokens)
	outCost := price.EstimateOutputCost(model, completionTokens)
	if inpCost > 0 || outCost > 0 {
		return inpCost + outCost
	}
	// fallback：$2/1M tokens（旧占位费率）
	total := promptTokens + completionTokens
	return float64(total) / 1e6 * 2.0
}

// recordUsageStats 把一次成功请求的 token 用量按「日 × 用户 × 服务商 × 模型」聚合进 usage_stats。
// Dashboard 直接 SUM 该表，因此必须用事务 upsert 保证聚合正确（该表无唯一约束也不影响正确性，
// 因为聚合靠 SUM；并发重复插入只会产生多行，SUM 仍正确）。
func recordUsageStats(db *sql.DB, userID, providerID int64, model, pricingType string, promptTokens, completionTokens int) {
	if promptTokens == 0 && completionTokens == 0 {
		return
	}
	date := time.Now().Format("2006-01-02")
	total := promptTokens + completionTokens
	cost := estimateCost(promptTokens, completionTokens, pricingType, model)

	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	var id int64
	var curReq, curTok int
	var curCost float64
	err = tx.QueryRow(
		"SELECT id, request_count, total_tokens, estimated_cost FROM usage_stats WHERE date = ? AND user_id = ? AND provider_id = ? AND model = ?",
		date, userID, providerID, model,
	).Scan(&id, &curReq, &curTok, &curCost)
	if err == sql.ErrNoRows {
		if _, err = tx.Exec(
			"INSERT INTO usage_stats (user_id, date, provider_id, model, request_count, total_tokens, estimated_cost) VALUES (?, ?, ?, ?, 1, ?, ?)",
			userID, date, providerID, model, total, cost,
		); err != nil {
			return
		}
	} else if err == nil {
		if _, err = tx.Exec(
			"UPDATE usage_stats SET request_count = request_count + 1, total_tokens = total_tokens + ?, estimated_cost = estimated_cost + ? WHERE id = ?",
			total, cost, id,
		); err != nil {
			return
		}
	} else {
		return
	}
	if err := tx.Commit(); err != nil {
		return
	}
}

// shieldError 把上游原始 HTTP 错误聚合为友好提示，避免把服务商的内部细节暴露给调用方。
// 主要屏蔽 429（限流）和 5xx（服务端错误），其他状态码原样透出。
func shieldError(statusCode int, body string) string {
	switch {
	case statusCode == 429:
		return "请求频率过高，已自动切换到备用通道"
	case statusCode >= 500 && statusCode < 600:
		return "上游服务暂时繁忙，正在尝试其他通道"
	case statusCode == 401 || statusCode == 403:
		return "认证信息已过期，请检查账户配置"
	case statusCode == 400:
		// 从响应体提取 message 字段（如果有的话）
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &errResp); err == nil && errResp.Error.Message != "" {
			return "请求参数异常：" + errResp.Error.Message
		}
		return "请求参数有误"
	default:
		return fmt.Sprintf("上游返回 %d，请检查服务商状态", statusCode)
	}
}
