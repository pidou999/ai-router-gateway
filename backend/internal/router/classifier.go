package router

import (
	"sort"
	"strings"

	"ai-router-gateway/internal/models"
)

// Capability 表示模型能力 / 请求所需能力。
// 组合（combo）的智能路由（strategy = "auto"）依据「请求意图」与「模型能力」匹配来挑选模型：
// 用户发图片 → 走视觉模型；写代码 → 走代码模型；超长上下文 → 走长上下文模型；其余 → 文本模型。
type Capability string

const (
	CapText        Capability = "text"         // 通用文本对话（所有模型默认具备）
	CapVision      Capability = "vision"       // 图片理解
	CapCode        Capability = "code"         // 代码生成 / 调试
	CapAudio       Capability = "audio"        // 语音输入
	CapLongContext Capability = "long_context" // 超长上下文
	CapReasoning   Capability = "reasoning"    // 深度推理（o1/o3/R1/thinking 系列）
	CapAuto        Capability = "auto"         // 仅用于组合条目：表示由模型名自动推断能力
)

// longContextThreshold 触发「长上下文意图」的字符阈值。
// 取值偏保守（约 2 万 token 量级），避免普通多轮对话被误判为长文本。
const longContextThreshold = 60000

// visionModelKeywords 视觉模型名特征（小写匹配）。
var visionModelKeywords = []string{
	"vision", "-vl", "vl-", "vl_", "llava", "pixtral", "internvl", "cogvlm",
	"minicpm-v", "glm-4v", "glm-4.1v", "glm-4.5v", "step-1v", "step-1o", "molmo",
	"gpt-4o", "gpt-4.1", "gpt-4-turbo", "gpt-5", "o4-mini",
	"claude-3", "claude-4", "claude-sonnet", "claude-opus", "claude-haiku",
	"gemini", "grok-vision", "grok-2-vision", "grok-4", "qwen-vl", "qvq",
	"llama-3.2-11b", "llama-3.2-90b", "llama-4", "phi-3-vision", "phi-4-multimodal",
	"kimi-vl", "doubao-vision", "ernie-4.5-vl", "seed-1.6",
}

// codeModelKeywords 代码模型名特征。
var codeModelKeywords = []string{
	"coder", "code-", "-code", "codex", "codestral", "codegeex", "codellama",
	"code-llama", "starcoder", "devstral", "deepseek-coder", "codegemma", "codeqwen",
	"granite-code", "opencoder", "qwen3-coder", "kimi-k2",
}

// reasoningModelKeywords 推理模型名特征（推理模型通常代码能力也强）。
var reasoningModelKeywords = []string{
	"o1", "o3", "o4", "deepseek-r1", "-r1", "reasoner", "thinking", "qwq",
	"marco-o1", "skywork-o1", "glm-z1", "magistral", "minimax-m1", "minimax-m2",
}

// longContextModelKeywords 长上下文模型名特征。
var longContextModelKeywords = []string{
	"128k", "200k", "256k", "1m", "long", "longcat", "gemini-1.5", "gemini-2",
	"kimi", "moonshot", "claude-3", "claude-4", "claude-sonnet", "claude-opus",
	"minimax", "gpt-4.1", "llama-4",
}

// audioModelKeywords 语音模型名特征。
var audioModelKeywords = []string{
	"audio", "whisper", "voice", "omni", "realtime", "qwen2-audio", "step-1o",
}

// InferCapabilities 依据模型名（含服务商前缀）保守推断模型能力。
// 所有模型默认具备 CapText；命中特征时追加对应能力。
// 该推断仅在用户未手动为组合条目指定 capability 时使用。
func InferCapabilities(modelID string) []Capability {
	name := strings.ToLower(strings.TrimSpace(modelID))
	caps := []Capability{CapText}
	if name == "" {
		return caps
	}

	add := func(c Capability) {
		for _, existing := range caps {
			if existing == c {
				return
			}
		}
		caps = append(caps, c)
	}

	if containsAny(name, visionModelKeywords) {
		add(CapVision)
	}
	if containsAny(name, codeModelKeywords) {
		add(CapCode)
	}
	if containsAny(name, reasoningModelKeywords) {
		add(CapReasoning)
		// 推理模型通常也擅长代码，作为代码意图的次优候选
		add(CapCode)
	}
	if containsAny(name, longContextModelKeywords) {
		add(CapLongContext)
	}
	if containsAny(name, audioModelKeywords) {
		add(CapAudio)
	}
	return caps
}

// ResolveCapabilities 在关键词推断的基础上叠加真实探测结果。
//
// probed 为「探测过的能力 → 是否支持」的映射（来自 models.probed_caps）：
//   - true：实测支持，即使模型名没有相应特征也强制加入；
//   - false：实测不支持，即使模型名命中关键词也剔除（关键词误判的纠正）。
//
// 键缺失表示未探测，保持关键词推断的结论。CapText 始终保留，保证纯文本请求总有候选。
func ResolveCapabilities(modelID string, probed map[string]bool) []Capability {
	caps := InferCapabilities(modelID)
	if len(probed) == 0 {
		return caps
	}

	result := make([]Capability, 0, len(caps)+len(probed))
	for _, c := range caps {
		// 实测明确不支持的能力从推断结果中剔除（text 永远保留）
		if supported, tested := probed[string(c)]; tested && !supported && c != CapText {
			continue
		}
		result = append(result, c)
	}
	// 实测支持但关键词没识别出来的能力补进去
	for name, supported := range probed {
		if !supported {
			continue
		}
		c := Capability(name)
		if !hasCapability(result, c) {
			result = append(result, c)
		}
	}
	return result
}

func containsAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// 代码意图强特征（命中任意一个即判定为代码请求）。
var codeStrongMarkers = []string{
	"```", "写一个函数", "写个函数", "写段代码", "写一段代码", "帮我写代码", "写代码",
	"这段代码", "以下代码", "下面的代码", "代码报错", "报错信息", "堆栈", "调用栈",
	"单元测试", "重构", "修复bug", "修复 bug", "改bug", "调试",
	"traceback", "stack trace", "stacktrace", "segmentation fault", "compile error",
	"syntax error", "npm install", "pip install", "git commit", "pull request",
	"refactor", "unit test", "debug this", "fix this bug", "fix the bug",
	"write a function", "write code", "implement a class", "code review",
	// 补充：更贴近日常「写代码」措辞
	"代码", "编程", "脚本", "代码实现", "python代码", "java代码",
	"用python", "用java", "用go", "用javascript", "用js", "用ts",
	"实现一个", "编写一个", "生成代码", "写个脚本",
}

// 代码意图弱特征（需命中 2 个及以上）。
var codeWeakMarkers = []string{
	"函数", "变量", "接口", "编译", "报错", "异常", "算法", "数据结构", "正则",
	"python", "golang", "java", "javascript", "typescript", "rust", "c++", "sql",
	"react", "vue", "docker", "kubernetes", "api", "json", "http", "class ",
	"def ", "func ", "import ", "return ", "const ", "package ", "select ",
	"bug", "error", "exception", "script", "server", "database",
}

// ClassifyIntents 识别一次请求所需的「能力集合」（而非单一意图）。
//
// 例如带图的代码请求会同时返回 [CapVision, CapCode]，纯文本仅 [CapText]。
// 与 ClassifyIntent 只返回一个主意图不同，这里返回完整集合，
// 让 SelectByIntent 能按「覆盖度」优先选择同时具备多种能力的模型
// （如既能读图又能写代码的 Qwen3-Next），而不是只挑单一维度。
//
// 检测项（硬约束在前，漏判会直接丢信息）：
//  1. 图片 → CapVision
//  2. 音频 → CapAudio
//  3. 超长上下文 → CapLongContext
//  4. 代码特征 → CapCode
//  5. 推理特征 → CapReasoning
//  6. 其余 → CapText
func ClassifyIntents(req *models.ChatRequest) []Capability {
	needed := make([]Capability, 0, 4)
	seen := make(map[Capability]bool)
	add := func(c Capability) {
		if !seen[c] {
			seen[c] = true
			needed = append(needed, c)
		}
	}

	if req == nil || len(req.Messages) == 0 {
		return []Capability{CapText}
	}

	hasImage := false
	hasAudio := false
	totalChars := 0

	// 只检测最新一条 user 消息来决定本次请求需要的能力。
	// 历史上下文不应当影响本次路由——路由关心的是「这条新消息需要什么能力」。
	latest := req.Messages[len(req.Messages)-1]
	if latest.Role != "user" {
		return []Capability{CapText}
	}
	raw := string(latest.Content)

	for _, msg := range req.Messages {
		// 遍历整段对话历史仅做图片/音频检测 + 字符数统计（长上下文依据）
		msgRaw := string(msg.Content)
		totalChars += len(msgRaw)

		// 多模态消息在 models.MessageContent 中原样保留了原始 JSON 数组，
		// 因此可直接从原始串里识别图片 / 音频 part。
		if len(msgRaw) > 0 && msgRaw[0] == '[' {
			msgLower := strings.ToLower(msgRaw)
			if strings.Contains(msgLower, `"image_url"`) || strings.Contains(msgLower, `"type":"image"`) ||
				strings.Contains(msgLower, `"type": "image"`) || strings.Contains(msgLower, `"input_image"`) {
				hasImage = true
			}
			if strings.Contains(msgLower, `"input_audio"`) || strings.Contains(msgLower, `"audio_url"`) ||
				strings.Contains(msgLower, `"type":"audio"`) || strings.Contains(msgLower, `"type": "audio"`) {
				hasAudio = true
			}
		}

	}

	if hasImage {
		add(CapVision)
	}
	if hasAudio {
		add(CapAudio)
	}
	if len(raw) >= longContextThreshold {
		add(CapLongContext)
	}
	if looksLikeCode(latest.Content.String()) {
		add(CapCode)
	}
	if looksLikeReasoning(latest.Content.String()) {
		add(CapReasoning)
	}

	if len(needed) == 0 {
		add(CapText)
	}
	return needed
}

// ClassifyIntent 返回请求的主意图（单一能力），供日志展示与旧逻辑兼容。
// 优先级：视觉 > 音频 > 长上下文 > 代码 > 推理 > 纯文本。
func ClassifyIntent(req *models.ChatRequest) Capability {
	intents := ClassifyIntents(req)
	order := []Capability{CapVision, CapAudio, CapLongContext, CapCode, CapReasoning, CapText}
	for _, c := range order {
		for _, it := range intents {
			if it == c {
				return c
			}
		}
	}
	return CapText
}

// intentsLabel 将能力集合拼成逗号分隔的字符串，用于日志展示（如 "vision,code"）。
func intentsLabel(intents []Capability) string {
	parts := make([]string, len(intents))
	for i, c := range intents {
		parts[i] = string(c)
	}
	return strings.Join(parts, ",")
}

// looksLikeCode 基于关键词打分判断文本是否为编程类请求。
func looksLikeCode(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)

	for _, marker := range codeStrongMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	hits := 0
	for _, marker := range codeWeakMarkers {
		if strings.Contains(lower, marker) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

// reasoningStrongMarkers 推理意图强特征（命中任意一个即判定为推理请求）。
var reasoningStrongMarkers = []string{
	"think step by step", "step by step", "逐步思考", "逐步推理", "深度推理", "深入思考",
	"推理过程", "let's think", "think through", "reasoning", "chain of thought", "cot",
	"请思考", "仔细分析", "为什么", "证明", "推导", "解释原理", "底层原理",
}

// reasoningWeakMarkers 推理意图弱特征（需命中 2 个及以上）。
var reasoningWeakMarkers = []string{
	"数学", "算法", "逻辑", "证明", "推导", "分析", "原因", "优化", "架构", "复杂",
	"why", "math", "logic", "optimize", "design", "原理", "本质", "区别",
}

// looksLikeReasoning 基于关键词打分判断文本是否为推理类请求。
func looksLikeReasoning(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range reasoningStrongMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	hits := 0
	for _, marker := range reasoningWeakMarkers {
		if strings.Contains(lower, marker) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

// capabilityPreference 给定意图，返回按优先级排列的可接受能力链。
// 例如代码意图优先选代码模型，其次推理模型，最后退回通用文本模型。
func capabilityPreference(intent Capability) []Capability {
	switch intent {
	case CapVision:
		return []Capability{CapVision}
	case CapAudio:
		return []Capability{CapAudio, CapVision}
	case CapCode:
		return []Capability{CapCode, CapReasoning, CapText}
	case CapLongContext:
		return []Capability{CapLongContext, CapText}
	default:
		return []Capability{CapText}
	}
}

// hasCapability 判断能力集合中是否包含目标能力。
func hasCapability(caps []Capability, target Capability) bool {
	for _, c := range caps {
		if c == target {
			return true
		}
	}
	return false
}

// itemCapabilities 返回组合条目的实际能力集合，优先级从高到低：
//  1. 用户在组合里手动指定的能力标签（非 auto）；
//  2. 提供商详情页的真实探测结果（probedCaps）；
//  3. 模型名关键词推断。
func itemCapabilities(item comboModelItem) []Capability {
	manual := Capability(strings.ToLower(strings.TrimSpace(item.Capability)))
	if manual != "" && manual != CapAuto {
		// 手动指定的能力同时保留 text 作为兜底，避免纯文本请求无候选
		if manual == CapText {
			return []Capability{CapText}
		}
		return []Capability{manual, CapText}
	}
	return ResolveCapabilities(item.ID, item.probedCaps)
}

// SelectByIntent 按「请求所需能力集合」对组合内模型重排序。
//
// 评分规则（越靠前越优先）：
//  1. 覆盖度：拥有的「所需能力」越多越靠前。例如带图的代码请求，
//     同时具备 vision+code 的模型优先于仅有 vision 的模型——解决了单意图模式下
//     「能力越少越优先」把代码模型排到后面的问题；
//  2. 覆盖度相同时，能力集越「精」（能力数越少）越靠前，避免用重型模型处理简单请求；
//  3. 完全不匹配的模型保留在末尾作为兜底，保证始终有可用候选。
func SelectByIntent(items []comboModelItem, intents []Capability) []comboModelItem {
	if len(items) <= 1 || len(intents) == 0 {
		return items
	}

	type scored struct {
		idx      int
		coverage int
		total    int
	}
	scoredList := make([]scored, len(items))
	for i, item := range items {
		caps := itemCapabilities(item)
		cov := 0
		for _, need := range intents {
			if hasCapability(caps, need) {
				cov++
			}
		}
		scoredList[i] = scored{idx: i, coverage: cov, total: len(caps)}
	}

	// 覆盖度降序；同覆盖度时：
	//  1. 如果请求只需要纯文本 → 优先纯文本模型（不带 vision/audio）
	//  2. 否则能力集越「精」（能力数越少）越靠前，避免用重型模型处理简单请求；
	//  3. 再保持原顺序稳定。
	sort.SliceStable(scoredList, func(a, b int) bool {
		if scoredList[a].coverage != scoredList[b].coverage {
			return scoredList[a].coverage > scoredList[b].coverage
		}
		// 如果请求只需要纯文本，优先纯文本模型（不包含额外能力）
		if len(intents) == 1 && intents[0] == CapText {
			aTotal := scoredList[a].total
			bTotal := scoredList[b].total
			// 都是纯文本或都不是 → 继续按能力数排序
			if (aTotal == 1) == (bTotal == 1) {
				return scoredList[a].total < scoredList[b].total
			}
			// 只有一个是纯文本（能力数=1）→ 纯文本靠前
			return aTotal == 1
		}
		if scoredList[a].total != scoredList[b].total {
			return scoredList[a].total < scoredList[b].total
		}
		return scoredList[a].idx < scoredList[b].idx
	})

	ordered := make([]comboModelItem, len(items))
	for i, s := range scoredList {
		ordered[i] = items[s.idx]
	}
	return ordered
}
