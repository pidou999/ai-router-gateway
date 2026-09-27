# AI Router Gateway 下一步开发方案（基于 9Router 源码深度研究）

> 研究基线：9Router（Next.js 16，1068 源文件）vs 本项目 `ai-router-gateway`（Go 1.26.4 + Gin / React18 + TS + Vite）
> 研究方式：4 路并行 Agent（路由·负载均衡·故障转移 / 协议适配层 / 功能全景 / 本项目现状盘点）+ 实测验证 4 个关键缺陷
> 结论一句话：**控制面（CRUD / 前端 / 模型管理）完成度高；数据面 5 环节仅「转发」完整可用，协议转换与计量落库完全未接线，鉴权与选路部分可用。**

---

## 一、数据面请求链路接线状态（实测结论）

下图横排展示一次请求经过的 5 个数据面环节及其接线状态：

| 环节 | 状态 | 关键问题 |
|---|---|---|
| ① 鉴权 | 🟡 部分可用 | API Key 路径 `combinedAuth` 引用 `api_keys` 表不存在的 `username/role` 列，必然报错，Key 访问 `/api/*` 静默失效 |
| ② 选路 | 🟡 部分可用 | 能选账号/模型，但 `cooldown_until`、`rate_limit_rpm/tpm` 列从不写入 → 冷却、限流逻辑悬空 |
| ③ 协议转换 | 🔴 未接线 | `translator` 包（claude/gemini/openai）已实现但 `RegisterTranslator` 从未被调用 → registry 恒空，非 OpenAI 上游无法翻译 |
| ④ 转发上游 | 🟢 可用 | engine 层转发 + 流式已打通（本次 Cloudflare 405 修复后模型获取亦通） |
| ⑤ 计量落库 | 🔴 未接线 | `usage_stats` 表只被 dashboard 读取、从未 INSERT/UPDATE → Dashboard 恒为零 |

**诊断图（请求链路接线状态）：**

```
[① 鉴权]──🟡──>[② 选路]──🟡──>[③ 协议转换]──🔴──>[④ 转发上游]──🟢──>[⑤ 计量落库]──🔴
```

---

## 二、已实测验证的 4 个关键缺陷（根因 + 证据）

1. **translator 注册失活** — `internal/translator/translator.go` 的 `RegisterTranslator` 全仓零调用，`registry` 恒为空；`GetTranslator` 永远返回 nil。Claude/Gemini 上游即使选出，请求体也不被翻译。
2. **combinedAuth SQL Bug** — `cmd/server/main.go` 中 `SELECT user_id, username, role FROM api_keys WHERE key_hash = ?`，但 `api_keys` 实际列仅 `(user_id, name, key_hash, key_prefix, scopes, enabled, last_used, created_at)`，无 `username/role` → 查询必报错，API Key 鉴权静默失败。
3. **usage_stats 恒为零** — `internal/db/db.go` 定义 `usage_stats` 表，仅 `dashboard_handler` 读取，全仓无 INSERT/UPDATE；真实 token 用量不上报。
4. **cooldown / 限流不生效** — `accounts` 表有 `cooldown_until`、`rate_limit_rpm`、`rate_limit_tpm` 列，但全仓无对应 UPDATE；失败冷却、RPM/TPM 限流均不触发。

---

## 三、下一步开发方案（按优先级）

### P0 — 必须立即修，否则上层能力全部悬空

#### P0-1 接线 translator（激活协议转换层）
- **改动点**：在 `internal/translator` 初始化处（建议 `cmd/server/main.go` 或 `internal/engine` 初始化）调用 `RegisterTranslator("claude", &ClaudeTranslator{})`、`RegisterTranslator("gemini", &GeminiTranslator{})`、`RegisterTranslator("openai", &OpenAITranslator{})`。
- **补齐能力**（对齐 9Router）：
  - `claude.go`/`gemini.go` 现状仅做非流式纯文本（system 抽取、role 合并、纯文本拼接），需补：**tool_calls 转换、thinking 块、多模态 content、流式翻译器（带状态机）**。
  - 9Router 关键经验：`transport.format` 字段区分协议；**流式翻译器必须带状态机**（避免分片把 `tool_calls` 参数切散）；错误规则表「文本优先 + fail-open」。
- **验收**：用 Claude/Gemini 原生格式账号走通一次非流式 + 一次流式对话，响应被正确翻译回 OpenAI 形状。

#### P0-2 修复 combinedAuth SQL Bug
- **改动点**：`cmd/server/main.go` 的 `combinedAuth`：
  - 方案 A（最小改动）：`SELECT user_id, name FROM api_keys WHERE key_hash = ?`，role 由 `user_id JOIN users` 取得或在 `scopes` 字段派生。
  - 方案 B（推荐）：`SELECT k.user_id, u.username, u.role FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash = ?`。
- **验收**：用 API Key 访问 `/api/*` 接口返回 200 且身份正确（之前静默 401/500）。

### P1 — 让网关「真正可观测、可自愈」

#### P1-1 真实 token 计量落库
- **改动点**：在 engine 转发拿到上游 `usage`（prompt_tokens / completion_tokens）后：
  - 写 `request_logs`（明细：provider/account/model/耗时/tokens/状态）；
  - 写 `usage_stats`（按日聚合：`INSERT ... ON CONFLICT(user_id, model, date) DO UPDATE SET ...`）。
- **验收**：Dashboard 用量曲线非零；单模型日消耗可下钻。

#### P1-2 失败冷却 + 账号×模型粒度熔断 + 指数退避
- **对齐 9Router**：熔断粒度 = `(账号 × 模型)`，而非整账号。实现：在 `accounts` 表平铺 `model_lock_{model}` 字段存过期时间（或独立 `account_model_locks` 表）。
- **改动点**：选路/转发层捕获 429/5xx/超时 → `UPDATE accounts SET cooldown_until = ? WHERE id = ?`（仅锁该 model），重试走 combo→account→request 三层嵌套 fallback，`excludeSet` 控制重试边界，退避用指数（如 1s→2s→4s）。
- **验收**：单模型 429 只锁该模型，同账号其他模型仍可用（见下图对比）。

#### 熔断粒度对比图
```
账号级熔断（现状/朴素）：  [account-A] ── opus 429 ──> 整号变红 ❌ haiku/sonnet 也不可用
账号×模型级熔断（目标/9Router）：[account-A] ── opus 429 ──> 仅 opus 红 🔒；haiku✅ sonnet✅
```

### P2 — 能力扩展与稳定性

#### P2-1 RPM/TPM 限流执行
- **改动点**：选路前读 `rate_limit_rpm/tpm`，用令牌桶/滑动窗口判定；超限返回 429（带 `Retry-After`）。需在 P1-2 冷却写入打通后接上。

#### P2-2 协议扩展（原生入口）
- 暴露 Claude `/v1/messages`、Gemini `/v1beta/models/...:generateContent` 原生入口；通过 `transport.format` 路由到对应 translator，实现 OpenAI 中枢双跳协议翻译（9Router 经验）。

### P3 — 安全与可观测性

#### P3-1 强加密（AES-GCM）
- 现状密钥存 base64 + 前 N 字节 XOR，不安全。改用 AES-GCM 封装 `ENCRYPTION_KEY`，覆盖 api_key / 账号 secret 落库。

#### P3-2 可观测性（四段式 requestDetails trace）
- 沿 9Router 思路，对每请求记录四段：`auth → route → translate → upstream`，输出结构化 `requestDetails`（含耗时、选中账号/模型、翻译前后差异、上游错误），便于排障。

---

## 四、里程碑排期建议（按风险/收益）

| 里程碑 | 范围 | 交付物 | 风险 |
|---|---|---|---|
| M0（本周） | P0-1 + P0-2 | 协议转换激活 + Key 鉴权修复 | 低，纯接线/SQL |
| M1 | P1-1 + P1-2 | 用量落库 + 账号×模型熔断 | 中，需改选路/重试 |
| M2 | P2-1 + P2-2 | 限流执行 + 原生协议入口 | 中 |
| M3 | P3-1 + P3-2 | AES-GCM + 四段 trace | 低，独立模块 |

---

## 五、给工程的提醒（来自 9Router 研究）

- **被动熔断 > 主动探测**：9Router 靠真实失败触发熔断（fail-open），不依赖心跳探活，更简单也更稳。
- **流式翻译器必须有状态机**：这是 Claude/Gemini 流式最容易翻车的地方，优先级拉满。
- **错误分类文本优先**：先匹配错误文本再匹配状态码，避免误杀可重试错误。
- **粘性轮询（sticky round-robin）**：同会话尽量落同一账号，降低冷启动与配额抖动。

> 本次已修复：Cloudflare 模型获取 405（补 `SubstituteTemplate` 替换 `{accountId}` + 专用 `/models/search?task=Text+Generation`），26 个模型已入库。下一步从 P0 接线开始即可让现有控制面价值兑现。

---

## 六、实施进度（实时更新）

### ✅ P0-1 协议转换层接线（已完成，2026-08-02）

**改了什么**
- `internal/translator/translator.go`：新增 `StreamTranslator` 接口（`TranslateChunk`）与 `Translator.NewStream()`；`init()` 中注册 `anthropic`/`claude`→ClaudeTranslator、`gemini`→GeminiTranslator、`openai`→OpenAITranslator。此前 `registry` 恒空，所有请求走原始透传。
- `internal/models/models.go`：新增 `ToolCallDelta` 类型、`Delta.ToolCalls` 字段；`Delta.MarshalJSON` 输出 `tool_calls`；`StreamChunk` 增加 `Usage` 字段（流式用量落在 finish chunk）。
- `internal/translator/claude.go`（重写）：多模态（image_url→image block，data: URL 解码为 base64）、assistant `tool_calls`→`tool_use`、`tool` 消息→`tool_result`、思考流（thinking→reasoning_content）、**带状态机的流式翻译器**（text/thinking/tool_use 增量正确翻译，tool_use 的 arguments JSON 分片正确拼接）。
- `internal/translator/gemini.go`（重写）：多模态（image_url→`inline_data`）、`functionCall`→`tool_calls`、流式逐 chunk 翻译（文本/functionCall/finish+usage）。
- `internal/router/engine.go`：`resolveChatURL` 现按 api_type 构造 Gemini `:generateContent` / `:streamGenerateContent?alt=sse`；为 Anthropic 补 `x-api-key`、Gemini 补 `x-goog-api-key` 鉴权头；流式路径改用 `streamTransl.TranslateChunk`（此前错误地把原始 SSE 喂给 `TranslateResponse`，Claude/Gemini 流式永不可达）。

**关键修复点**：OpenAI `tool_calls[].function.arguments` 是 JSON **字符串**，翻译给 Anthropic `input` / Gemini `args` 时必须解码为 JSON **对象**（否则上游收到字符串而非对象）。

**验证**：`go build ./...` OK；`go vet` 无告警；`go test ./internal/translator/ ./internal/models/ ./internal/router/` 全过（含 Claude/Gemini 请求/响应/流式状态机用例）；服务端启动通过 init 注册与 DB 初始化无 panic。

**剩余接线项**
- ~~P0-2 `combinedAuth` SQL Bug~~ ✅ **已完成**（见下方 P0-2 进度）。
- P1-1/P1-2/P2/P3 见前文。

---

## 七、实施进度（P0-2：`combinedAuth` SQL Bug 修复）

**根因**：`cmd/server/main.go` 的 `combinedAuth` 用 `SELECT user_id, username, role FROM api_keys WHERE key_hash = ?` 查 API Key 所属用户。但 `api_keys` 表只有 `user_id` 列，`username`/`role` 属于 `users` 表 → 该查询必然报「no such column」错误 → `QueryRow().Scan()` 返回 err → `err == nil` 不成立 → 静默 fall through → API Key 访问 `/api/*` 全部 401 失效。JWT Bearer 路径不受影响，故问题长期被掩盖。

**修复**：
- 抽出 `resolveAPIKeyUser(db, keyHash) (userID, username, role, ok)` helper，查询改为与 `internal/auth/middleware.go:86` 一致的正确写法：
  `SELECT k.user_id, u.username, u.role FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash = ? AND k.enabled = 1`。
- `combinedAuth` 的 Bearer(`ark_` 前缀) 与 `x-api-key` 两个分支统一调用该 helper，消除重复。

**验证**：`go build ./...` OK；新增 `cmd/server/main_test.go`（真实 SQLite 临时库），覆盖「正常命中 / 哈希不存在 / 密钥被禁用」三态，`go test ./cmd/server/` 通过；`go test ./...` 全量无回归。

**影响**：API Key 现在可正常访问所有 `/api/*` 受保护端点（含 admin 角色的 `RequireRole` 鉴权，因 `role` 字段现已正确取回）。

---

## 八、实施进度（P1-1：真实 token 计量落库）

**根因**：Dashboard 始终为零，源于两处：
1. `usage_stats` 聚合表全仓从未被 INSERT/UPDATE —— `dashboard_handler.GetStats` 直接 SUM 该表，无数据则恒为零。
2. `engine.go` 的 `logRequest` 把**请求/响应字节数**塞进了 `request_logs` 的 `request_tokens`/`response_tokens` 列（列名是 token 实际存的是字节），且流式路径根本未捕获 usage。

**修复**：
- `router/engine.go`：
  - `logRequest` 签名改为 `requestTokens, responseTokens int`，非流式成功路径传 `chatResp.Usage.PromptTokens/CompletionTokens`，流式成功路径从结尾 usage chunk 捕获 `finalUsage`，错误路径传 `0, 0`。
  - 新增 `recordUsageStats(db, userID, providerID, model, pricingType, promptTokens, completionTokens)`：按「日 × 用户 × 服务商 × 模型」事务 upsert 进 `usage_stats`（`request_count + 1`、`total_tokens + N`、`estimated_cost + 估算`），Dashboard 直接 SUM 即可显示真实用量。
  - 新增 `estimateCost(total, pricingType)`：三级 fallback 占位（free/free_trial→0；paid→$2/1M tokens，与 dashboard cost_savings 订阅层费率一致）。真实分级定价属后续阶段。
  - 流式路径暂存 `finalUsage`（来自 OpenAI finish chunk / Gemini usageMetadata / Claude message_delta），成功时写入。
  - 删除冗余的 `totalRespBytes` 字节计数（已被 token 计量取代）。
- `router/fallback.go`：`ProviderConfig` 新增 `PricingType` 字段（取自 `providerRow.PricingType`），引擎无需额外查库即可算成本。
- `translator/claude.go`：修复**流式 prompt token 丢失**——Anthropic 在 `message_start` 的 `usage.input_tokens` 给出 prompt token，结尾 `message_delta` 只给 `output_tokens`；`claudeStreamTranslator` 现暂存 `promptTokens` 并在 `message_delta` 合并（含 `TotalTokens`）。非流式早已正确。

**验证**：`go build ./...` OK；`go vet` 无告警；新增 `engine_metering_test.go`（upsert 聚合 / free 成本为零 / 不同 model 分行 / 零 token 跳过）与 `claude_stream_test.go`（流式 prompt token 合并）；`go test ./...` 全量通过。

**影响**：`request_logs` 现记录真实 token 数；`usage_stats` 在每次成功请求后聚合，Dashboard「今日/本月请求数、token、成本、各服务商用量、成本节省」全部显示真实数据。

**下一步**
- P1-2：失败冷却 + 账号×模型粒度熔断（见第九章，已完成）。
- P2-1：RPM/TPM 限流执行（`accounts` 表有 `rate_limit_rpm/tpm` 从不执行）。

---

## 九、实施进度（P1-2：失败冷却 + 账号×模型粒度熔断）

**根因**：`roundRobinSelect` 早就有账户级冷却过滤（检查 `Account.CooldownUntil`），但全仓从不写入 `cooldown_until`/模型级状态 → 过滤器是死代码，任何上游 429/5xx/超时都会被一遍遍重试，既浪费配额又拖垮延迟；且故障是账号级「一损俱损」，一个模型挂了整号所有模型都被拖下水。

**修复**（对应 9Router 的 account×model circuit breaker + fail-open）：
- **新增 `router/breaker.go`**：`CircuitBreaker` 以内存 map 为主（`[accountID][model] -> {until, failures}`，model 空串 `""` 表示整账号级），DB 为辅。
  - `RecordFailure(accountID, model, statusCode)` 按状态码分级：
    - `429/500/502/503/504/0`(传输错误) → **账号×模型级**冷却（仅该模型，同号其他模型仍可用）；
    - `401/403` → **整账号级**冷却（鉴权失效，写 `accounts.cooldown_until`）；
    - 其他 4xx（400/404/413）→ **不熔断**（换账号也救不了，避免误杀健康凭证）。
  - 指数退避：`base 30s × 2^(n-1)`，封顶 5min；**迭代实现避免大 n 时 int64 溢出**。
  - `RecordSuccess` 某模型命中成功后**重置其连失败计数**（证明已恢复健康），并落库删除。
  - `Load()` 进程启动时载入未过期记录，熔断**跨重启持续生效**。
  - **fail-open**：`roundRobinSelect` 两轮轮询，全部冷却时仍放行第一个被跳过的账户，优先可用性而非整体拒绝。
- **`db.go`**：新增 `account_model_cooldowns` 表（`account_id, model, cooldown_until, consecutive_failures`，主键 `(account_id, model)`，幂等 CREATE）。
- **`engine.go`**：`RouteEngine` 加 `breaker` 字段，`NewRouteEngine` 初始化并 `Load()`；两处 `roundRobinSelect` 调用加 `skipIf` 谓词（`IsCooling(acct, model) || IsAccountCooling(acct)`）；非流式/流式所有失败路径（`ProxyRequest`/`ProxyStreamRequest` 错误、HTTP≥400）接 `RecordFailure`，成功路径接 `RecordSuccess`。
- **`balancer.go`**：`roundRobinSelect` 加 `skipIf func(models.Account) bool` 参数并实现 fail-open 双轮询；`stickySelect` 内部调用同步。

**验证**：`go build ./...` OK；`go vet` 无告警；新增 `router/breaker_test.go`（退避封顶/溢出、模型级冷却且同号其他模型不受影响、401 整账号冷却屏蔽所有模型、4xx 不熔断、过期清理、传输错误冷却、落库+Load 恢复、fail-open 放行）；`go test ./...` 全量通过。

**影响**：上游单模型故障只冷却该模型（其余模型/账号照常），整账号鉴权失效则整号冷却；失败时指数退避不再反复猛砸挂掉的上游；进程重启后熔断状态保留。

**下一步**
- P2-1：RPM/TPM 限流执行（见第十章，已完成）。

---

## 十、实施进度（P2-1：RPM/TPM 限流执行）

**根因**（之前诊断的缺陷之一）：`accounts` 表有 `rate_limit_rpm`/`rate_limit_tpm` 列，`models.Account` 也读取了这两个字段，但选路时**从不执行限流**——`roundRobinSelect` 只要账户 `enabled` 就一律选中，配额形同虚设。重度用户会把单账户打爆触发上游 429，反而又触发 P1-2 的熔断冷却。

**修复**（与 P1-2 熔断共用 `skipIf` 跳过机制）：
- **新增 `router/ratelimit.go`**：`RateLimiter` 按账户维护「固定窗口 + 内存计数」（每账户一个按分钟滚动的窗口，累计 `rpm` 请求数 + `tpm` token 数）。
  - `Exceeded(accountID, rpmLimit, tpmLimit) bool`：**只读**检查当前是否超限（某维度 `limit<=0` 表示该维度不限流）。必须在 `skipIf` 谓词里只读调用，因为谓词会对每个候选账户求值，只有最终选中的才应被计入。
  - `RecordRequest(accountID)`：实际转发前累加请求计数（`accountID==0` 免费服务商静默忽略）。
  - `RecordTokens(accountID, tokens)`：成功响应后累加 token 消耗（用于 TPM）。
  - 窗口过期（≥1 分钟）自动重置。
- **`engine.go`**：
  - `RouteEngine` 加 `rateLimiter *RateLimiter` 字段，`NewRouteEngine` 初始化 `NewRateLimiter()`。
  - 两处 `roundRobinSelect` 的 `skipIf` 谓词并入 `e.rateLimiter.Exceeded(a.ID, a.RateLimitRPM, a.RateLimitTPM)`。
  - 选中账户后调用 `RecordRequest(acct.ID)`；非流式/流式成功路径调用 `RecordTokens(acct.ID, totalTokens)`。
- **设计取舍**：限流是瞬时控制面，按分钟窗口自然过期，**无需跨重启持久化**（与熔断的冷却不同）；纯内存 map，热路径零查库。

**验证**：`go build ./...` OK；`go vet` 无告警；新增 `router/ratelimit_test.go`（无配置不限流 / RPM 超限 / TPM 超限 / 窗口过期重置 / 免费账户零号静默忽略）；`go test ./...` 全量通过。

**影响**：配置了 `rate_limit_rpm`/`rate_limit_tpm` 的账户在超额时会被选路自动跳过，把流量导向同服务商下其他健康账户，从源头避免单账户被打爆触发 429→熔断连锁。

**下一步**
- P2-2：协议扩展（Claude `/v1/messages`、Gemini `/v1beta` 原生入口）——当前已是「OpenAI 中枢 + translator 双跳」架构，扩展点清晰。
- P3-1：强加密（AES-GCM 替换现有 base64 前 N 字节 XOR）。
- P3-2：可观测性（四段式 requestDetails trace）。

---

## 十一、实施进度（P2-2：协议扩展 / 原生入口）

**根因**：本网关当前是「OpenAI 中枢 + translator 双跳」架构——所有上游流量都先归一到 OpenAI `ChatRequest` 形状，转发后再由 `translator` 翻成上游原生请求；响应同理反向翻译。这套架构对非 OpenAI SDK 友好，但**无法让 Claude Code / Anthropic SDK、Gemini SDK 直接以原生协议接入**（它们发的是 `/v1/messages`、`/v1beta/models/{model}:generateContent`，不是 `/v1/chat/completions`）。之前研究把「原生入口」列为缺陷之一（③ 协议转换虽打通但只有 OpenAI 入口）。

**修复**（新增「逆向映射」层，与原正向 translator 互为逆操作）：

- **新增 `translator/claude_reverse.go`（Anthropic 原生入口）**：
  - `ParseClaudeRequest(body)`：Anthropic `messages` 请求体 → `models.ChatRequest`，喂给已有路由引擎。覆盖：system 抽取、content block 数组（text / image→`image_url` 多模态 / tool_use→`tool_calls` / tool_result→`role=tool` / thinking→`ReasoningContent`）。
  - `SerializeClaudeResponse(resp)`：OpenAI `ChatResponse` → Anthropic 响应体，content block 严格按 **thinking → text → tool_use** 顺序（Anthropic SDK 要求 tool_use 在文本之后，否则解析报错）。
  - `NewClaudeStreamEmitter()`：OpenAI `StreamChunk` → Anthropic SSE 事件序列（`message_start` / `content_block_start`+`delta`+`stop` / `message_delta` / `message_stop`），`event:`+`data:` 双行协议，带状态机防 tool_calls 参数被分片切散；`delta==nil` 有兜底空 `Delta{}`（finish 分片无 delta）。
  - 导出 `StreamEvent{Type, Data}`：`Type==""` 仅发 `data:` 行，否则发 `event:`+`data:`。

- **新增 `translator/gemini_reverse.go`（Gemini 原生入口）**：
  - `ParseGeminiRequest(body, model)`：model 取自 URL 路径（空则报错，因 Gemini 请求体无 model 字段）；映射 systemInstruction、contents（role 翻转 model→assistant）、functionCall→`tool_calls`、functionResponse→`role=tool`。
  - `SerializeGeminiResponse(resp)`：→ Gemini `GenerateContentResponse`（candidates + usageMetadata）。
  - `NewGeminiStreamEmitter()`：OpenAI `StreamChunk` → 自包含的 `GenerateContentResponse` 分片，仅 `data:` 行（无 `event:` 前缀）；工具调用参数按字符串拼接、结束分片再解析为对象；同样补 `delta==nil` 兜底（修复 finish 分片 nil panic）。

- **新增 `handlers/native_handler.go`**：
  - `loadRouteSettings(db)`：读取 caveman/ponytail/rtk 选路设置，与 `ChatCompletions` 一致。
  - `MessagesNative(c)`：Claude 入口 → `ParseClaudeRequest` → `Route`/`StreamRoute` → `SerializeClaudeResponse`（非流式）或 `NewClaudeStreamEmitter`（流式，`event:`+`data:`）。
  - `GeminiNative(c)`：从 `c.Param("model")` 取 model，剥离尾随 `:action`（`generateContent`/`streamGenerateContent`/`countTokens`），→ `ParseGeminiRequest` → `Route` → `SerializeGeminiResponse`/`NewGeminiStreamEmitter`。流式响应头设 `X-Accel-Buffering: no`，用 `http.Flusher` 逐片 flush。

- **`cmd/server/main.go` 路由接线**：
  - `/v1` 组下加 `openai.POST("/messages", chatHandler.MessagesNative)`（Anthropic 兼容入口挂在 OpenAI 版组下，复用同一 API Key 鉴权）。
  - 新增 `gemini := r.Group("/v1beta")` 组，挂 `auth.APIKeyAuthMiddleware`，`gemini.Any("/models/*action", chatHandler.GeminiNative)`（`.Any` 覆盖 GET/POST，兼容 generateContent 与 streamGenerateContent；`*action` 通配 `models/{model}:generateContent` 整段）。

**验证**：`go build ./...` OK；`go vet` 无告警；新增 `translator/translator_reverse_test.go`（`ParseClaudeRequest` 文本/工具、多模态 image_url、`SerializeClaudeResponse` 块顺序、`ClaudeStreamEmitter`、`ParseGeminiRequest`、`SerializeGeminiResponse`、`GeminiStreamEmitter`）；`go test ./...` 全量通过。
> 注：测试期发现两处 bug 并已修复——(1) `TestParseClaudeRequestText` 断言误用 `MessageContent.String()`（该方法会丢弃 image part 只留文本），改为断言原始 JSON 含 `data:image/png;base64,AAA`；(2) `geminiStreamEmitter.Emit` 在 finish 无 delta 分片上 nil panic，补 `delta==nil` 兜底（与 claude 侧一致）。

**影响**：网关现在同时支持三类协议入口——OpenAI `/v1/chat/completions`、Anthropic `/v1/messages`、Gemini `/v1beta`。Claude Code / Gemini SDK 可零改造直连，控制台统一以 OpenAI 中枢做选路/熔断/限流/计量。

**下一步**
- P3-1：强加密（AES-GCM 替换现有 base64 前 N 字节 XOR）。
- P3-2：可观测性（四段式 requestDetails trace）。

---

## 十二、实施进度（P3-1：强加密 AES-GCM）

**根因**：落库密钥（`accounts.api_key_encrypted`、用户 API Key `api_keys.key_encrypted`）此前仅做 `base64` 编码后对**前 N 字节** XOR（N = `len(EncryptionKey)`），既未覆盖整段明文（长密钥后缀以明文 base64 形态存储），也无完整性/认证校验，等同于弱明文存储。

**修复**：
- **新增 `internal/crypto/crypto.go`**（集中加解密，消除 4 处重复实现）：
  - `Encrypt(plaintext, masterKey)`：SHA-256 派生 32 字节密钥 → AES-256-GCM；随机 12 字节 nonce；输出 `gcm.` 前缀 + `base64(nonce || ciphertext || tag)`。整段加密 + 认证标签。
  - `Decrypt(ciphertext, masterKey)`：带 `gcm.` 前缀走 GCM；否则回退旧版 `base64 + XOR`（兼容存量数据）。解密失败返回原串（与旧行为一致）。
  - `IsGCMFormat` 供迁移判重。
- **新增 `internal/db/migrate.go`**：`MigrateEncryption(db, masterKey)` 把 `accounts.api_key_encrypted` 与 `api_keys.key_encrypted` 中仍为旧格式的行**就地升级**为 GCM；幂等（已带前缀跳过）；列不存在（老库）则跳过该列而非报错；解密失败行跳过避免破坏数据。
- **重构 4 处调用点**统一委托 `crypto` 包：`engine.go` `decryptAPIKey`、`auth_handler.go` `encryptKey/decryptKey`、`account_handler.go` `encryptAPIKey/decryptAPIKey`、`provider_handler.go` `decryptAPIKey`（均移除内联 XOR + `encoding/base64` 依赖）。
- **`cmd/server/main.go` 接线**：`RunMigrations` 之后调用 `db.MigrateEncryption`，**非致命**（失败仅告警，双格式回退仍可读取旧密文）。

**验证**：`go build ./...` OK；`go vet` 无告警；新增 `crypto/crypto_test.go`（往返 / 旧格式解密 / 错误密钥失败 / 前缀检测 / 空密钥 / nonce 随机）与 `db/migrate_test.go`（升级 2 行 + 幂等 + 缺失列容忍）；`go test ./...` 全量通过。

**影响**：新写入的密钥全部为 AES-256-GCM（整段加密 + 认证）；存量弱密文在首次启动时被就地升级；即便迁移未执行，旧密文仍可经兼容解密读取，部署零中断。

---

## 十三、实施进度（P3-2：可观测性四段式 `requestDetails`）

**根因**：此前排障只能看到 `request_logs` 的最终结果（模型 / token / 总耗时 / 状态），无法回答「慢在哪一段」「选中了哪个账号」「翻译是否发生、前后体积变化」「失败前尝试过哪些服务商」。一次跨服务商回退的请求，在日志里与一次直达成功的请求毫无区别。

**实现**：

1. **新增 `internal/trace` 包**（`trace.go`）
   - `RequestTrace`：`request_id`（纳秒时间戳 + 进程内自增，便于日志关联）、`user_id`、`model`、起止时间、`segments`（四段）、`attempts`（回退尝试列表）。
   - 经 `context.Context` 透传：`WithTrace` / `FromContext`；**`FromContext` 缺失时返回 `nil`**，所有调用点判空后可无损降级，不影响未接线路径。
   - API：`StartSegment`（幂等，不重置起点）/ `EndSegment`（`detail` 传 `nil` 时保留既有 detail）/ `RecordSegment`（显式耗时，用于「翻译 = 请求翻译 + 响应翻译之和」「流式 upstream = 整段流耗时」这类无法由单一起止点表达的段）/ `SetDetail` / `AddAttempt` / `RecordModel` / `JSON` / `Log`。
   - 全部方法持 `sync.Mutex`，因流式转发与主流程可能并发写入。

2. **引擎接线（`internal/router/engine.go`）**
   - `Route` / `StreamRoute` 入口开启 `route` 段；`tryProviders` / `streamProviders` 记录 `RecordModel`。
   - `route` 段 detail：选中的 `provider_id/provider_name/account_id/model/api_type`。
   - `translate` 段：请求翻译 + 响应翻译耗时之和，detail 含 `translated`（是否真的发生翻译）、`api_type`、`req_in/out_bytes`、`resp_in/out_bytes` —— 即「翻译前后差异」。
   - `upstream` 段：`status_code`、`response_bytes`；流式改记 `chunks` + `sse_bytes`（此前恒为 `0`，无任何信息量）。
   - 每次失败的服务商写入 `AddAttempt`（服务商、账号、模型、协议、状态码、错误、耗时），使回退过程完全可见。
   - 全部服务商失败时以 `error` 状态定稿三段，避免 trace 半途而废。

3. **落库与日志**
   - `request_logs` 新增 `request_details TEXT` 列（`CREATE TABLE` + 老库 `ALTER TABLE` 双路径）。
   - `logRequest` 增加 `ctx` 参数，从 `trace.FromContext(ctx)` 取 JSON 写入该列。
   - 请求结束 `defer tr.Log()` 输出 `[requestDetails] {...}` 结构化日志行，便于 grep 排障。

4. **入口接线**：`chat_handler.go` 的 `ChatCompletions`、`native_handler.go` 的 `MessagesNative` / `GeminiNative` 均创建 trace、记录 `auth` 段（耗时取自 `combinedAuth` 中间件写入的 `req_start`，detail 含 `user_id/role/username`，原生入口额外标 `entry`），再经 ctx 透传给引擎。

**顺带修复的两个真实缺陷**（端到端联调中暴露）：

- **注册接口完全不可用**：`auth_handler.go` 的 `INSERT INTO users (username, email, password_hash, role) VALUES (?,?,?,?)` 有 4 个占位符却只传 3 个实参（漏 `role`），运行时必然报错；而 `err != nil` 分支**一律**返回「用户名或邮箱已存在」，把参数缺失伪装成 409，长期无人察觉。修复：补传 `role`；新增 `isUniqueViolation`，仅唯一约束冲突返回 409，其余错误落日志并返回 500。另写脚本全量扫描 98 处字面量 SQL 调用的占位符/实参数量，确认再无同类问题。
- **Claude 服务商端点补全错误**：`resolveChatURL` 对 `anthropic`/`claude` 也追加 `/chat/completions`，而载荷已被 translator 转成 Anthropic Messages 格式 —— 只填 base（如 `https://api.anthropic.com/v1`）的服务商必然 404。种子数据用完整 `/v1/messages` 绕过了它，用户自建服务商则直接踩雷。修复：该协议改补 `/messages`，并新增 `engine_url_test.go` 表驱动锁定 openai / claude / anthropic / gemini（流式与非流式）/ Azure 带参 / 空值共 12 种情形。

**验证**：
- `go build ./...`、`go vet ./...` 均干净；`gofmt` 已对本阶段涉及文件统一格式化（期间发现脚本改写把 `engine.go` 换行符变成 CRLF，已还原为 LF 与项目一致）。
- 新增 `trace/trace_test.go` 12 例（上下文往返、`request_id` 唯一性、段计时、幂等开段、未开段直接结束、`detail` 传 nil 保留、覆盖写、attempts、JSON 形状、`Log` 设置 `ended_at`、8 goroutine × 50 轮并发写）；`engine_url_test.go` 12 例。`go test ./...` 全量通过。
- 端到端：起本地 mock 上游（同时模拟 `/v1/messages` 与 `/v1/chat/completions`），配置「必失败服务商 + 可用 claude 服务商」，经网关发 OpenAI 格式请求 → 翻译为 Claude 协议 → 命中 `/messages` → 反向翻译回 OpenAI，token 计量 11/7/18 正确；原生 `/v1/messages` 入口同样正常。日志与 `request_logs.request_details` 中四段齐备，并完整记录了 `dead-provider` 连接被拒的回退尝试。

**影响**：任意一次请求都可回答「卡在哪一段、走了谁、翻译有没有发生、失败前试过谁」，数据同时存在于服务端日志与数据库，可直接用于后续排障面板。附带修复了一个让注册功能完全不可用、以及一个让自建 Claude 服务商必然 404 的线上级缺陷。

**至此 `NEXT_DEV_PLAN.md` 的 P0-1 → P3-2 全部路线项已完成。**

**下一步**
- P3-2：可观测性（沿 9Router 思路，对每请求记录 `auth → route → translate → upstream` 四段 `requestDetails`：含耗时、选中账号/模型、翻译前后差异、上游错误，便于排障）。
