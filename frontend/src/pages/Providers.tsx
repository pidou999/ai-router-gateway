import { useState, useEffect, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { Plus, Pencil, Trash2, Activity, Eye, EyeOff, CheckCircle2, Circle, Search, X, RefreshCw } from 'lucide-react';
import api from '../api/client';
import type { Provider, Account } from '../types';
import { healthLabels, apiTypeLabels, healthDot, healthBadge, pricingLabels, pricingBadge } from '../labels';

type CardHandlers = {
  navigate: (path: string) => void;
  onHealthCheck: (id: number) => void;
  onToggle: (p: Provider) => void;
  onAutoSync: (p: Provider) => void;
  onEdit: (p: Provider) => void;
  onDelete: (id: number) => void;
  isAdmin: boolean;
  syncStatus?: Record<number, 'ok' | 'err'>;
};

function ProviderCard({ p, handlers }: { p: Provider; handlers: CardHandlers }) {
  const health = p.health_status || p.health || 'unknown';
  const active = p.enabled === 1;
  const syncStatus = handlers.syncStatus?.[p.id];
  return (
    <div
      key={p.id}
      onClick={() => handlers.navigate(`/providers/${p.id}`)}
      className="group relative bg-white rounded-xl shadow-sm border border-gray-200 hover:shadow-md hover:border-indigo-200 transition-all p-4 flex flex-col cursor-pointer"
    >
      <div className="flex items-start justify-between gap-2 mb-3">
        <div className="flex items-center gap-2 min-w-0">
          <span className={`w-2.5 h-2.5 rounded-full shrink-0 ${healthDot[health] || 'bg-gray-300'}`} />
          <h3 className="font-semibold text-gray-900 truncate" title={p.name}>{p.name}</h3>
        </div>
        <span className={`shrink-0 inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${healthBadge[health] || 'bg-gray-100 text-gray-800'}`}>
          {healthLabels[health] || health}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-2 mb-3">
        <span className="inline-flex px-2 py-0.5 rounded-md bg-indigo-50 text-indigo-700 text-xs font-medium">
          {apiTypeLabels[p.api_type] || p.api_type}
        </span>
        {(p.pricing_type && pricingLabels[p.pricing_type]) && (
          <span className={`inline-flex px-2 py-0.5 rounded-md text-xs font-medium ${pricingBadge[p.pricing_type] || 'bg-gray-100 text-gray-600'}`}>
            {pricingLabels[p.pricing_type]}
          </span>
        )}
        <span className={`inline-flex px-2 py-0.5 rounded-md text-xs font-medium ${active ? 'bg-green-50 text-green-700' : 'bg-gray-100 text-gray-500'}`}>
          {active ? '已启用' : '已停用'}
        </span>
      </div>

      <div className="mb-3">
        <p className="text-xs text-gray-400 mb-1">API 地址</p>
        <p className="text-xs font-mono text-gray-600 break-all line-clamp-2" title={p.base_url}>{p.base_url}</p>
      </div>

      <div className="flex items-center justify-between mt-auto pt-3 border-t border-gray-100">
        <span className="text-xs text-gray-400">
          优先级 <span className="text-gray-700 font-medium">{p.priority}</span>
        </span>
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => handlers.onHealthCheck(p.id)}
            className="p-1.5 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-100 transition-colors"
            title="健康检查"
          >
            <Activity className="w-4 h-4" />
          </button>
          {handlers.isAdmin && (
            <>
              <button
                onClick={() => handlers.onAutoSync(p)}
                className={`p-1.5 rounded-lg hover:bg-gray-100 transition-colors ${
                  syncStatus === 'ok' ? 'text-green-600'
                  : syncStatus === 'err' ? 'text-red-500'
                  : p.auto_sync ? 'text-blue-600 hover:text-blue-700' : 'text-gray-400 hover:text-blue-500'
                }`}
                title={p.auto_sync ? '关闭自动同步' : '启用自动同步（每30分钟从上游拉取模型列表）'}
              >
                <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                  <path d="M13.5 8A5.5 5.5 0 1 1 8 2.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  <path d="M13.5 2.5V8H8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                </svg>
              </button>
              <button
                onClick={() => handlers.onToggle(p)}
                className="p-1.5 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-100 transition-colors"
                title={active ? '停用' : '启用'}
              >
                {active ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
              <button
                onClick={() => handlers.onEdit(p)}
                className="p-1.5 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-100 transition-colors"
                title="编辑"
              >
                <Pencil className="w-4 h-4" />
              </button>
              <button
                onClick={() => handlers.onDelete(p.id)}
                className="p-1.5 text-gray-400 hover:text-red-600 rounded-lg hover:bg-gray-100 transition-colors"
                title="删除"
              >
                <Trash2 className="w-4 h-4" />
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

export default function Providers() {
  const navigate = useNavigate();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [syncing, setSyncing] = useState(false);
  const [syncStatus, setSyncStatus] = useState<Record<number, 'ok' | 'err'>>({});
  const [showModal, setShowModal] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState({ name: '', base_url: '', api_type: 'openai', priority: 0, pricing_type: 'paid', auto_sync: false });
  const [query, setQuery] = useState('');

  const role = localStorage.getItem('role') || 'user';
  const isAdmin = role === 'admin';

  const fetchData = async () => {
    try {
      const [pRes, aRes] = await Promise.all([
        api.get<Provider[]>('/providers'),
        api.get<Account[]>('/accounts'),
      ]);
      setProviders(pRes.data);
      setAccounts(aRes.data);
      setError('');
    } catch {
      setError('加载提供商失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  // 已配置：拥有至少一个「已启用」账户的服务商
  const configuredIds = useMemo(() => {
    const ids = new Set<number>();
    accounts
      .filter((a) => a.enabled !== 0)
      .forEach((a) => ids.add(Number(a.provider_id)));
    return ids;
  }, [accounts]);

  // 搜索过滤：匹配名称、接口类型（原始值与中文标签）、API 地址
  // 在分组之前过滤，使各分组均只展示命中结果
  // 同时过滤掉已禁用的服务商（软删除）
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    // 只显示启用的服务商
    const enabledProviders = providers.filter((p) => p.enabled === 1);
    if (!q) return enabledProviders;
    return enabledProviders.filter((p) => {
      const label = apiTypeLabels[p.api_type] || '';
      return (
        p.name.toLowerCase().includes(q) ||
        p.api_type.toLowerCase().includes(q) ||
        label.toLowerCase().includes(q) ||
        (p.base_url || '').toLowerCase().includes(q)
      );
    });
  }, [providers, query]);

  const configured = useMemo(
    () => filtered.filter((p) => configuredIds.has(p.id)),
    [filtered, configuredIds],
  );
  const others = useMemo(
    () => filtered.filter((p) => !configuredIds.has(p.id)),
    [filtered, configuredIds],
  );

  // 未配置服务商按收费类型分两组
  const freeGroup = useMemo(() => others.filter((p) => p.pricing_type === 'free'), [others]);
  const apiProvidersGroup = useMemo(
    () => others.filter((p) => p.pricing_type !== 'free'),
    [others],
  );

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      if (editingId) {
        await api.put(`/providers/${editingId}`, form);
      } else {
        await api.post('/providers', form);
      }
      setShowModal(false);
      setForm({ name: '', base_url: '', api_type: 'openai', priority: 0, pricing_type: 'paid', auto_sync: false });
      setEditingId(null);
      fetchData();
    } catch {
      setError('保存失败');
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm('确定删除该提供商吗？')) return;
    try {
      await api.delete(`/providers/${id}`);
      fetchData();
    } catch {
      setError('删除失败');
    }
  };

  const handleHealthCheck = async (id: number) => {
    try {
      await api.post(`/providers/${id}/health-check`);
      fetchData();
    } catch {
      setError('健康检查失败');
    }
  };

  const handleToggle = async (p: Provider) => {
    try {
      await api.put(`/providers/${p.id}`, {
        name: p.name,
        base_url: p.base_url,
        api_type: p.api_type,
        priority: p.priority,
        pricing_type: p.pricing_type || 'paid',
        enabled: p.enabled !== 1,
        auto_sync: p.auto_sync ?? 0,
      });
      fetchData();
    } catch {
      setError('更新状态失败');
    }
  };

  const handleAutoSync = async (p: Provider) => {
    try {
      const newVal = p.auto_sync ? 0 : 1;
      await api.put(`/providers/${p.id}`, {
        auto_sync: newVal,
      });
      setSyncStatus(prev => ({ ...prev, [p.id]: 'ok' }));
      fetchData();
    } catch {
      setSyncStatus(prev => ({ ...prev, [p.id]: 'err' }));
      setError('更新同步设置失败');
    }
  };

  const openEdit = (p: Provider) => {
    setForm({ name: p.name, base_url: p.base_url, api_type: p.api_type, priority: p.priority, pricing_type: p.pricing_type || 'paid', auto_sync: p.auto_sync === 1 });
    setEditingId(p.id);
    setShowModal(true);
  };

  const handlers: CardHandlers = {
    navigate,
    isAdmin,
    onHealthCheck: handleHealthCheck,
    onToggle: handleToggle,
    onAutoSync: handleAutoSync,
    onEdit: openEdit,
    onDelete: handleDelete,
    syncStatus,
  };

  if (loading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h2 className="text-2xl font-bold text-gray-900">提供商</h2>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4 animate-pulse">
          {[...Array(8)].map((_, i) => (
            <div key={i} className="h-40 bg-gray-200 rounded-xl" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-2xl font-bold text-gray-900">提供商</h2>
          <p className="text-sm text-gray-400 mt-0.5">
            {query.trim()
              ? `匹配到 ${filtered.length} / ${providers.length} 个服务商`
              : `共 ${providers.length} 个服务商`}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" />
            <input
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索服务商…"
              aria-label="搜索服务商"
              className="w-56 sm:w-64 pl-9 pr-8 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
            />
            {query && (
              <button
                type="button"
                onClick={() => setQuery('')}
                aria-label="清除搜索"
                className="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-gray-400 hover:text-gray-600 rounded"
              >
                <X className="w-4 h-4" />
              </button>
            )}
          </div>
          {isAdmin && (
            <button
              onClick={async () => {
                try {
                  setSyncing(true);
                  await api.post('/admin/models/sync');
                  fetchData();
                  setError('');
                } catch (e: any) {
                  setError(e?.response?.data?.error || '同步失败，请检查服务商配置');
                } finally {
                  setSyncing(false);
                }
              }}
              disabled={syncing}
              className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-white bg-green-600 rounded-lg hover:bg-green-700 disabled:opacity-50 transition-colors whitespace-nowrap"
              title="手动触发一次全量模型同步（从上游拉取最新模型列表并测试连通性）"
            >
              <RefreshCw className={`w-4 h-4 ${syncing ? 'animate-spin' : ''}`} />
              {syncing ? '同步中...' : '模型同步'}
            </button>
          )}
          {isAdmin && (
            <button
              onClick={() => {
                setEditingId(null);
                setForm({ name: '', base_url: '', api_type: 'openai', priority: 0, pricing_type: 'paid', auto_sync: false });
                setShowModal(true);
              }}
              className="flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 transition-colors whitespace-nowrap"
            >
              <Plus className="w-4 h-4" />
              添加提供商
            </button>
          )}
        </div>
      </div>

      {error && (
        <div className="bg-red-50 border border-red-200 rounded-xl p-4 text-red-700 text-sm">{error}</div>
      )}

      {providers.length === 0 ? (
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 py-16 text-center">
          <p className="text-gray-400 text-sm">暂无提供商</p>
        </div>
      ) : filtered.length === 0 ? (
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 py-16 text-center">
          <p className="text-gray-500 text-sm">
            没有匹配「<span className="font-medium text-gray-700">{query}</span>」的服务商
          </p>
          <button
            type="button"
            onClick={() => setQuery('')}
            className="mt-3 text-sm font-medium text-indigo-600 hover:text-indigo-700"
          >
            清除搜索
          </button>
        </div>
      ) : (
        <>
          {/* 已添加分组 */}
          {configured.length > 0 && (
            <section>
              <div className="flex items-center gap-2 mb-4">
                <CheckCircle2 className="w-5 h-5 text-green-600" />
                <h3 className="text-base font-semibold text-gray-900">已添加</h3>
                <span className="inline-flex items-center justify-center px-2 py-0.5 rounded-full bg-green-100 text-green-700 text-xs font-medium">
                  {configured.length}
                </span>
                <span className="text-xs text-gray-400">已配置账户的服务商</span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                {configured.map((p) => (
                  <ProviderCard key={p.id} p={p} handlers={handlers} />
                ))}
              </div>
            </section>
          )}

          {/* 未配置：按收费类型分组 */}
          {others.length > 0 && (
            <section className="space-y-8">
              {freeGroup.length > 0 && (
                <div>
                  <div className="flex items-center gap-2 mb-4">
                    <Circle className="w-5 h-5 text-emerald-500" />
                    <h3 className="text-base font-semibold text-gray-900">完全免费</h3>
                    <span className="inline-flex items-center justify-center px-2 py-0.5 rounded-full bg-emerald-100 text-emerald-700 text-xs font-medium">
                      {freeGroup.length}
                    </span>
                    <span className="text-xs text-gray-400">本地部署或零成本运行</span>
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                    {freeGroup.map((p) => (
                      <ProviderCard key={p.id} p={p} handlers={handlers} />
                    ))}
                  </div>
                </div>
              )}

              {apiProvidersGroup.length > 0 && (
                <div>
                  <div className="flex items-center gap-2 mb-4">
                    <Circle className="w-5 h-5 text-amber-500" />
                    <h3 className="text-base font-semibold text-gray-900">API 提供商</h3>
                    <span className="inline-flex items-center justify-center px-2 py-0.5 rounded-full bg-amber-100 text-amber-700 text-xs font-medium">
                      {apiProvidersGroup.length}
                    </span>
                    <span className="text-xs text-gray-400">需要 API Key 的付费服务商</span>
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                    {apiProvidersGroup.map((p) => (
                      <ProviderCard key={p.id} p={p} handlers={handlers} />
                    ))}
                  </div>
                </div>
              )}
            </section>
          )}
        </>
      )}

      {showModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-white rounded-xl shadow-xl p-6 w-full max-w-md">
            <h3 className="text-lg font-semibold text-gray-900 mb-4">
              {editingId ? '编辑提供商' : '添加提供商'}
            </h3>
            <form onSubmit={handleSubmit} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">名称</label>
                <input
                  type="text"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">基础地址</label>
                <input
                  type="url"
                  value={form.base_url}
                  onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  required
                />
                {form.api_type === 'cloudflare' && (
                  <p className="mt-1 text-xs text-gray-400">
                    填入含 <code>{'{accountId}'}</code> 占位符的地址，例如
                    <code className="ml-1">https://api.cloudflare.com/client/v4/accounts/{'{accountId}'}/ai/v1/chat/completions</code>
                    ；账户处再填 Account ID。
                  </p>
                )}
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">接口类型</label>
                  <select
                    value={form.api_type}
                    onChange={(e) => setForm({ ...form, api_type: e.target.value })}
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  >
                    <option value="openai">OpenAI</option>
                    <option value="cloudflare">Cloudflare (Workers AI)</option>
                    <option value="anthropic">Anthropic (Claude)</option>
                  <option value="claude">Claude Code</option>
                  <option value="gemini">Google Gemini</option>
                  <option value="vertex">Vertex AI</option>
                  <option value="azure">Azure OpenAI</option>
                  <option value="groq">Groq</option>
                  <option value="deepseek">DeepSeek</option>
                  <option value="qwen">通义千问</option>
                  <option value="siliconflow">硅基流动</option>
                  <option value="moonshot">月之暗面 (Kimi)</option>
                  <option value="zhipu">智谱 GLM</option>
                  <option value="glm">智谱 (z.ai)</option>
                  <option value="mistral">Mistral</option>
                  <option value="openrouter">OpenRouter</option>
                  <option value="together">Together AI</option>
                  <option value="fireworks">Fireworks AI</option>
                  <option value="cerebras">Cerebras</option>
                  <option value="cohere">Cohere</option>
                  <option value="perplexity">Perplexity</option>
                  <option value="xai">xAI (Grok)</option>
                  <option value="nvidia">NVIDIA NIM</option>
                  <option value="hyperbolic">Hyperbolic</option>
                  <option value="nebius">Nebius AI</option>
                  <option value="venice">Venice AI</option>
                  <option value="chutes">Chutes AI</option>
                  <option value="featherless">Featherless</option>
                  <option value="volcengine">火山方舟</option>
                  <option value="minimax">MiniMax</option>
                  <option value="alibaba">阿里云百炼</option>
                  <option value="codebuddy">腾讯 CodeBuddy</option>
                  <option value="github">GitHub Copilot</option>
                  <option value="ollama">Ollama (本地)</option>
                  <option value="opencode">OpenCode</option>
                  <option value="kilo">Kilo Code</option>
                  <option value="custom">自定义</option>
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">收费类型</label>
                <select
                  value={form.pricing_type}
                  onChange={(e) => setForm({ ...form, pricing_type: e.target.value as 'free' | 'free_trial' | 'paid' })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                >
                  <option value="free">完全免费</option>
                  <option value="free_trial">免费额度</option>
                  <option value="paid">API提供商</option>
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">优先级</label>
                <input
                  type="number"
                  value={form.priority}
                  onChange={(e) => setForm({ ...form, priority: Number(e.target.value) })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                />
              </div>
              <div className="flex justify-end gap-3 pt-2">
                <button
                  type="button"
                  onClick={() => setShowModal(false)}
                  className="px-4 py-2 text-sm font-medium text-gray-700 bg-gray-100 rounded-lg hover:bg-gray-200 transition-colors"
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="px-4 py-2 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-700 transition-colors"
                >
                  保存
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
