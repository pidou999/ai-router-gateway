# AI Router Gateway

多协议 AI 网关：统一 OpenAI / Anthropic Claude / Google Gemini 入口，支持智能选路、失败熔断、限流、计量与结构化追踪。

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.25 + Gin + SQLite（modernc，纯 Go） |
| 前端 | React 19 + TypeScript + Vite 8 + Tailwind CSS 4 + Recharts |

## 快速开始

### 环境要求

- Go 1.25+
- Node.js 18+
- 必须设置环境变量：
  - `JWT_SECRET` — JWT 签名密钥（任意字符串，建议 ≥ 32 字符）
  - `ENCRYPTION_KEY` — AES-GCM 加密主密钥（必须 ≥ 32 字节随机字符串）

```bash
# 启动后端（默认 :5176）
cd backend
go run ./cmd/server

# 另开终端，启动前端开发服务器（默认 :5137）
cd frontend
npm run dev
```

### 构建生产版本

```bash
# 前端
cd frontend
npm run build    # 产物输出到 frontend/dist/

# 后端
cd backend
go build -o bin/gwserver ./cmd/server
```

## 核心功能

### 协议中枢
- **OpenAI 兼容** `/v1/chat/completions` + 流式 SSE
- **Anthropic 原生** `/v1/messages`（解析 Claude 请求格式）
- **Gemini 原生** `/v1beta/models/{model}:generateContent`

### 智能选路
- 多级组合（Combo）：按优先级/权重/回退策略组合多个账号×模型
- 粘性轮询：同会话尽量落同一账号
- 自动降级：健康检查过滤，故障账号自动跳过

### 熔断与限流
- **账号×模型粒度熔断**：单模型 429 只锁该模型，同账号其他模型不受影响
- 指数退避重试：1s→2s→4s，封顶 5 分钟
- RPM / TPM 限流：按账号配置每分钟请求数与每分钟 Token 数
- fail-open：全部冷却时仍放行，避免整体拒绝

### 计量与观测
- 真实 token 用量落库（`usage_stats` 聚合表 + `request_logs` 明细）
- Dashboard 展示：请求数、token、成本估算、各服务商用量分布
- 四段式 `requestDetails` 追踪：auth → route → translate → upstream，含耗时与失败回退记录

### 安全
- AES-256-GCM 密钥加密（API Key、Secret）
- JWT 认证 + API Key 双轨鉴权
- 存量弱密文自动就地升级（兼容旧数据）

## API 文档

### 鉴权方式

| Header | 说明 |
|--------|------|
| `Authorization: Bearer <jwt>` | 用户登录后的 JWT Token |
| `Authorization: Bearer ark_<key_hash>` | 通过控制台生成的 API Key |
| `X-API-Key: <raw_key>` | 原始 API Key（自动哈希匹配） |

### 主要端点

```
POST /api/auth/login          # 登录，返回 JWT
POST /api/auth/register       # 注册
GET  /api/auth/api-keys       # 列表当前用户 API Key
POST /api/auth/api-keys       # 创建 API Key

GET  /api/providers           # 服务商列表
POST /api/providers           # 新建服务商（admin）
GET  /api/providers/:id/models        # 服务商模型列表
POST /api/providers/:id/models/test   # 测试单个模型

GET  /api/accounts            # 账号列表
POST /api/accounts            # 新建账号（admin）
POST /api/accounts/:id/test   # 测试账号连通性

POST /v1/chat/completions     # OpenAI 兼容接口
POST /v1/messages             # Anthropic 兼容接口
POST /v1beta/models/{m}:generateContent  # Gemini 兼容接口

GET  /api/dashboard/stats     # 用量统计
GET  /api/logs                # 请求日志
```

## 目录结构

```
backend/
  cmd/server/main.go          # 入口：路由注册、中间件、启动
  internal/
    auth/                     # JWT 签发/验证、中间件
    config/                   # 配置加载与校验（含密钥强制检查）
    crypto/                   # AES-GCM 加解密
    db/                       # SQLite 初始化、迁移、种子
    handlers/                 # HTTP 处理器（CRUD + 协议入口 + 探测）
    logger/                   # 结构化日志（slog，带 request_id 上下文）
    models/                   # 数据模型定义
    modes/                    # caveman / ponytail 提示词增强
    proxy/                    # HTTP 代理客户端（连接池配置）
    router/                   # 选路引擎：balancer / breaker / ratelimit / fallback
    trace/                    # 四段式链路追踪
    translator/               # OpenAI ↔ Claude ↔ Gemini 双向翻译

frontend/
  src/
    pages/                    # 各功能页面（Dashboard、Providers、Accounts 等）
    components/               # 布局与公共组件
    api/                      # API 请求封装
```

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `PORT` | 否 | `8080` | 服务监听端口 |
| `DB_PATH` | 否 | `./data/gateway.db` | SQLite 数据库路径 |
| `JWT_SECRET` | **是** | — | JWT 签名密钥（≥ 32 字符） |
| `ENCRYPTION_KEY` | **是** | — | AES-GCM 加密主密钥（≥ 32 字节） |
| `DEFAULT_TIMEOUT` | 否 | `30` | 单次请求默认超时（秒） |

## 备份说明

项目无 Git 历史，发布前请手动备份：
```bash
python scripts/backup.py  # 或手动压缩 backend/ + frontend/src/
```
