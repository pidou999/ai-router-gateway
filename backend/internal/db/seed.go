package db

import (
	"database/sql"
)

// presetProvider 描述一个待预置的服务商。
// BaseURL 只存“基址”(如 https://api.openai.com/v1)，不再写完整的 /chat/completions 路径。
// 网关在转发时会由 router/engine.go 的 resolveChatURL 自动补全 /chat/completions，
// 详见 internal/handlers/provider_handler.go 中同名的 resolveChatURL。
// 约定：
//   - OpenAI 兼容服务商：只填基址（不含 /chat/completions），后台自动补全；
//   - 非 OpenAI 协议（claude/gemini/vertex 等）：填其协议真实地址，需 translator 才能转发，
//     这里仅作“可见预设”，并在备注中说明。
type presetProvider struct {
	Name        string
	BaseURL     string
	APIType     string
	Priority    int
	PricingType string
	Note        string
}

// pricingTypeOf 根据 api_type 推导默认收费类型
func pricingTypeOf(apiType string) string {
	switch apiType {
	case "ollama":
		return "free"
	case "openai", "deepseek", "qwen", "siliconflow", "moonshot", "zhipu", "glm",
		"minimax", "alibaba", "volcengine", "gemini", "github", "codebuddy":
		return "free_trial"
	default:
		return "paid"
	}
}

// presetProviders 参考 9Router（decolua/9router）的注册表，整理出常用服务商及其真实 API 地址。
// 仅在 providers 表为空时写入，避免覆盖用户后续新增/修改的数据。
// 注意：所有 OpenAI 兼容服务商的 BaseURL 只填基址，/chat/completions 由后台自动补全。
var presetProviders = []presetProvider{
	{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", APIType: "openai", Priority: 100},
	{Name: "Anthropic (Claude)", BaseURL: "https://api.anthropic.com/v1/messages", APIType: "anthropic", Priority: 95, Note: "Claude 协议，需实现 translator 才能转发（保留 /v1/messages，后台不补全 /chat/completions）"},
	{Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/models", APIType: "gemini", Priority: 90, Note: "Gemini 协议，需实现 translator 才能转发"},
	{Name: "Azure OpenAI", BaseURL: "https://your-resource.openai.azure.com/openai/deployments/{deployment}/chat/completions?api-version=2024-10-21", APIType: "azure", Priority: 85, Note: "请将 your-resource 与 {deployment} 替换为实际值（含部署路径与查询参数，后台原样转发）"},
	{Name: "Groq", BaseURL: "https://api.groq.com/openai/v1", APIType: "groq", Priority: 80},
	{Name: "DeepSeek", BaseURL: "https://api.deepseek.com", APIType: "deepseek", Priority: 78},
	{Name: "通义千问 (DashScope)", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", APIType: "qwen", Priority: 76},
	{Name: "硅基流动 (SiliconFlow)", BaseURL: "https://api.siliconflow.cn/v1", APIType: "siliconflow", Priority: 74},
	{Name: "月之暗面 (Kimi)", BaseURL: "https://api.kimi.com/coding/v1", APIType: "moonshot", Priority: 72},
	{Name: "智谱 GLM (China)", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIType: "zhipu", Priority: 70},
	{Name: "智谱 GLM (z.ai)", BaseURL: "https://api.z.ai/api/coding/paas/v4", APIType: "glm", Priority: 68},
	{Name: "Mistral", BaseURL: "https://api.mistral.ai/v1", APIType: "mistral", Priority: 66},
	{Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", APIType: "openrouter", Priority: 64},
	{Name: "Together AI", BaseURL: "https://api.together.xyz/v1", APIType: "together", Priority: 62},
	{Name: "Fireworks AI", BaseURL: "https://api.fireworks.ai/inference/v1", APIType: "fireworks", Priority: 60},
	{Name: "Cerebras", BaseURL: "https://api.cerebras.ai/v1", APIType: "cerebras", Priority: 58},
	{Name: "Cohere", BaseURL: "https://api.cohere.ai/v1", APIType: "cohere", Priority: 56},
	{Name: "Perplexity", BaseURL: "https://api.perplexity.ai", APIType: "perplexity", Priority: 54},
	{Name: "xAI (Grok)", BaseURL: "https://api.x.ai/v1", APIType: "xai", Priority: 52},
	{Name: "NVIDIA NIM", BaseURL: "https://integrate.api.nvidia.com/v1", APIType: "nvidia", Priority: 50},
	{Name: "Hyperbolic", BaseURL: "https://api.hyperbolic.xyz/v1", APIType: "hyperbolic", Priority: 48},
	{Name: "Nebius AI", BaseURL: "https://api.studio.nebius.ai/v1", APIType: "nebius", Priority: 46},
	{Name: "Venice AI", BaseURL: "https://api.venice.ai/api/v1", APIType: "venice", Priority: 44},
	{Name: "Chutes AI", BaseURL: "https://llm.chutes.ai/v1", APIType: "chutes", Priority: 42},
	{Name: "Featherless", BaseURL: "https://api.featherless.ai/v1", APIType: "featherless", Priority: 40},
	{Name: "火山方舟 (Volcengine Ark)", BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3", APIType: "volcengine", Priority: 38},
	{Name: "MiniMax", BaseURL: "https://api.minimax.io/v1", APIType: "minimax", Priority: 36},
	{Name: "阿里云百炼 (Alibaba)", BaseURL: "https://coding.dashscope.aliyuncs.com/v1", APIType: "alibaba", Priority: 34},
	{Name: "腾讯 CodeBuddy CN", BaseURL: "https://copilot.tencent.com/v2", APIType: "codebuddy", Priority: 32},
	{Name: "GitHub Copilot", BaseURL: "https://api.githubcopilot.com", APIType: "github", Priority: 30},
	{Name: "Ollama (本地)", BaseURL: "http://localhost:11434/v1", APIType: "ollama", Priority: 28},
	// 免费服务商：参考 9Router / QwenPaw 的 OpenCode、Kilo 免费层实现
	// OpenCode：公共无鉴权 OpenAI 兼容端点，免费模型 id 以 "-free" 结尾，无需 API Key
	{Name: "OpenCode", BaseURL: "https://opencode.ai/zen/v1", APIType: "opencode", Priority: 27, PricingType: "free"},
	// Kilo Code：完全免费（OpenAI 兼容网关），免费模型 id 以 ":free" 结尾，无需 API Key（对齐 QwenPaw 的 require_api_key=False）
	{Name: "Kilo Code", BaseURL: "https://api.kilo.ai/api/gateway", APIType: "kilo", Priority: 26, PricingType: "free"},
	{Name: "Vertex AI", BaseURL: "https://aiplatform.googleapis.com", APIType: "vertex", Priority: 25, Note: "Vertex 协议，需实现 translator 才能转发"},
	{Name: "Claude Code", BaseURL: "https://api.anthropic.com/v1/messages", APIType: "claude", Priority: 24, Note: "Claude 协议，需实现 translator 才能转发（保留 /v1/messages，后台不补全 /chat/completions）"},
	// Cloudflare Workers AI：凭据为 Account ID + API Token 两项。
	// {accountId} 占位符在转发时由账户 extra_config 替换，见 proxy.SubstituteTemplate。
	{Name: "Cloudflare (Workers AI)", BaseURL: "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1", APIType: "cloudflare", Priority: 23, PricingType: "free_trial", Note: "需在账户中同时填写 API Token 与 Account ID；{accountId} 占位符由账户配置自动替换"},
}

// incrementalSeeds 定义「批次化」的增量预置服务商。
//
// 背景：SeedProviders 只在 providers 表为空时全量写入，因此老库无法获得后续新增的预置项。
// 这里按批次补齐：每个批次执行一次后在 settings 表写入标记，之后不再重复执行。
// 这样即使用户主动删除了某个预置服务商，重启也不会把它塞回来。
//
// 新增预置服务商时：追加一个新批次（key 递增），不要修改已发布的旧批次。
var incrementalSeeds = []struct {
	Key       string // settings 表中的标记键，全局唯一且只增不改
	Providers []presetProvider
}{
	{
		Key: "seed_batch_cloudflare",
		Providers: []presetProvider{
			// Cloudflare Workers AI：需要 Account ID + API Token 两项凭据。
			// base_url 中的 {accountId} 占位符会在转发时被账户的 extra_config
			// （形如 {"accountId":"xxx"}）替换，参见 proxy.SubstituteTemplate。
			{
				Name:        "Cloudflare (Workers AI)",
				BaseURL:     "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1",
				APIType:     "cloudflare",
				Priority:    23,
				PricingType: "free_trial",
				Note:        "需在账户中同时填写 API Token 与 Account ID；{accountId} 占位符由账户配置自动替换",
			},
		},
	},
}

// applyIncrementalSeeds 按批次补齐预置服务商。
// 每个批次仅执行一次（以 settings 表中的标记为准），批次内按名称去重，
// 已存在同名服务商时跳过，不覆盖用户已有配置。
func applyIncrementalSeeds(db *sql.DB, dialect Dialect) error {
	for _, batch := range incrementalSeeds {
		var done string
		err := db.QueryRow("SELECT value FROM settings WHERE key = ?", batch.Key).Scan(&done)
		if err == nil {
			// 该批次已执行过，跳过
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}

		for _, p := range batch.Providers {
			var exists int
			if err := db.QueryRow("SELECT COUNT(*) FROM providers WHERE name = ?", p.Name).Scan(&exists); err != nil {
				return err
			}
			if exists > 0 {
				continue
			}
			pt := p.PricingType
			if pt == "" {
				pt = pricingTypeOf(p.APIType)
			}
			if _, err := db.Exec(
				"INSERT INTO providers (name, base_url, api_type, enabled, priority, health_status, pricing_type, auto_sync) VALUES (?, ?, ?, 1, ?, 'unknown', ?, 0)",
				p.Name, p.BaseURL, p.APIType, p.Priority, pt,
			); err != nil {
				return err
			}
		}

		// 标记该批次已完成，避免重复插入（含用户手动删除后的“复活”）
		if _, err := db.Exec(ConflictActionSQL(dialect), batch.Key); err != nil {
			return err
		}
	}
	return nil
}

// SeedProviders 写入预设服务商。
//
// 两段式：
//  1. 全新库（providers 表为空）：全量写入 presetProviders；
//  2. 已有库：跳过全量写入，改由 applyIncrementalSeeds 按批次补齐后续新增的预置项。
//
// 无论哪种情况都会走一次 applyIncrementalSeeds，以便统一写入批次标记
// （全新库中这些服务商已由全量写入创建，增量阶段按名称去重后只写标记）。
func SeedProviders(db *sql.DB, dialect Dialect) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM providers").Scan(&count); err != nil {
		return err
	}

	if count == 0 {
		for _, p := range presetProviders {
			pt := p.PricingType
			if pt == "" {
				pt = pricingTypeOf(p.APIType)
			}
			if _, err := db.Exec(
				"INSERT INTO providers (name, base_url, api_type, enabled, priority, health_status, pricing_type, auto_sync) VALUES (?, ?, ?, 1, ?, 'unknown', ?, 0)",
				p.Name, p.BaseURL, p.APIType, p.Priority, pt,
			); err != nil {
				return err
			}
		}
	}

	return applyIncrementalSeeds(db, dialect)
}
