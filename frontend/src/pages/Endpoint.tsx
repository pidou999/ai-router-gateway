import { useState, useEffect } from 'react';
import { Copy, Check, Plus, Trash2, Eye, EyeOff, KeyRound, Globe, Shield } from 'lucide-react';
import api from '../api/client';

interface APIKey {
  id: number;
  name: string;
  prefix: string;
  scopes: string;
  enabled: number;
  last_used?: string;
  created_at: string;
}

export default function Endpoint() {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // 创建密钥
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [createdKey, setCreatedKey] = useState<string | null>(null);

  // 设置
  const [requireApiKey, setRequireApiKey] = useState(false);

  // 复制 & 显隐
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [visibleKeys, setVisibleKeys] = useState<Set<number>>(new Set());
  // 缓存已拉取的完整密钥 id -> fullKey
  const [fullKeys, setFullKeys] = useState<Map<number, string>>(new Map());

  const isAdmin = (localStorage.getItem('role') || 'user') === 'admin';

  // 获取本地端点 URL
  const [endpointUrl, setEndpointUrl] = useState('');
  useEffect(() => {
    if (typeof window !== 'undefined') {
      setEndpointUrl(`${window.location.origin}/v1`);
    }
  }, []);

  const fetchData = async () => {
    try {
      const [keysRes, settingsRes] = await Promise.all([
        api.get<APIKey[]>('/auth/api-keys'),
        isAdmin ? api.get<Record<string, unknown>>('/admin/settings') : Promise.resolve({ data: {} as Record<string, unknown> }),
      ]);
      setKeys(keysRes.data || []);
      if (isAdmin && settingsRes.data) {
        setRequireApiKey(settingsRes.data.require_api_key === true || settingsRes.data.require_api_key === 'true');
      }
      setError('');
    } catch {
      setError('加载数据失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  // 拉取完整密钥（带缓存）
  const fetchFullKey = async (id: number): Promise<string | null> => {
    // 先查缓存
    const cached = fullKeys.get(id);
    if (cached) return cached;
    try {
      const res = await api.get<{ api_key: string }>(`/auth/api-keys/${id}/key`);
      const full = res.data?.api_key;
      if (full) {
        setFullKeys(prev => new Map(prev).set(id, full));
        return full;
      }
    } catch {
      // 旧版哈希密钥无法恢复，降级显示前缀
    }
    return null;
  };

  // 获取用于显示的密钥文本
  const getKeyDisplay = (key: APIKey): string => {
    if (visibleKeys.has(key.id)) {
      const full = fullKeys.get(key.id);
      if (full) return full;
      // 正在加载中或无法获取，仍显示前缀
      return key.prefix || '****';
    }
    // 遮蔽状态
    return (key.prefix || '') + '****';
  };

  // 获取用于复制的密钥文本（优先完整密钥）
  const getCopyText = async (key: APIKey): Promise<string> => {
    const full = await fetchFullKey(key.id);
    return full || (key.prefix || '') + '****';
  };

  // 复制到剪贴板
  const copyToClipboard = async (text: string, id: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 1500);
    } catch {
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 1500);
    }
  };

  // 创建密钥
  const handleCreateKey = async () => {
    if (!newKeyName.trim()) return;
    try {
      const res = await api.post<{ key: string }>('/auth/api-keys', { name: newKeyName, scopes: '*' });
      if (res.data.key) {
        setCreatedKey(res.data.key);
        setNewKeyName('');
        setShowCreateModal(false);
        fetchData();
      }
    } catch {
      setError('创建密钥失败');
    }
  };

  // 删除密钥
  const handleDeleteKey = async (id: number) => {
    if (!confirm('确定删除该 API 密钥吗？')) return;
    try {
      await api.delete(`/auth/api-keys/${id}`);
      setVisibleKeys(prev => { const next = new Set(prev); next.delete(id); return next; });
      setFullKeys(prev => { const next = new Map(prev); next.delete(id); return next; });
      fetchData();
    } catch {
      setError('删除密钥失败');
    }
  };

  // 切换密钥启用状态
  const handleToggleKey = async (id: number, isActive: boolean) => {
    try {
      await api.put(`/auth/api-keys/${id}`, { isActive });
      fetchData();
    } catch {
      setError('切换状态失败');
    }
  };

  // 切换"需要 API 密钥"
  const handleToggleRequireApiKey = async (value: boolean) => {
    try {
      await api.put('/admin/settings', { require_api_key: value });
      setRequireApiKey(value);
    } catch {
      setError('更新设置失败');
    }
  };

  // 切换显隐（如果展开则自动拉完整密钥）
  const toggleVisibility = async (id: number) => {
    setVisibleKeys(prev => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id); // 隐藏
      } else {
        next.add(id); // 显示 — 触发拉取完整密钥
      }
      return next;
    });
    // 如果是"显示"操作，预拉完整密钥
    if (!visibleKeys.has(id)) {
      fetchFullKey(id);
    }
  };

  // 带完整密钥的复制
  const handleCopyKey = async (key: APIKey) => {
    const text = await getCopyText(key);
    await copyToClipboard(text, `copy-${key.id}`);
  };

  if (loading) {
    return (
      <div className="space-y-6 animate-pulse">
        <div className="h-40 bg-gray-200 rounded-xl" />
        <div className="h-80 bg-gray-200 rounded-xl" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* ====== API 端点卡片 ====== */}
      <div className="bg-white rounded-xl shadow-sm p-6">
        <h2 className="text-lg font-semibold text-gray-900 mb-4 flex items-center gap-2">
          <Globe className="w-5 h-5 text-indigo-600" />
          API 端点
        </h2>

        <div className="flex flex-col gap-3">
          {/* Local */}
          <div className="flex items-center gap-3">
            <span className="text-xs font-mono px-2 py-1 rounded bg-gray-100 text-gray-600 shrink-0 min-w-[72px] text-center">
              Local
            </span>
            <code className="flex-1 px-3 py-2 bg-gray-50 border border-gray-200 rounded-lg text-sm font-mono text-gray-700 truncate">
              {endpointUrl}
            </code>
            <button
              onClick={() => copyToClipboard(endpointUrl, 'endpoint')}
              className="p-2 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-100 transition-colors shrink-0"
              title={copiedId === 'endpoint' ? '已复制' : '复制'}
            >
              {copiedId === 'endpoint' ? <Check className="w-4 h-4 text-green-500" /> : <Copy className="w-4 h-4" />}
            </button>
          </div>
        </div>

        <p className="mt-3 text-xs text-gray-400">
          将此端点配置到你的 AI 工具（Cursor、Cline、Continue 等）中，即可通过本网关路由请求。
        </p>
      </div>

      {/* ====== API 密钥卡片 ====== */}
      <div className="bg-white rounded-xl shadow-sm p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
            <KeyRound className="w-5 h-5 text-indigo-600" />
            API 密钥
          </h2>
          <button
            onClick={() => setShowCreateModal(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 transition-colors"
          >
            <Plus className="w-4 h-4" />
            创建密钥
          </button>
        </div>

        {/* 需要密钥开关 */}
        {isAdmin && (
          <div className="flex items-center justify-between pb-4 mb-4 border-b border-gray-200">
            <div>
              <p className="font-medium text-sm text-gray-900">需要 API 密钥</p>
              <p className="text-xs text-gray-500 mt-0.5">没有有效密钥的请求将被拒绝</p>
            </div>
            <button
              onClick={() => handleToggleRequireApiKey(!requireApiKey)}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                requireApiKey ? 'bg-indigo-600' : 'bg-gray-300'
              }`}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  requireApiKey ? 'translate-x-6' : 'translate-x-1'
                }`}
              />
            </button>
          </div>
        )}

        {error && (
          <div className="mb-4 bg-red-50 border border-red-200 rounded-lg px-4 py-3 text-red-700 text-sm">{error}</div>
        )}

        {/* 密钥列表 */}
        {keys.length === 0 ? (
          <div className="text-center py-12">
            <div className="inline-flex items-center justify-center w-14 h-14 rounded-full bg-indigo-50 text-indigo-500 mb-3">
              <Shield className="w-7 h-7" />
            </div>
            <p className="text-gray-700 font-medium text-sm mb-1">暂无 API 密钥</p>
            <p className="text-gray-400 text-xs mb-4">创建第一个 API 密钥以开始使用</p>
            <button
              onClick={() => setShowCreateModal(true)}
              className="inline-flex items-center gap-1.5 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 transition-colors"
            >
              <Plus className="w-4 h-4" />
              创建密钥
            </button>
          </div>
        ) : (
          <div className="divide-y divide-gray-100">
            {keys.map((key) => (
              <div
                key={key.id}
                className={`group flex items-center justify-between py-3 ${key.enabled === 0 ? 'opacity-50' : ''}`}
              >
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-gray-900">{key.name}</p>
                  <div className="flex items-center gap-2 mt-1">
                    <code className="text-xs text-gray-500 font-mono">
                      {getKeyDisplay(key)}
                    </code>
                    <button
                      onClick={() => toggleVisibility(key.id)}
                      className="p-1 text-gray-400 hover:text-indigo-600 rounded hover:bg-gray-100 opacity-0 group-hover:opacity-100 transition-all"
                      title={visibleKeys.has(key.id) ? '隐藏' : '显示完整密钥'}
                    >
                      {visibleKeys.has(key.id) ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
                    </button>
                    <button
                      onClick={() => handleCopyKey(key)}
                      className="p-1 text-gray-400 hover:text-indigo-600 rounded hover:bg-gray-100 opacity-0 group-hover:opacity-100 transition-all"
                      title="复制完整密钥"
                    >
                      {copiedId === `copy-${key.id}` ? <Check className="w-3.5 h-3.5 text-green-500" /> : <Copy className="w-3.5 h-3.5" />}
                    </button>
                  </div>
                  <p className="text-xs text-gray-400 mt-1">
                    创建于 {new Date(key.created_at).toLocaleDateString('zh-CN')}
                    {key.last_used && ` · 最后使用 ${new Date(key.last_used).toLocaleDateString('zh-CN')}`}
                  </p>
                  {key.enabled === 0 && (
                    <p className="text-xs text-orange-500 mt-0.5">已停用</p>
                  )}
                </div>
                <div className="flex items-center gap-2 ml-4">
                  {/* 启用/停用开关 */}
                  <button
                    onClick={() => handleToggleKey(key.id, key.enabled === 0)}
                    className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                      key.enabled === 1 ? 'bg-indigo-600' : 'bg-gray-300'
                    }`}
                    title={key.enabled === 1 ? '停用' : '启用'}
                  >
                    <span
                      className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${
                        key.enabled === 1 ? 'translate-x-5' : 'translate-x-1'
                      }`}
                    />
                  </button>
                  {/* 删除 */}
                  <button
                    onClick={() => handleDeleteKey(key.id)}
                    className="p-1.5 text-gray-400 hover:text-red-600 rounded-lg hover:bg-red-50 opacity-0 group-hover:opacity-100 transition-all"
                    title="删除"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ====== 创建密钥弹窗 ====== */}
      {showCreateModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-white rounded-xl shadow-xl p-6 w-full max-w-md">
            <h3 className="text-lg font-semibold text-gray-900 mb-4">创建 API 密钥</h3>
            <div className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">密钥名称</label>
                <input
                  type="text"
                  value={newKeyName}
                  onChange={(e) => setNewKeyName(e.target.value)}
                  placeholder="例如：生产环境密钥"
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  autoFocus
                />
              </div>
              <div className="flex gap-3 pt-2">
                <button
                  onClick={handleCreateKey}
                  disabled={!newKeyName.trim()}
                  className="flex-1 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                >
                  创建
                </button>
                <button
                  onClick={() => { setShowCreateModal(false); setNewKeyName(''); }}
                  className="flex-1 px-4 py-2 bg-gray-100 text-gray-700 rounded-lg text-sm font-medium hover:bg-gray-200 transition-colors"
                >
                  取消
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* ====== 密钥创建成功（仅显示一次） ====== */}
      {createdKey && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-white rounded-xl shadow-xl p-6 w-full max-w-md">
            <h3 className="text-lg font-semibold text-gray-900 mb-4">API 密钥已创建</h3>
            <div className="flex gap-2 mb-4">
              <input
                type="text"
                value={createdKey}
                readOnly
                className="flex-1 px-3 py-2 bg-gray-50 border border-gray-200 rounded-lg font-mono text-sm text-gray-700"
              />
              <button
                onClick={() => copyToClipboard(createdKey, 'created')}
                className={`px-3 py-2 rounded-lg text-sm font-medium border transition-colors ${
                  copiedId === 'created'
                    ? 'bg-green-50 border-green-200 text-green-700'
                    : 'bg-white border-gray-300 text-gray-700 hover:bg-gray-50'
                }`}
              >
                {copiedId === 'created' ? '已复制！' : '复制'}
              </button>
            </div>
            <button
              onClick={() => setCreatedKey(null)}
              className="w-full px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 transition-colors"
            >
              完成
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
