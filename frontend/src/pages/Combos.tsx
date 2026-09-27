import { useState, useEffect } from 'react';
import { Plus, Pencil, Trash2, GripVertical, X, Sparkles, Copy, Check } from 'lucide-react';
import api from '../api/client';
import type { Combo, ComboConfig, ComboModel, ComboStrategy, ModelCapability } from '../types';
import { capabilityLabels, capabilityBadgeClass, isCapabilityMuted } from '../labels';
import ModelSelectModal from '../components/ModelSelectModal';

const VALID_NAME_REGEX = /^[a-zA-Z0-9_.\-]+$/;

const STRATEGY_OPTIONS = [
  { value: 'auto', label: 'Auto（智能路由 · 推荐）', desc: '按请求内容自动选模型：发图片走视觉模型、写代码走代码模型、超长上下文走长文本模型，其余走通用文本模型' },
  { value: 'fallback', label: 'Fallback（顺序回退）', desc: '按顺序尝试，失败时切换到下一个模型' },
  { value: 'round_robin', label: 'Round Robin（轮询）', desc: '按轮询方式分配请求到各模型' },
];

// 能力标签：智能路由据此自动分派请求；标签文案与样式统一由 ../labels 提供
const modelKey = (providerId: number | undefined, id: string) => `${providerId ?? 0}::${id}`;

export default function Combos() {
  const [combos, setCombos] = useState<Combo[]>([]);
  const [copiedComboId, setCopiedComboId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [name, setName] = useState('');
  const [nameError, setNameError] = useState('');
  const [models, setModels] = useState<ComboModel[]>([]);
  const [strategy, setStrategy] = useState<ComboStrategy>('auto');
  const [showModelPicker, setShowModelPicker] = useState(false);
  // 复合键 -> 后端推断的能力列表，用于在「自动识别」时展示实际生效的能力
  const [capMap, setCapMap] = useState<Record<string, string[]>>({});

  const fetchCombos = async () => {
    try {
      const { data } = await api.get<Combo[]>('/combos');
      // 兼容后端返回的 config 字符串或对象
      const normalized = (data || []).map((c) => ({
        ...c,
        config: normalizeConfig(c.config),
      }));
      setCombos(normalized);
      setError('');
    } catch {
      setError('加载组合失败');
    } finally {
      setLoading(false);
    }
  };

  // 拉取可用模型的能力推断结果（用于展示徽章，与后端 auto 策略使用同一套推断规则）
  const fetchCapabilities = async () => {
    try {
      const { data } = await api.get('/models');
      const map: Record<string, string[]> = {};
      for (const m of (data?.data || []) as { id: string; provider_id?: number; capabilities?: string[] }[]) {
        map[modelKey(m.provider_id, m.id)] = m.capabilities || [];
      }
      setCapMap(map);
    } catch {
      // 能力徽章为增强信息，拉取失败不影响主流程
    }
  };

  useEffect(() => {
    fetchCombos();
    fetchCapabilities();
  }, []);

  const copyCombo = async (id: number, name: string) => {
    try {
      await navigator.clipboard.writeText(name);
      setCopiedComboId(id);
      setTimeout(() => setCopiedComboId((c) => (c === id ? null : c)), 1500);
    } catch {
      /* 忽略复制失败 */
    }
  };

  // 返回某个组合条目实际生效的能力标签列表
  const effectiveCaps = (m: ComboModel): string[] => {
    if (m.capability && m.capability !== 'auto') return [m.capability];
    return capMap[modelKey(m.provider_id, m.id)] || [];
  };

  // 将后端返回的 config 统一为 ComboConfig 对象
  function normalizeConfig(raw: unknown): ComboConfig {
    if (typeof raw === 'string') {
      try {
        const parsed = JSON.parse(raw);
        return normalizeConfig(parsed);
      } catch {
        return { models: [], strategy: 'fallback' as const };
      }
    }
    const obj = raw as Record<string, unknown>;
    // 新格式
    if (Array.isArray(obj.models)) {
      const allowed: ComboStrategy[] = ['auto', 'fallback', 'round_robin'];
      const strategy = allowed.includes(obj.strategy as ComboStrategy)
        ? (obj.strategy as ComboStrategy)
        : 'fallback';
      return {
        models: obj.models.map((m: unknown) => {
          if (typeof m === 'string') return { id: m } as ComboModel;
          const item = m as { id?: string; provider_id?: number; capability?: ModelCapability };
          return { id: item.id || '', provider_id: item.provider_id, capability: item.capability } as ComboModel;
        }).filter((m) => m.id),
        strategy,
      };
    }
    // 旧格式兼容: { model_mapping: "xxx" }
    if (typeof obj.model_mapping === 'string') {
      return { models: [{ id: obj.model_mapping } as ComboModel], strategy: 'fallback' as const };
    }
    return { models: [], strategy: 'fallback' as const };
  }

  const openCreate = () => {
    setEditingId(null);
    setName('');
    setNameError('');
    setModels([]);
    setStrategy('auto');
    setShowModal(true);
  };

  const openEdit = (c: Combo) => {
    setEditingId(c.id);
    setName(c.name);
    setNameError('');
    const cfg = normalizeConfig(c.config);
    setModels(cfg.models || []);
    setStrategy(cfg.strategy || 'fallback');
    setShowModal(true);
  };

  const handleAddModels = (modelKeys: string[]) => {
    const parsed: ComboModel[] = modelKeys.map((key) => {
      const sep = key.indexOf('::');
      if (sep === -1) return { id: key };
      const pid = parseInt(key.slice(0, sep), 10);
      return { id: key.slice(sep + 2), provider_id: Number.isNaN(pid) ? undefined : pid };
    });
    setModels(prev => {
      const next = [...prev];
      for (const m of parsed) {
        const dup = next.some((x) => (x.provider_id ?? 0) === (m.provider_id ?? 0) && x.id === m.id);
        if (!dup) next.push(m);
      }
      return next;
    });
  };

  const handleRemoveModel = (idx: number) => {
    setModels(models.filter((_, i) => i !== idx));
  };

  const handleSubmit = async () => {
    setNameError('');

    if (!name.trim()) {
      setNameError('名称不能为空');
      return;
    }
    if (!VALID_NAME_REGEX.test(name)) {
      setNameError('仅允许使用字母、数字、- _ .');
      return;
    }

    const config: ComboConfig = { models, strategy };

    try {
      if (editingId) {
        await api.put(`/combos/${editingId}`, { name, config });
      } else {
        await api.post('/combos', { name, config });
      }
      setShowModal(false);
      fetchCombos();
    } catch {
      setError('保存失败');
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm('确定删除该组合吗？')) return;
    try {
      await api.delete(`/combos/${id}`);
      fetchCombos();
    } catch {
      setError('删除失败');
    }
  };

  if (loading) {
    return (
      <div className="space-y-4 animate-pulse">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="h-20 bg-gray-200 rounded-xl" />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900">组合</h2>
          <p className="text-sm text-gray-500 mt-1">将多个模型组合为一个虚拟入口，自动按策略路由</p>
        </div>
        <button
          onClick={openCreate}
          className="flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 transition-colors"
        >
          <Plus className="w-4 h-4" />
          添加组合
        </button>
      </div>

      {error && (
        <div className="bg-red-50 border border-red-200 rounded-xl p-4 text-red-700 text-sm">{error}</div>
      )}

      {/* Combo List */}
      <div className="space-y-3">
        {combos.map((c) => {
          const cfg = normalizeConfig(c.config);
          const modelList = cfg.models || [];
          return (
            <div key={c.id} className="bg-white rounded-xl shadow-sm border border-gray-100 p-5 flex items-center gap-4">
              {/* Icon */}
              <div className="w-10 h-10 rounded-lg bg-orange-50 flex items-center justify-center shrink-0">
                <span className="text-orange-500 text-xs font-bold">C</span>
              </div>

              {/* Info */}
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <h3 className="text-base font-semibold text-gray-900">{c.name}</h3>
                  {cfg.strategy === 'auto' && (
                    <span className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-indigo-50 text-indigo-600 font-medium">
                      <Sparkles className="w-3 h-3" /> 智能路由
                    </span>
                  )}
                </div>
                <p className="text-sm text-gray-500 mt-0.5 truncate">
                  {modelList.length > 0
                    ? `${modelList.map(m => m.id).join(', ')} · ${
                        cfg.strategy === 'auto' ? 'Auto' : cfg.strategy === 'round_robin' ? 'Round Robin' : 'Fallback'
                      }`
                    : '未配置模型'}
                </p>
              </div>

              {/* Actions */}
              <div className="flex items-center gap-1 shrink-0">
                <button onClick={() => copyCombo(c.id, c.name)} className="p-2 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-50" title="复制组合名">
                  {copiedComboId === c.id ? <Check className="w-4 h-4 text-emerald-600" /> : <Copy className="w-4 h-4" />}
                </button>
                <button onClick={() => openEdit(c)} className="p-2 text-gray-400 hover:text-indigo-600 rounded-lg hover:bg-gray-50" title="编辑">
                  <Pencil className="w-4 h-4" />
                </button>
                <button onClick={() => handleDelete(c.id)} className="p-2 text-gray-400 hover:text-red-600 rounded-lg hover:bg-gray-50" title="删除">
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          );
        })}
        {combos.length === 0 && (
          <div className="py-16 text-center">
            <div className="text-gray-300 mb-3 inline-block">
              <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10"/></svg>
            </div>
            <p className="text-gray-400 text-sm">暂无组合</p>
            <button onClick={openCreate} className="mt-3 text-sm text-indigo-600 hover:text-indigo-700 font-medium">创建第一个组合</button>
          </div>
        )}
      </div>

      {/* Create/Edit Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50" onClick={() => setShowModal(false)}>
          <div className="bg-white rounded-2xl shadow-2xl w-full max-w-md overflow-hidden flex flex-col max-h-[85vh]" onClick={(e) => e.stopPropagation()}>
            {/* Modal Header - macOS style dots */}
            <div className="flex items-center gap-2 px-6 py-4 border-b border-gray-100 shrink-0">
              <span className="w-3 h-3 rounded-full bg-red-400" />
              <span className="w-3 h-3 rounded-full bg-yellow-400" />
              <span className="w-3 h-3 rounded-full bg-green-400" />
              <span className="flex-1 text-center text-sm font-medium text-gray-900 pr-7">
                {editingId ? '编辑组合' : '创建组合'}
              </span>
            </div>

            <form onSubmit={(e) => { e.preventDefault(); handleSubmit(); }} className="flex-1 overflow-y-auto p-6 space-y-5 min-h-0">
              {/* Name Field */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1.5">组合名称</label>
                <input
                  type="text"
                  value={name}
                  onChange={(e) => { setName(e.target.value); setNameError(''); }}
                  placeholder="my-combo"
                  className={`w-full px-3 py-2.5 border rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 ${
                    nameError ? 'border-red-300 focus:ring-red-500' : 'border-gray-300'
                  }`}
                />
                <p className="mt-1.5 text-xs text-gray-400">仅允许使用字母、数字、- _ .</p>
                {nameError && <p className="mt-1 text-xs text-red-600">{nameError}</p>}
              </div>

              {/* Models Section */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1.5">模型</label>
                <div className="border border-gray-200 rounded-lg overflow-hidden">
                  {models.length === 0 ? (
                    /* Empty State */
                    <div className="py-10 text-center bg-gray-50">
                      <svg className="mx-auto w-8 h-8 text-gray-300 mb-2" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.5">
                        <path strokeLinecap="round" strokeLinejoin="round" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10"/>
                      </svg>
                      <p className="text-sm text-gray-400">尚未添加模型</p>
                    </div>
                  ) : (
                    /* Model List - 可滚动 */
                    <div className="divide-y divide-gray-100 max-h-[40vh] overflow-y-auto">
                      {models.map((m, idx) => (
                        <div key={modelKey(m.provider_id, m.id)} className="px-3 py-2.5 group bg-white hover:bg-gray-50">
                          <div className="flex items-center gap-2">
                            <GripVertical className="w-4 h-4 text-gray-300 cursor-grab shrink-0" />
                            <code className="flex-1 text-sm text-gray-700 truncate">{m.id}</code>
                            {m.provider_id != null && (
                              <span className="shrink-0 text-[10px] px-1.5 py-0.5 rounded bg-gray-100 text-gray-500">P{m.provider_id}</span>
                            )}
                            <button
                              type="button"
                              onClick={() => handleRemoveModel(idx)}
                              className="opacity-0 group-hover:opacity-100 p-1 text-gray-400 hover:text-red-500 transition-all"
                            >
                              <X className="w-3.5 h-3.5" />
                            </button>
                          </div>

                          {/* 智能路由：展示该模型已探测到的能力（文本为基线能力，弱化显示） */}
                          {strategy === 'auto' && (
                            <div className="mt-1.5 ml-6 flex items-center gap-1 flex-wrap">
                              {effectiveCaps(m).map((cap) => (
                                <span
                                  key={cap}
                                  title={isCapabilityMuted(cap, effectiveCaps(m)) ? '文本为所有模型的基线能力，已弱化显示' : undefined}
                                  className={`text-[10px] px-1.5 py-0.5 rounded ${capabilityBadgeClass(cap, effectiveCaps(m))}`}
                                >
                                  {capabilityLabels[cap] || cap}
                                </span>
                              ))}
                              {effectiveCaps(m).length === 0 && (
                                <span className="text-[10px] text-gray-400">未识别到特殊能力，按通用文本处理</span>
                              )}
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
                {/* Add Model Button */}
                <button
                  type="button"
                  onClick={() => setShowModelPicker(true)}
                  className="mt-2 flex items-center gap-1.5 text-sm text-orange-600 hover:text-orange-700 font-medium"
                >
                  <Plus className="w-4 h-4" /> 添加模型
                </button>
              </div>

              {/* Strategy Select */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1.5">策略</label>
                <select
                  value={strategy}
                  onChange={(e) => setStrategy(e.target.value as ComboStrategy)}
                  className="w-full px-3 py-2.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 bg-white"
                >
                  {STRATEGY_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>{opt.label}</option>
                  ))}
                </select>
                <p className="mt-1 text-xs text-gray-400">
                  {STRATEGY_OPTIONS.find(o => o.value === strategy)?.desc}
                </p>
                {strategy === 'auto' && (
                  <div className="mt-2 flex gap-2 rounded-lg bg-indigo-50 border border-indigo-100 px-3 py-2">
                    <Sparkles className="w-3.5 h-3.5 text-indigo-500 shrink-0 mt-0.5" />
                    <p className="text-xs text-indigo-700 leading-relaxed">
                      外部调用该组合时，网关会先识别请求内容：<b>含图片</b> → 视觉模型；<b>写代码/贴报错</b> → 代码模型；
                      <b>超长上下文</b> → 长上下文模型；其余走文本模型。选中的模型失败时会自动回退到组合内下一个候选。
                    </p>
                  </div>
                )}
              </div>
            </form>

            {/* Footer Buttons - 固定在底部，不随内容滚动 */}
            <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-100 shrink-0">
              <button
                type="button"
                onClick={() => setShowModal(false)}
                className="px-4 py-2 text-sm font-medium text-gray-700 bg-gray-100 rounded-lg hover:bg-gray-200 transition-colors"
              >
                取消
              </button>
              <button
                type="button"
                onClick={handleSubmit}
                disabled={!name.trim() || models.length === 0}
                className="px-5 py-2 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-700 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
              >
                {editingId ? '更新' : '创建'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Model Picker Modal */}
      <ModelSelectModal
        open={showModelPicker}
        onClose={() => setShowModelPicker(false)}
        onSelect={handleAddModels}
        excludeIds={models.map((m) => `${m.provider_id ?? 0}::${m.id}`)}
      />
    </div>
  );
}
