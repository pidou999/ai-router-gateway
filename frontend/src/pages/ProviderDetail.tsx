import { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  ArrowLeft,
  Pencil,
  Activity,
  Eye,
  EyeOff,
  Copy,
  Check,
  Key,
  Plus,
  Trash2,
  Zap,
  Loader2,
  Scan,
} from 'lucide-react';
import api from '../api/client';
import type { Provider, Account, ProviderModel } from '../types';
import { healthLabels, apiTypeLabels, healthDot, healthBadge, capabilityLabels, capabilityBadgeClass, isCapabilityMuted } from '../labels';

export default function ProviderDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const [provider, setProvider] = useState<Provider | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [copied, setCopied] = useState(false);
  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState({ name: '', base_url: '', api_type: 'openai', priority: 0 });

  const [showAccountModal, setShowAccountModal] = useState(false);
  const [accForm, setAccForm] = useState({ name: '', api_key: '', account_id: '', rate_limit_rpm: 0, rate_limit_tpm: 0, extra_config: '' });
  const [editingAccId, setEditingAccId] = useState<number | null>(null);

  // 当服务商类型需要额外 ID（如 Cloudflare 的 Account ID）时，账户表单显示对应输入框。
  const needsAccountID = !!provider && (provider.api_type === 'cloudflare' || provider.base_url.includes('{accountId}'));
  const [showKey, setShowKey] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);

  const [healthChecking, setHealthChecking] = useState(false);
  const [healthMsg, setHealthMsg] = useState('');

  const [models, setModels] = useState<ProviderModel[]>([]);
  const [fetchingModels, setFetchingModels] = useState(false);
  const [modelMsg, setModelMsg] = useState('');

  const [testingAll, setTestingAll] = useState(false);
  const [testAllMsg, setTestAllMsg] = useState<{ ok: number; fail: number; total: number } | null>(null);
  // 全部测试的实时进度（SSE 流式推送驱动）
  const [testProgress, setTestProgress] = useState<{ done: number; total: number; ok: number; fail: number; current: string } | null>(null);
  const [testingModelId, setTestingModelId] = useState<string | null>(null);
  // 真实多模态探测（发小图给上游判定 vision）：单模型与批量
  const [probingModelId, setProbingModelId] = useState<string | null>(null);
  const [probingAll, setProbingAll] = useState(false);
  const [probeAllMsg, setProbeAllMsg] = useState<{ total: number; vision: number; text: number; unknown: number } | null>(null);
  const [copiedModel, setCopiedModel] = useState<string | null>(null);
  const [copiedKey, setCopiedKey] = useState(false);
  const [visibleKeyId, setVisibleKeyId] = useState<number | null>(null);
  const [copiedAccId, setCopiedAccId] = useState<number | null>(null);
  const [fullKeys, setFullKeys] = useState<Record<number, string>>({});

  const role = localStorage.getItem('role') || 'user';
  const isAdmin = role === 'admin';

  const fetchData = async () => {
    try {
      const [pRes, aRes, mRes] = await Promise.all([
        api.get<Provider>(`/providers/${id}`),
        api.get<Account[]>('/accounts'),
        api.get<ProviderModel[]>(`/providers/${id}/models`),
      ]);
      setProvider(pRes.data);
      setAccounts(aRes.data.filter((a) => String(a.provider_id) === String(id)));
      setModels(mRes.data || []);
      setError('');
      setFullKeys({});
    } catch {
      setError('加载服务商详情失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  const handleHealthCheck = async () => {
    if (!id) return;
    setHealthChecking(true);
    setHealthMsg('');
    try {
      const { data } = await api.post<{ health_status: string; message: string }>(`/providers/${id}/health-check`);
      setHealthMsg(data.message || (data.health_status === 'healthy' ? '连接成功' : '连接失败'));
      fetchData();
    } catch {
      setHealthMsg('健康检查请求失败');
    } finally {
      setHealthChecking(false);
    }
  };

  const handleFetchModels = async () => {
    if (!id) return;
    setFetchingModels(true);
    setModelMsg('');
    try {
      const { data } = await api.post<{ ok: boolean; message: string; models: ProviderModel[] }>(
        `/providers/${id}/models/fetch`,
      );
      setModelMsg(data.message || (data.ok ? '获取成功' : '获取失败'));
      if (data.models) {
        setModels(data.models);
      }
      fetchData();
    } catch {
      setModelMsg('获取模型失败');
    } finally {
      setFetchingModels(false);
    }
  };

  const toggleModel = async (modelId: string, enabled: number) => {
    if (!id) return;
    try {
      await api.put(`/providers/${id}/models`, { model_id: modelId, enabled });
      setModels((prev) =>
        prev.map((m) => (m.model_id === modelId ? { ...m, enabled } : m)),
      );
    } catch {
      setError('更新模型状态失败');
    }
  };

  const copyModel = async (modelId: string) => {
    try {
      await navigator.clipboard.writeText(modelId);
      setCopiedModel(modelId);
      setTimeout(() => setCopiedModel((c) => (c === modelId ? null : c)), 1500);
    } catch {
      /* 忽略复制失败 */
    }
  };

  const copyApiKey = async () => {
    if (!accForm.api_key) return;
    try {
      await navigator.clipboard.writeText(accForm.api_key);
      setCopiedKey(true);
      setTimeout(() => setCopiedKey(false), 1500);
    } catch {
      /* 忽略复制失败 */
    }
  };

  const copyAccKey = async (accId: number) => {
    try {
      let keyText = fullKeys[accId];
      if (!keyText) {
        const { data } = await api.get<{ api_key: string }>(`/accounts/${accId}/key`);
        keyText = data.api_key;
        setFullKeys((prev) => ({ ...prev, [accId]: keyText }));
      }
      await navigator.clipboard.writeText(keyText);
      setCopiedAccId(accId);
      setTimeout(() => setCopiedAccId((id) => (id === accId ? null : id)), 1500);
    } catch {
      /* 忽略 */
    }
  };

  const showFullKey = async (accId: number) => {
    if (fullKeys[accId]) {
      setVisibleKeyId((v) => (v === accId ? null : accId));
      return;
    }
    try {
      const { data } = await api.get<{ api_key: string }>(`/accounts/${accId}/key`);
      setFullKeys((prev) => ({ ...prev, [accId]: data.api_key }));
      setVisibleKeyId(accId);
    } catch {
      /* 忽略 */
    }
  };

  const testSingleModel = async (modelId: string) => {
    if (!id || testingModelId === modelId) return;
    setTestingModelId(modelId);
    try {
      const { data } = await api.post<{ ok: boolean; enabled: number }>(
        `/providers/${id}/models/test`,
        { model_id: modelId },
      );
      setModels((prev) =>
        prev.map((m) => (m.model_id === modelId ? { ...m, enabled: data.enabled } : m)),
      );
    } catch {
      /* 忽略单模型测试失败 */
    } finally {
      setTestingModelId(null);
    }
  };

  const testAllModels = async () => {
    if (!id || testingAll) return;
    setTestingAll(true);
    setTestAllMsg(null);
    setTestProgress({ done: 0, total: 0, ok: 0, fail: 0, current: '' });
    try {
      // 后端以 SSE 流式返回进度：start(total) → result×N(每测完一个) → done(汇总)
      const token = localStorage.getItem('token') || '';
      const res = await fetch(`/api/providers/${id}/models/test-all`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
      });
      if (!res.ok || !res.body) {
        if (res.status === 401) {
          localStorage.removeItem('token');
          window.location.href = '/login';
        }
        throw new Error(`HTTP ${res.status}`);
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let total = 0;
      let done = 0;
      let ok = 0;
      let fail = 0;
      let current = '';
      const applyEvent = (ev: Record<string, unknown>) => {
        switch (ev.type) {
          case 'start':
            total = Number(ev.total) || 0;
            break;
          case 'result': {
            done += 1;
            if (ev.ok) ok += 1;
            else fail += 1;
            const mid = String(ev.model_id || '');
            const enabled = Number(ev.enabled) || 0;
            current = String(ev.display_name || ev.model_id || '');
            // 实时同步本地模型启用状态
            setModels((prev) => prev.map((m) => (m.model_id === mid ? { ...m, enabled } : m)));
            break;
          }
          case 'done':
            total = Number(ev.total) || total;
            ok = Number(ev.ok_count) || ok;
            fail = Number(ev.fail_count) || fail;
            break;
        }
        setTestProgress({ done, total, ok, fail, current });
      };
      // 按 SSE 帧（空行分隔）解析
      for (;;) {
        const { value, done: streamDone } = await reader.read();
        if (streamDone) break;
        buffer += decoder.decode(value, { stream: true });
        const frames = buffer.split('\n\n');
        buffer = frames.pop() ?? '';
        for (const frame of frames) {
          const line = frame.trim();
          if (!line.startsWith('data:')) continue;
          const payload = line.slice(5).trim();
          if (!payload) continue;
          try {
            applyEvent(JSON.parse(payload));
          } catch {
            /* 忽略单条解析失败 */
          }
        }
      }
      setTestAllMsg({ total, ok, fail });
      fetchData();
    } catch {
      setError('批量测试失败');
    } finally {
      setTestingAll(false);
    }
  };

  // 真实多模态探测：发一张小图给上游，判定该模型是否支持视觉输入。
  // 探测结果（capabilities / probed_at）由后端写库，前端刷新模型列表即可拿到。
  const probeSingleModel = async (modelId: string) => {
    if (!id || probingModelId === modelId) return;
    setProbingModelId(modelId);
    try {
      await api.post(`/providers/${id}/models/probe`, { model_id: modelId });
      fetchData(); // 重新拉取模型，更新 capabilities 与 probed_at
    } catch {
      /* 忽略单模型探测失败 */
    } finally {
      setProbingModelId(null);
    }
  };

  const probeAllModels = async () => {
    if (!id || probingAll) return;
    setProbingAll(true);
    setProbeAllMsg(null);
    try {
      const { data } = await api.post<{ total: number; vision_count: number; text_count: number; unknown_count: number }>(
        `/providers/${id}/models/probe-all`,
      );
      setProbeAllMsg({
        total: data.total,
        vision: data.vision_count,
        text: data.text_count,
        unknown: data.unknown_count,
      });
      fetchData();
    } catch {
      setError('批量探测失败');
    } finally {
      setProbingAll(false);
    }
  };

  const handleToggle = async () => {
    if (!provider) return;
    try {
      await api.put(`/providers/${provider.id}`, {
        name: provider.name,
        base_url: provider.base_url,
        api_type: provider.api_type,
        priority: provider.priority,
        enabled: provider.enabled !== 1,
      });
      fetchData();
    } catch {
      setError('更新状态失败');
    }
  };

  const openEdit = () => {
    if (!provider) return;
    setForm({
      name: provider.name,
      base_url: provider.base_url,
      api_type: provider.api_type,
      priority: provider.priority,
    });
    setShowModal(true);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!provider) return;
    try {
      await api.put(`/providers/${provider.id}`, form);
      setShowModal(false);
      fetchData();
    } catch {
      setError('保存失败');
    }
  };

  const copyUrl = async () => {
    if (!provider) return;
    try {
      await navigator.clipboard.writeText(provider.base_url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* 忽略复制失败 */
    }
  };

  const handleTest = async () => {
    if (!provider || !accForm.api_key) {
      setTestResult({ ok: false, message: '请先填写 API Key' });
      return;
    }
    setTesting(true);
    setTestResult(null);
    try {
      const extraConfig =
        accForm.account_id.trim() !== ''
          ? JSON.stringify({ accountId: accForm.account_id.trim() })
          : '';
      const { data } = await api.post('/accounts/test', {
        provider_id: provider.id,
        api_key: accForm.api_key,
        extra_config: extraConfig,
      });
      setTestResult({
        ok: !!data.ok,
        message: data.message || (data.ok ? '连接成功' : '连接失败'),
      });
    } catch {
      setTestResult({ ok: false, message: '测试请求失败' });
    } finally {
      setTesting(false);
    }
  };

  const handleToggleAccount = async (accountId: number, currentEnabled: number) => {
    try {
      await api.put(`/accounts/${accountId}`, { enabled: currentEnabled === 1 ? false : true });
      fetchData();
    } catch {
      setError('更新账户状态失败');
    }
  };

  const handleEditAccount = async (accountId: number) => {
    try {
      const target = accounts.find((a) => a.id === accountId);
      if (!target) {
        setError('未找到该账户');
        return;
      }
      setEditingAccId(accountId);
      let accountIdFromExtra = '';
      if (target.extra_config) {
        try {
          const extra = JSON.parse(target.extra_config);
          accountIdFromExtra = extra.accountId || '';
        } catch {
          // ignore
        }
      }
      let fullKey = target.api_key || '';
      try {
        const keyRes = await api.get<{ api_key: string }>(`/accounts/${accountId}/key`);
        if (keyRes.data.api_key) fullKey = keyRes.data.api_key;
      } catch {
        // fallback to existing value
      }
      setAccForm({
        name: target.name,
        api_key: fullKey,
        account_id: accountIdFromExtra,
        rate_limit_rpm: target.rate_limit_rpm || 0,
        rate_limit_tpm: target.rate_limit_tpm || 0,
        extra_config: target.extra_config || '',
      });
      setShowAccountModal(true);
    } catch {
      setError('加载账户信息失败');
    }
  };

  const handleUpdateAccount = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingAccId) return;
    try {
      const extraConfig =
        accForm.account_id.trim() !== ''
          ? JSON.stringify({ accountId: accForm.account_id.trim() })
          : '';
      await api.put(`/accounts/${editingAccId}`, {
        name: accForm.name,
        api_key: accForm.api_key,
        extra_config: extraConfig,
        rate_limit_rpm: accForm.rate_limit_rpm,
        rate_limit_tpm: accForm.rate_limit_tpm,
      });
      setShowAccountModal(false);
      setEditingAccId(null);
      setAccForm({ name: '', api_key: '', account_id: '', rate_limit_rpm: 0, rate_limit_tpm: 0, extra_config: '' });
      fetchData();
    } catch {
      setError('更新账户失败');
    }
  };

  const handleDeleteAccount = async (accountId: number) => {
    const target = accounts.find((a) => a.id === accountId);
    if (!target) return;
    const confirmed = window.prompt('请输入账户名称以确认删除：');
    if (confirmed === null) return;
    if (confirmed !== target.name) {
      alert('名称不匹配，无法删除');
      return;
    }
    try {
      await api.delete(`/accounts/${accountId}`);
      fetchData();
    } catch {
      setError('删除账户失败');
    }
  };

  const handleCreateAccount = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!provider) return;
    try {
      // Cloudflare 等「ID + Key」服务商：把 Account ID 组装进 extra_config（JSON 字符串）
      const extraConfig =
        accForm.account_id.trim() !== ''
          ? JSON.stringify({ accountId: accForm.account_id.trim() })
          : '';
      await api.post('/accounts', {
        provider_id: provider.id,
        name: accForm.name,
        api_key: accForm.api_key,
        extra_config: extraConfig,
      });
      setShowAccountModal(false);
      setEditingAccId(null);
      setAccForm({ name: '', api_key: '', account_id: '', rate_limit_rpm: 0, rate_limit_tpm: 0, extra_config: '' });
      setShowKey(false);
      setTestResult(null);
      fetchData();
    } catch {
      setError('创建账户失败');
    }
  };

  if (loading) {
    return (
      <div className="space-y-4 animate-pulse">
        <div className="h-8 w-48 bg-gray-200 rounded-lg" />
        <div className="h-40 bg-gray-200 rounded-xl" />
        <div className="h-40 bg-gray-200 rounded-xl" />
      </div>
    );
  }

  if (!provider) {
    return (
      <div className="space-y-4">
        <button
          onClick={() => navigate('/providers')}
          className="flex items-center gap-2 text-sm text-gray-500 hover:text-gray-800 transition-colors"
        >
          <ArrowLeft className="w-4 h-4" />
          返回服务商列表
        </button>
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 py-16 text-center text-gray-400 text-sm">
          未找到该服务商
        </div>
      </div>
    );
  }

  const health = provider.health_status || provider.health || 'unknown';
  const active = provider.enabled === 1;

  return (
    <div className="space-y-6">
      <button
        onClick={() => navigate('/providers')}
        className="flex items-center gap-2 text-sm text-gray-500 hover:text-gray-800 transition-colors"
      >
        <ArrowLeft className="w-4 h-4" />
        返回服务商列表
      </button>

      {error && (
        <div className="bg-red-50 border border-red-200 rounded-xl p-4 text-red-700 text-sm">{error}</div>
      )}

      {/* 概览卡片 */}
      <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
        <div className="flex items-start justify-between gap-4 flex-wrap">
          <div className="flex items-center gap-3 min-w-0">
            <span className={`w-3 h-3 rounded-full shrink-0 ${healthDot[health] || 'bg-gray-300'}`} />
            <div className="min-w-0">
              <h2 className="text-xl font-bold text-gray-900 truncate">{provider.name}</h2>
              <div className="flex items-center gap-2 mt-1.5 flex-wrap">
                <span className="inline-flex px-2 py-0.5 rounded-md bg-indigo-50 text-indigo-700 text-xs font-medium">
                  {apiTypeLabels[provider.api_type] || provider.api_type}
                </span>
                <span className={`inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${healthBadge[health] || 'bg-gray-100 text-gray-800'}`}>
                  {healthLabels[health] || health}
                </span>
                <span className={`inline-flex px-2 py-0.5 rounded-md text-xs font-medium ${active ? 'bg-green-50 text-green-700' : 'bg-gray-100 text-gray-500'}`}>
                  {active ? '已启用' : '已停用'}
                </span>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2 shrink-0">
            <button
              onClick={handleHealthCheck}
              disabled={healthChecking}
              className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-600 bg-gray-100 rounded-lg hover:bg-gray-200 transition-colors disabled:opacity-50"
            >
              <Activity className="w-4 h-4" />
              {healthChecking ? '检测中…' : '健康检查'}
            </button>
            {isAdmin && (
              <>
                <button
                  onClick={handleToggle}
                  className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-600 bg-gray-100 rounded-lg hover:bg-gray-200 transition-colors"
                  title={active ? '停用' : '启用'}
                >
                  {active ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                  {active ? '停用' : '启用'}
                </button>
                <button
                  onClick={openEdit}
                  className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-700 transition-colors"
                >
                  <Pencil className="w-4 h-4" />
                  编辑
                </button>
              </>
            )}
          </div>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-6">
          <div>
            <p className="text-xs text-gray-400 mb-1">API 地址</p>
            <div className="flex items-center gap-2">
              <code className="flex-1 text-xs font-mono text-gray-700 bg-gray-50 border border-gray-200 rounded-lg px-3 py-2 break-all">
                {provider.base_url}
              </code>
              <button
                onClick={copyUrl}
                className="p-2 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-100 transition-colors shrink-0"
                title="复制地址"
              >
                {copied ? <Check className="w-4 h-4 text-green-600" /> : <Copy className="w-4 h-4" />}
              </button>
            </div>
          </div>
          <div>
            <p className="text-xs text-gray-400 mb-1">优先级</p>
            <p className="text-sm font-medium text-gray-900 bg-gray-50 border border-gray-200 rounded-lg px-3 py-2">
              {provider.priority}
            </p>
          </div>
        </div>

        {healthMsg && (
          <div className={`mt-4 rounded-lg p-3 text-sm ${healthMsg.includes('成功') || healthMsg.includes('状态码 2') ? 'bg-green-50 border border-green-200 text-green-700' : 'bg-red-50 border border-red-200 text-red-700'}`}>
            {healthMsg}
          </div>
        )}
      </div>

      {/* 关联账户 */}
      <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-base font-semibold text-gray-900">关联账户</h3>
            <div className="flex items-center gap-3">
              <span className="text-xs text-gray-400">{accounts.length} 个</span>
              <button
                onClick={() => {
                  setAccForm({ name: '', api_key: '', account_id: '', rate_limit_rpm: 0, rate_limit_tpm: 0, extra_config: '' });
                  setEditingAccId(null);
                  setShowKey(false);
                  setTestResult(null);
                  setShowAccountModal(true);
                }}
                className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-700 transition-colors"
              >
                <Plus className="w-4 h-4" />
                添加账户
              </button>
            </div>
          </div>
        {accounts.length === 0 ? (
          <p className="text-sm text-gray-400 py-4 text-center">该服务商下暂无账户</p>
        ) : (
          <div className="space-y-2">
            {accounts.map((a) => {
              const showFull = visibleKeyId === a.id;
              const fullKey = fullKeys[a.id] || '';
              const displayKey = showFull ? (fullKey || a.api_key_prefix || '') : (a.api_key_prefix ? `••••${a.api_key_prefix}` : '无密钥');
              return (
              <div
                key={a.id}
                className={`flex items-center justify-between gap-3 border border-gray-100 rounded-lg px-4 py-3 ${a.enabled !== 1 ? 'opacity-60 bg-gray-50' : ''}`}
              >
                <div className="flex items-center gap-3 min-w-0">
                  <Key className="w-4 h-4 text-gray-400 shrink-0" />
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <p className="text-sm font-medium text-gray-900 truncate">{a.name}</p>
                      {a.enabled !== 1 && (
                        <span className="px-1.5 py-0.5 text-[11px] font-medium text-gray-500 bg-gray-200 rounded shrink-0">已停用</span>
                      )}
                    </div>
                    <div className="flex items-center gap-1.5">
                      <p className={`text-xs font-mono truncate ${showFull && fullKey ? 'text-indigo-700' : 'text-gray-400'}`}>
                        {displayKey}
                      </p>
                      {isAdmin && (
                        <button
                          type="button"
                          onClick={() => showFullKey(a.id)}
                          className="p-0.5 text-gray-300 hover:text-indigo-600 transition-colors shrink-0"
                          title={showFull ? '隐藏密钥' : '显示完整密钥'}
                        >
                          {showFull ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
                        </button>
                      )}
                      {(a.api_key_prefix || fullKey) && (
                        <button
                          type="button"
                          onClick={() => copyAccKey(a.id)}
                          className="p-0.5 text-gray-300 hover:text-indigo-600 transition-colors shrink-0"
                          title={showFull ? '复制完整密钥' : '复制密钥'}
                        >
                          {copiedAccId === a.id ? (
                            <Check className="w-3.5 h-3.5 text-emerald-600" />
                          ) : (
                            <Copy className="w-3.5 h-3.5" />
                          )}
                        </button>
                      )}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  {isAdmin && (
                    <button
                      onClick={() => handleToggleAccount(a.id, a.enabled ?? 0)}
                      className={`relative inline-flex h-[18px] w-[30px] items-center rounded-full transition-colors ${a.enabled === 1 ? 'bg-emerald-500' : 'bg-gray-300'}`}
                      title={a.enabled === 1 ? '点击停用' : '点击启用'}
                    >
                      <span
                        className={`inline-block h-[14px] w-[14px] transform rounded-full bg-white shadow transition-transform ${a.enabled === 1 ? 'translate-x-[12px]' : 'translate-x-0.5'}`}
                      />
                    </button>
                  )}
                  {isAdmin && (
                    <button
                      onClick={() => handleEditAccount(a.id)}
                      className="p-1.5 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-100 transition-colors"
                      title="编辑账户"
                    >
                      <Pencil className="w-4 h-4" />
                    </button>
                  )}
                  {isAdmin && (
                    <button
                      onClick={() => handleDeleteAccount(a.id)}
                      className="p-1.5 text-gray-400 hover:text-red-600 rounded-lg hover:bg-red-50 transition-colors"
                      title="删除账户"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  )}
                </div>
              </div>
              );
            })}
          </div>
        )}
      </div>

      {/* 模型 */}
      <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
        <div className="flex items-center justify-between mb-4 flex-wrap gap-3">
          <h3 className="text-base font-semibold text-gray-900">模型</h3>
          <div className="flex items-center gap-3">
            <span className="text-xs text-gray-400">{models.length} 个</span>
            {isAdmin && (
              <>
                <button
                  onClick={testAllModels}
                  disabled={testingAll || models.length === 0}
                  className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-emerald-600 rounded-lg hover:bg-emerald-700 transition-colors disabled:opacity-50"
                >
                  {testingAll ? <Loader2 className="w-4 h-4 animate-spin" /> : <Activity className="w-4 h-4" />}
                  {testingAll ? '测试中…' : '全部测试'}
                </button>
                <button
                  onClick={probeAllModels}
                  disabled={probingAll || models.length === 0}
                  className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-violet-600 rounded-lg hover:bg-violet-700 transition-colors disabled:opacity-50"
                  title="发送真实小图给上游，判定每个模型是否支持视觉输入"
                >
                  {probingAll ? <Loader2 className="w-4 h-4 animate-spin" /> : <Scan className="w-4 h-4" />}
                  {probingAll ? '探测中…' : '全部探测能力'}
                </button>
                <button
                  onClick={handleFetchModels}
                  disabled={fetchingModels}
                  className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-700 transition-colors disabled:opacity-50"
                >
                  <Activity className="w-4 h-4" />
                  {fetchingModels ? '获取中…' : '自动获取模型'}
                </button>
              </>
            )}
          </div>
        </div>

        {modelMsg && (
          <div className={`mb-4 rounded-lg p-3 text-sm ${modelMsg.includes('成功') ? 'bg-green-50 border border-green-200 text-green-700' : 'bg-red-50 border border-red-200 text-red-700'}`}>
            {modelMsg}
          </div>
        )}

        {testingAll && testProgress && testProgress.total > 0 && (
          <div className="mb-4 rounded-lg border border-emerald-200 bg-emerald-50/60 p-3">
            <div className="flex items-center justify-between gap-3 mb-1.5">
              <span className="flex items-center gap-1.5 text-sm text-emerald-700 min-w-0">
                <Loader2 className="w-3.5 h-3.5 animate-spin shrink-0" />
                <span className="truncate">
                  正在测试{testProgress.current ? `：${testProgress.current}` : '…'}
                </span>
              </span>
              <span className="text-xs text-gray-500 shrink-0">
                {testProgress.done}/{testProgress.total} · 成功 <b className="text-emerald-600">{testProgress.ok}</b> · 失败{' '}
                <b className="text-red-500">{testProgress.fail}</b>
              </span>
            </div>
            <div className="h-2 bg-gray-200/70 rounded-full overflow-hidden">
              <div
                className="h-full bg-emerald-500 rounded-full transition-all duration-300"
                style={{ width: `${Math.min(100, Math.round((testProgress.done / testProgress.total) * 100))}%` }}
              />
            </div>
          </div>
        )}

        {testAllMsg && (
          <div className="mb-4 rounded-lg p-3 text-sm bg-emerald-50 border border-emerald-200 text-emerald-700">
            全部测试完成：成功 <b>{testAllMsg.ok}</b> 个，失败 <b>{testAllMsg.fail}</b> 个，共 {testAllMsg.total} 个
          </div>
        )}

        {probeAllMsg && (
          <div className="mb-4 rounded-lg p-3 text-sm bg-violet-50 border border-violet-200 text-violet-700">
            探测完成：共 <b>{probeAllMsg.total}</b> 个 · 支持视觉 <b>{probeAllMsg.vision}</b> 个 · 纯文本 <b>{probeAllMsg.text}</b> 个 · 不可结论 <b>{probeAllMsg.unknown}</b> 个
          </div>
        )}

        {models.length === 0 ? (
          <p className="text-sm text-gray-400 py-4 text-center">
            暂无模型，点击右上角「自动获取模型」从该服务商拉取（需先配置有效账户）
          </p>
        ) : (
          <div className="space-y-5">
            {(() => {
              const added = models.filter((m) => m.enabled === 1);
              const notAdded = models.filter((m) => m.enabled !== 1);
              const renderCard = (m: ProviderModel) => (
                <div
                  key={m.model_id}
                  className="group flex items-center gap-2 border border-gray-100 rounded-lg px-3 py-2 hover:border-gray-200 hover:bg-gray-50/60 transition-colors"
                >
                  {isAdmin && (
                    <button
                      onClick={() => testSingleModel(m.model_id)}
                      disabled={testingModelId === m.model_id}
                      className="p-1 text-gray-300 hover:text-indigo-600 rounded transition-colors shrink-0"
                      title="测试该模型连通性"
                    >
                      {testingModelId === m.model_id ? (
                        <Loader2 className="w-3.5 h-3.5 animate-spin text-indigo-500" />
                      ) : (
                        <Zap className="w-3.5 h-3.5" />
                      )}
                    </button>
                  )}
                  {isAdmin && (
                    <button
                      onClick={() => probeSingleModel(m.model_id)}
                      disabled={probingModelId === m.model_id}
                      className="p-1 text-gray-300 hover:text-violet-600 rounded transition-colors shrink-0"
                      title="探测该模型是否支持视觉输入"
                    >
                      {probingModelId === m.model_id ? (
                        <Loader2 className="w-3.5 h-3.5 animate-spin text-violet-500" />
                      ) : (
                        <Scan className="w-3.5 h-3.5" />
                      )}
                    </button>
                  )}
                  <div className="min-w-0 flex-1">
                    <p className="text-xs font-mono text-gray-800 truncate leading-tight" title={m.display_name || m.model_id}>
                      {m.display_name || m.model_id}
                    </p>
                    <div className="flex items-center gap-1 flex-wrap mt-0.5">
                      {(m.capabilities && m.capabilities.length > 0) ? (
                        m.capabilities.map((cap) => (
                          <span
                            key={cap}
                            title={isCapabilityMuted(cap, m.capabilities) ? '文本为所有模型的基线能力，已弱化显示' : undefined}
                            className={`inline-flex px-1.5 py-0.5 rounded text-[10px] font-medium ${capabilityBadgeClass(cap, m.capabilities)}`}
                          >
                            {capabilityLabels[cap] || cap}
                          </span>
                        ))
                      ) : (
                        <span className="text-[10px] text-gray-300">未识别能力</span>
                      )}
                      {m.probed_at ? (
                        <span className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[10px] font-medium bg-emerald-50 text-emerald-600" title={`已于 ${m.probed_at} 完成真实探测`}>
                          <Check className="w-2.5 h-2.5" />
                          已实测
                        </span>
                      ) : null}
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    {isAdmin && (
                      <button
                        onClick={() => toggleModel(m.model_id, m.enabled === 1 ? 0 : 1)}
                        className={`relative inline-flex h-4.5 w-7.5 items-center rounded-full transition-colors ${m.enabled === 1 ? 'bg-emerald-500' : 'bg-gray-300'}`}
                        style={{ width: '30px', height: '18px' }}
                        title={m.enabled === 1 ? '点击停用' : '点击启用'}
                      >
                        <span
                          className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform ${m.enabled === 1 ? 'translate-x-[12px]' : 'translate-x-0.5'}`}
                          style={{ width: '14px', height: '14px' }}
                        />
                      </button>
                    )}
                    <button
                      onClick={() => copyModel(m.model_id)}
                      className="p-1 text-gray-300 group-hover:text-indigo-500 rounded transition-colors opacity-0 group-hover:opacity-100"
                      title="复制模型名"
                    >
                      {copiedModel === m.model_id ? (
                        <Check className="w-3.5 h-3.5 text-emerald-600 opacity-100" />
                      ) : (
                        <Copy className="w-3.5 h-3.5" />
                      )}
                    </button>
                  </div>
                </div>
              );
              return (
                <>
                  <div>
                    <p className="text-xs font-medium text-emerald-600 mb-2">已添加（{added.length}）</p>
                    {added.length === 0 ? (
                      <p className="text-xs text-gray-400 py-2">暂无已添加的模型，点击「全部测试」或逐个测试后，连通成功的模型会出现在这里</p>
                    ) : (
                      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-1.5">{added.map(renderCard)}</div>
                    )}
                  </div>
                  {notAdded.length > 0 && (
                    <div>
                      <p className="text-xs font-medium text-gray-400 mb-2">未添加（{notAdded.length}）</p>
                      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-1.5">{notAdded.map(renderCard)}</div>
                    </div>
                  )}
                </>
              );
            })()}
          </div>
        )}
      </div>

      {showModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-white rounded-xl shadow-xl p-6 w-full max-w-md">
            <h3 className="text-lg font-semibold text-gray-900 mb-4">编辑提供商</h3>
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
                  更新
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {showAccountModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-white rounded-xl shadow-xl p-6 w-full max-w-md">
            <h3 className="text-lg font-semibold text-gray-900 mb-4">{editingAccId ? '编辑账户' : '添加账户'}</h3>
            <form onSubmit={editingAccId ? handleUpdateAccount : handleCreateAccount} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">名称</label>
                <input
                  type="text"
                  value={accForm.name}
                  onChange={(e) => setAccForm({ ...accForm, name: e.target.value })}
                  placeholder="例如：我的 OpenAI 密钥"
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">API Key</label>
                <div className="relative">
                  <input
                    type={showKey ? 'text' : 'password'}
                    value={accForm.api_key}
                    onChange={(e) => setAccForm({ ...accForm, api_key: e.target.value })}
                    placeholder="sk-... 或 pk-... 格式的 API Key"
                    className="w-full px-3 py-2 pr-20 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 font-mono"
                    required
                  />
                  <div className="absolute right-1.5 top-1/2 -translate-y-1/2 flex items-center gap-0.5">
                    <button
                      type="button"
                      onClick={copyApiKey}
                      className="p-1 text-gray-400 hover:text-indigo-600 transition-colors"
                      title="复制密钥"
                    >
                      {copiedKey ? <Check className="w-4 h-4 text-emerald-600" /> : <Copy className="w-4 h-4" />}
                    </button>
                    <button
                      type="button"
                      onClick={() => setShowKey((v) => !v)}
                      className="p-1 text-gray-400 hover:text-indigo-600 transition-colors"
                      title={showKey ? '隐藏' : '显示'}
                    >
                      {showKey ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                    </button>
                  </div>
                </div>
              </div>

              {needsAccountID && (
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Account ID
                    <span className="ml-1 text-xs font-normal text-gray-400">
                      （{provider?.api_type === 'cloudflare' ? 'Cloudflare' : '服务商'} 控制台获取的账户 ID）
                    </span>
                  </label>
                  <input
                    type="text"
                    value={accForm.account_id}
                    onChange={(e) => setAccForm({ ...accForm, account_id: e.target.value })}
                    placeholder="例如：a1b2c3d4e5f6..."
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 font-mono"
                    required
                  />
                  <p className="mt-1 text-xs text-gray-400">
                    该服务商的 API 地址包含 <code>{'{accountId}'}</code> 占位符，会用此 Account ID 替换。
                  </p>
                </div>
              )}

              {editingAccId && (
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-1">RPM 限制</label>
                    <input
                      type="number"
                      value={accForm.rate_limit_rpm}
                      onChange={(e) => setAccForm({ ...accForm, rate_limit_rpm: Number(e.target.value) })}
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                      min="0"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-1">TPM 限制</label>
                    <input
                      type="number"
                      value={accForm.rate_limit_tpm}
                      onChange={(e) => setAccForm({ ...accForm, rate_limit_tpm: Number(e.target.value) })}
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                      min="0"
                    />
                  </div>
                </div>
              )}

              {testResult && (
                <div
                  className={`rounded-lg p-3 text-sm ${
                    testResult.ok
                      ? 'bg-green-50 border border-green-200 text-green-700'
                      : 'bg-red-50 border border-red-200 text-red-700'
                  }`}
                >
                  {testResult.ok ? '✓ ' : '✗ '}
                  {testResult.message}
                </div>
              )}

              <div className="flex items-center justify-between gap-3 pt-2">
                <button
                  type="button"
                  onClick={handleTest}
                  disabled={testing}
                  className="px-4 py-2 text-sm font-medium text-indigo-600 bg-indigo-50 rounded-lg hover:bg-indigo-100 transition-colors disabled:opacity-50"
                >
                  {testing ? '测试中…' : '测试连通'}
                </button>
                <div className="flex items-center gap-3">
                  <button
                    type="button"
                    onClick={() => {
                      setShowAccountModal(false);
                      setEditingAccId(null);
                    }}
                    className="px-4 py-2 text-sm font-medium text-gray-700 bg-gray-100 rounded-lg hover:bg-gray-200 transition-colors"
                  >
                    取消
                  </button>
                  <button
                    type="submit"
                    className="px-4 py-2 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-700 transition-colors"
                  >
                    {editingAccId ? '更新' : '创建'}
                  </button>
                </div>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
