export interface User {
  id: number;
  username: string;
  email: string;
  role: 'admin' | 'member';
  created_at: string;
}

export interface Provider {
  id: number;
  name: string;
  base_url: string;
  api_type: string;
  priority: number;
  health_status?: string;
  health?: 'healthy' | 'degraded' | 'unhealthy';
  enabled?: number;
  status?: 'active' | 'inactive';
  pricing_type?: 'free' | 'free_trial' | 'paid';
  auto_sync?: number; // 是否开启模型自动同步（默认关闭）
  config?: Record<string, unknown>;
}

export interface Account {
  id: number;
  name: string;
  provider_id: number;
  provider_name?: string;
  base_url?: string;
  api_key: string;
  api_key_prefix?: string;
  // extra_config 存储服务商私有配置（JSON 字符串），如 Cloudflare 的 accountId。
  // 用于替换 base_url 模板中的命名占位符（如 {accountId}）。
  extra_config?: string;
  rate_limit_rpm?: number;
  rate_limit_tpm?: number;
  enabled?: number;
  status?: 'active' | 'inactive';
}

export interface Combo {
  id: number;
  name: string;
  config: ComboConfig;
}

export type ComboStrategy = 'auto' | 'fallback' | 'round_robin';

// 模型能力标签：用于组合的智能路由（auto 策略）
export type ModelCapability =
  | 'auto'          // 由模型名自动推断
  | 'text'          // 通用文本
  | 'vision'        // 图片理解
  | 'code'          // 代码生成/调试
  | 'long_context'  // 超长上下文
  | 'audio'         // 语音输入
  | 'reasoning';    // 深度推理

export interface ComboConfig {
  models: ComboModel[];
  strategy?: ComboStrategy;
  // 向后兼容：旧格式 { model_mapping: "xxx" }
  model_mapping?: string;
}

export interface ComboModel {
  id: string;       // 模型 ID（如 "gpt-4o" 或 "provider/model-id"）
  provider_id?: number; // 所属服务商 ID，用于精确路由（避免同名模型跨服务商歧义）
  capability?: ModelCapability; // 在组合中承担的角色，留空/auto = 自动推断
}

export interface ProviderModel {
  model_id: string;
  display_name: string;
  owned_by?: string;
  provider_id?: number;
  enabled?: number;
  capabilities?: string[]; // 后端启发式推断 + 真实探测合并后的能力列表
  probed_at?: string; // 非空表示做过真实多模态探测，前端据此区分「实测」与「按名猜测」
}

export interface RequestLog {
  id: number;
  request_id: string;
  model: string;
  picked_model?: string;
  status: number;
  latency_ms: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost: number;
  created_at: string;
  provider_name?: string;
  error_message?: string;
  request_body?: string;
  response_body?: string;
  request_details?: string; // JSON: RequestTrace
}

export interface SegmentDetail {
  provider_id?: number;
  provider_name?: string;
  account_id?: number;
  model?: string;
  api_type?: string;
  picked_model?: string;
  translated?: boolean;
  req_in_bytes?: number;
  req_out_bytes?: number;
  resp_in_bytes?: number;
  resp_out_bytes?: number;
  status_code?: number;
  response_bytes?: number;
  chunks?: number;
  sse_bytes?: number;
  [key: string]: unknown;
}

export interface Segment {
  name: string;
  started_at: string;
  duration_ms: number;
  status: 'ok' | 'error' | 'skip';
  detail?: SegmentDetail;
}

export interface Attempt {
  provider_id: number;
  provider_name: string;
  account_id: number;
  model: string;
  api_type: string;
  status_code: number;
  status: string;
  error?: string;
  latency_ms: number;
}

export interface RequestTrace {
  request_id: string;
  user_id: number;
  model: string;
  started_at: string;
  ended_at?: string;
  segments: Record<string, Segment>;
  attempts?: Attempt[];
}

export interface DashboardStats {
  today_requests: number;
  today_tokens: number;
  today_cost: number;
  tier_distribution: { name: string; value: number }[];
  provider_breakdown: { provider: string; requests: number; tokens: number; cost: number }[];
  cost_savings: { saved: number; percentage: number };
}

export interface Setting {
  key: string;
  value: string | number | boolean;
  type: string;
  description?: string;
}

export interface SettingsMap {
  [key: string]: string | number | boolean;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
}
