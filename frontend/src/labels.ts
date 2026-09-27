export const healthLabels: Record<string, string> = {
  healthy: '正常',
  degraded: '降级',
  unhealthy: '异常',
  unknown: '未知',
};

export const apiTypeLabels: Record<string, string> = {
  openai: 'OpenAI',
  cloudflare: 'Cloudflare (Workers AI)',
  anthropic: 'Anthropic (Claude)',
  claude: 'Claude Code',
  gemini: 'Google Gemini',
  vertex: 'Vertex AI',
  azure: 'Azure OpenAI',
  groq: 'Groq',
  deepseek: 'DeepSeek',
  qwen: '通义千问',
  siliconflow: '硅基流动',
  moonshot: '月之暗面 (Kimi)',
  zhipu: '智谱 GLM',
  glm: '智谱 (z.ai)',
  mistral: 'Mistral',
  openrouter: 'OpenRouter',
  together: 'Together AI',
  fireworks: 'Fireworks AI',
  cerebras: 'Cerebras',
  cohere: 'Cohere',
  perplexity: 'Perplexity',
  xai: 'xAI (Grok)',
  nvidia: 'NVIDIA NIM',
  hyperbolic: 'Hyperbolic',
  nebius: 'Nebius AI',
  venice: 'Venice AI',
  chutes: 'Chutes AI',
  featherless: 'Featherless',
  volcengine: '火山方舟',
  minimax: 'MiniMax',
  alibaba: '阿里云百炼',
  codebuddy: '腾讯 CodeBuddy',
  github: 'GitHub Copilot',
  ollama: 'Ollama (本地)',
  opencode: 'OpenCode',
  kilo: 'Kilo Code',
  custom: '自定义',
};

export const healthDot: Record<string, string> = {
  healthy: 'bg-green-500',
  degraded: 'bg-yellow-500',
  unhealthy: 'bg-red-500',
  unknown: 'bg-gray-300',
};

export const healthBadge: Record<string, string> = {
  healthy: 'bg-green-100 text-green-800',
  degraded: 'bg-yellow-100 text-yellow-800',
  unhealthy: 'bg-red-100 text-red-800',
  unknown: 'bg-gray-100 text-gray-800',
};

export const pricingLabels: Record<string, string> = {
  free: '完全免费',
  free_trial: 'API提供商',
  paid: 'API提供商',
};

export const pricingBadge: Record<string, string> = {
  free: 'bg-emerald-50 text-emerald-700',
  free_trial: 'bg-sky-50 text-sky-700',
  paid: 'bg-amber-50 text-amber-700',
};

// 模型能力徽章：与后端 capabilityStrings 返回的 key 保持一致
export const capabilityLabels: Record<string, string> = {
  text: '文本',
  vision: '视觉',
  code: '代码',
  long_context: '长上下文',
  audio: '语音',
  reasoning: '推理',
};

export const capabilityColors: Record<string, string> = {
  text: 'bg-gray-100 text-gray-600',
  vision: 'bg-purple-100 text-purple-700',
  code: 'bg-blue-100 text-blue-700',
  long_context: 'bg-amber-100 text-amber-700',
  audio: 'bg-pink-100 text-pink-700',
  reasoning: 'bg-emerald-100 text-emerald-700',
};

// 基线能力：对所有模型都成立、不具备区分度的能力（如文本）。
export const BASELINE_CAPABILITIES: string[] = ['text'];

/**
 * 能力徽章样式：对「文本」这类基线能力做弱化，避免淹没差异化标签。
 * - 模型同时具备差异化能力（视觉/代码/推理/长上下文/语音）时，文本标签弱化为淡灰虚框；
 * - 模型仅有文本能力（纯文本模型）时，保留正常灰色，便于一眼识别其定位。
 * 其余能力照常返回彩色样式。
 */
export function capabilityBadgeClass(cap: string, caps: string[] = []): string {
  const base = capabilityColors[cap] || 'bg-gray-100 text-gray-600';
  if (cap === 'text' && Array.isArray(caps) && caps.some((c) => c !== 'text')) {
    return 'bg-transparent text-gray-400 border border-dashed border-gray-300';
  }
  return base;
}

/** 该能力标签是否应弱化显示（用于决定是否加提示说明）。 */
export function isCapabilityMuted(cap: string, caps: string[] = []): boolean {
  return cap === 'text' && Array.isArray(caps) && caps.some((c) => c !== 'text');
}
