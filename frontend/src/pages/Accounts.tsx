import { useState, useEffect } from 'react';
import { Plus, Pencil, Trash2, Copy, Check } from 'lucide-react';
import api from '../api/client';
import type { Account, Provider } from '../types';

export default function Accounts() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState({ name: '', provider_id: 0, api_key: '', rate_limit_rpm: 0 });
  // 复制按钮状态：记录哪个字段被复制了（格式 "rowId-field"）
  const [copiedField, setCopiedField] = useState<string | null>(null);

  const isAdmin = (localStorage.getItem('role') || 'user') === 'admin';

  const fetchData = async () => {
    try {
      const [accRes, provRes] = await Promise.all([
        api.get<Account[]>('/accounts'),
        api.get<Provider[]>('/providers'),
      ]);
      setAccounts(accRes.data);
      setProviders(provRes.data);
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

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      if (editingId) {
        await api.put(`/accounts/${editingId}`, form);
      } else {
        await api.post('/accounts', form);
      }
      setShowModal(false);
      setForm({ name: '', provider_id: providers[0]?.id || 0, api_key: '', rate_limit_rpm: 0 });
      setEditingId(null);
      fetchData();
    } catch {
      setError('保存失败');
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm('确定删除该账户吗？')) return;
    try {
      await api.delete(`/accounts/${id}`);
      fetchData();
    } catch {
      setError('删除失败');
    }
  };

  const maskKey = (key: string) => {
    if (!key) return '****';
    return key.substring(0, 4) + '****';
  };

  // 复制到剪贴板，带短暂成功反馈
  const copyToClipboard = async (text: string, fieldId: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedField(fieldId);
      setTimeout(() => setCopiedField(null), 1500);
    } catch {
      // fallback for non-secure contexts
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
      setCopiedField(fieldId);
      setTimeout(() => setCopiedField(null), 1500);
    }
  };

  // 获取完整密钥并复制
  const copyFullKey = async (accountId: number) => {
    try {
      const res = await api.get<{ api_key: string }>(`/accounts/${accountId}/key`);
      const key = res.data.api_key;
      if (key) {
        await copyToClipboard(key, `key-${accountId}`);
      }
    } catch {
      // 如果接口失败，尝试复制前缀
      const acc = accounts.find(a => a.id === accountId);
      if (acc?.api_key_prefix) {
        copyToClipboard(acc.api_key_prefix, `key-${accountId}`);
      }
    }
  };

  // 复制按钮组件
  const CopyButton = ({ text, fieldId, isKey = false }: { text: string; fieldId: string; isKey?: boolean }) => {
    const isCopied = copiedField === fieldId;
    return (
      <button
        onClick={() => isKey ? copyFullKey(Number(fieldId.split('-')[1])) : copyToClipboard(text, fieldId)}
        className="inline-flex items-center gap-0.5 p-1 text-gray-400 hover:text-indigo-600 rounded hover:bg-gray-100 transition-colors"
        title={isCopied ? '已复制' : '复制'}
      >
        {isCopied ? <Check className="w-3.5 h-3.5 text-green-500" /> : <Copy className="w-3.5 h-3.5" />}
      </button>
    );
  };

  if (loading) {
    return (
      <div className="space-y-4 animate-pulse">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="h-16 bg-gray-200 rounded-xl" />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold text-gray-900">账户</h2>
        {isAdmin && (
          <button
            onClick={() => {
              setEditingId(null);
              setForm({ name: '', provider_id: providers[0]?.id || 0, api_key: '', rate_limit_rpm: 0 });
              setShowModal(true);
            }}
            className="flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 transition-colors"
          >
            <Plus className="w-4 h-4" />
            添加账户
          </button>
        )}
      </div>

      {error && (
        <div className="bg-red-50 border border-red-200 rounded-xl p-4 text-red-700 text-sm">{error}</div>
      )}

      <div className="bg-white rounded-xl shadow-sm overflow-hidden">
        <table className="w-full">
          <thead>
            <tr className="border-b border-gray-200 bg-gray-50">
              <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">名称</th>
              <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">提供商</th>
              <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">API 地址</th>
              <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">API 密钥</th>
              <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">速率限制</th>
              <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">状态</th>
              <th className="text-right py-3 px-4 text-sm font-medium text-gray-500">操作</th>
            </tr>
          </thead>
          <tbody>
            {accounts.map((a) => (
              <tr key={a.id} className="border-b border-gray-100 hover:bg-gray-50">
                <td className="py-3 px-4 text-sm font-medium text-gray-900">{a.name}</td>
                <td className="py-3 px-4 text-sm text-gray-600">{a.provider_name || `#${a.provider_id}`}</td>
                <td className="py-3 px-4 text-sm">
                  <div className="flex items-center gap-1">
                    <span className="text-gray-600 font-mono truncate max-w-[280px]" title={a.base_url || ''}>
                      {a.base_url || '-'}
                    </span>
                    {a.base_url && (
                      <CopyButton text={a.base_url} fieldId={`url-${a.id}`} />
                    )}
                  </div>
                </td>
                <td className="py-3 px-4 text-sm">
                  <div className="flex items-center gap-1">
                    <span className="text-gray-600 font-mono">{maskKey(a.api_key)}</span>
                    <CopyButton text="" fieldId={`key-${a.id}`} isKey />
                  </div>
                </td>
                <td className="py-3 px-4 text-sm text-gray-600">{a.rate_limit_rpm ? `${a.rate_limit_rpm}/分钟` : 'N/A'}</td>
                <td className="py-3 px-4">
                  <span className={`inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${
                    a.enabled === 1 ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-800'
                  }`}>
                    {a.enabled === 1 ? '启用' : '停用'}
                  </span>
                </td>
                <td className="py-3 px-4">
                  {isAdmin && (
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => {
                          setEditingId(a.id);
                          setForm({
                            name: a.name,
                            provider_id: a.provider_id,
                            api_key: a.api_key,
                            rate_limit_rpm: a.rate_limit_rpm || 0,
                          });
                          setShowModal(true);
                        }}
                        className="p-1.5 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-100 transition-colors"
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => handleDelete(a.id)}
                        className="p-1.5 text-gray-400 hover:text-red-600 rounded-lg hover:bg-gray-100 transition-colors"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
            {accounts.length === 0 && (
              <tr>
                <td colSpan={7} className="py-8 text-center text-gray-400 text-sm">
                  暂无账户
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {showModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-white rounded-xl shadow-xl p-6 w-full max-w-md">
            <h3 className="text-lg font-semibold text-gray-900 mb-4">
              {editingId ? '编辑账户' : '添加账户'}
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
                  placeholder="例如：我的 OpenAI 账户"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">提供商</label>
                <select
                  value={form.provider_id}
                  onChange={(e) => setForm({ ...form, provider_id: Number(e.target.value) })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  required
                >
                  <option value={0}>选择提供商</option>
                  {providers.map((p) => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">API 密钥</label>
                <input
                  type="password"
                  value={form.api_key}
                  onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 font-mono"
                  required
                  placeholder="sk-... 或 pk-... 格式的 API Key"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">速率限制（次/分钟）</label>
                <input
                  type="number"
                  value={form.rate_limit_rpm}
                  onChange={(e) => setForm({ ...form, rate_limit_rpm: Number(e.target.value) })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  min="0"
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
                  {editingId ? '更新' : '创建'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
