import { useState, useEffect } from 'react';
import { Search, X, Check } from 'lucide-react';
import api from '../api/client';
import type { ProviderModel } from '../types';

interface ModelSelectModalProps {
  open: boolean;
  onClose: () => void;
  onSelect: (modelKeys: string[]) => void; // 复合键数组："provider_id::model_id"
  excludeIds?: string[]; // 已添加模型的复合键，用于去重
}

// 复合键：服务商维度唯一，避免不同服务商同名模型联动
function compositeKey(m: ProviderModel): string {
  return `${m.provider_id ?? 0}::${m.model_id}`;
}

// 能力徽章：与后端 InferCapabilities 推断结果一一对应（text 为默认能力，不展示以减少噪音）
const CAP_BADGE: Record<string, { label: string; cls: string }> = {
  vision: { label: '视觉', cls: 'bg-purple-500/20 text-purple-300' },
  code: { label: '代码', cls: 'bg-blue-500/20 text-blue-300' },
  long_context: { label: '长上下文', cls: 'bg-amber-500/20 text-amber-300' },
  audio: { label: '语音', cls: 'bg-pink-500/20 text-pink-300' },
  reasoning: { label: '推理', cls: 'bg-emerald-500/20 text-emerald-300' },
};

export default function ModelSelectModal({ open, onClose, onSelect, excludeIds = [] }: ModelSelectModalProps) {
  const [models, setModels] = useState<ProviderModel[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!open) return;
    setLoading(true);
    setSearch('');
    setSelected(new Set());
    api.get('/models')
      .then(({ data }) => {
        const items: ProviderModel[] = (data.data || []).map((m: { id: string; display_name?: string; owned_by?: string; provider_id?: number; capabilities?: string[] }) => ({
          model_id: m.id,
          display_name: m.display_name || m.id,
          owned_by: m.owned_by,
          provider_id: m.provider_id,
          capabilities: m.capabilities,
        }));
        setModels(items);
      })
      .catch((err) => {
        console.error('Failed to load models:', err);
        setModels([]);
      })
      .finally(() => setLoading(false));
  }, [open]);

  if (!open) return null;

  const filtered = models.filter((m) => {
    const key = compositeKey(m);
    if (excludeIds.includes(key)) return false;
    if (search && !m.model_id.toLowerCase().includes(search.toLowerCase()) &&
        !m.display_name.toLowerCase().includes(search.toLowerCase())) return false;
    if (m.owned_by === 'combo') return false;
    return true;
  });

  // 按 owned_by（服务商）分组
  const grouped = filtered.reduce<Record<string, ProviderModel[]>>((acc, m) => {
    const key = m.owned_by || 'other';
    (acc[key] = acc[key] || []).push(m);
    return acc;
  }, {});

  const toggleSelect = (key: string) => {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  const handleConfirm = () => {
    if (selected.size > 0) {
      onSelect(Array.from(selected));
    }
    onClose();
  };

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-[100]" onClick={onClose}>
      <div className="bg-gray-900 rounded-xl shadow-2xl w-full max-w-lg max-h-[70vh] flex flex-col" onClick={(e) => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-gray-700/60">
          <h3 className="text-base font-medium text-white">选择模型</h3>
          <button onClick={onClose} className="text-gray-400 hover:text-white transition-colors">
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Search */}
        <div className="px-5 py-3">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-500" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索模型..."
              className="w-full pl-10 pr-4 py-2 bg-gray-800 border border-gray-700 rounded-lg text-sm text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
              autoFocus
            />
          </div>
        </div>

        {/* List - 暗色滚动条 */}
        <div className="flex-1 overflow-y-auto px-5 pb-5 scrollbar-dark">
          {loading ? (
            <div className="py-8 text-center text-gray-400 text-sm">加载中...</div>
          ) : filtered.length === 0 ? (
            <div className="py-8 text-center text-gray-400 text-sm">没有可用的模型</div>
          ) : (
            Object.entries(grouped).map(([group, items]) => (
              <div key={group} className="mb-4">
                <p className="text-xs font-medium text-gray-500 uppercase tracking-wider mb-2">{group}</p>
                <div className="space-y-1">
                  {items.map((m) => {
                    const key = compositeKey(m);
                    const isSelected = selected.has(key);
                    return (
                      <button
                        key={key}
                        onClick={() => toggleSelect(key)}
                        className={`w-full text-left px-3 py-2.5 rounded-lg border transition-all group flex items-center gap-2.5 ${
                          isSelected
                            ? 'bg-indigo-500/15 border-indigo-500/50'
                            : 'bg-gray-800 border-transparent hover:bg-gray-750 hover:border-gray-600'
                        }`}
                      >
                        {/* 选中指示器 */}
                        <span className={`shrink-0 w-5 h-5 rounded flex items-center justify-center transition-colors ${
                          isSelected ? 'bg-indigo-500' : 'border border-gray-500'
                        }`}>
                          {isSelected && <Check className="w-3 h-3 text-white" />}
                        </span>
                        <span className={`text-sm truncate ${isSelected ? 'text-indigo-300' : 'text-gray-200 group-hover:text-white'}`}>
                          {m.display_name}
                        </span>
                        {/* 能力徽章：智能路由据此自动分派请求 */}
                        <span className="ml-auto flex items-center gap-1 shrink-0">
                          {(m.capabilities || [])
                            .filter((c) => CAP_BADGE[c])
                            .map((c) => (
                              <span key={c} className={`text-[10px] px-1.5 py-0.5 rounded ${CAP_BADGE[c].cls}`}>
                                {CAP_BADGE[c].label}
                              </span>
                            ))}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>
            ))
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-5 py-4 border-t border-gray-700/60">
          <span className="text-xs text-gray-500">
            {selected.size > 0 ? `已选择 ${selected.size} 个模型` : '点击模型进行选择'}
          </span>
          <div className="flex gap-2">
            <button
              onClick={onClose}
              className="px-4 py-2 text-sm font-medium text-gray-400 bg-gray-800 hover:bg-gray-700 rounded-lg transition-colors"
            >
              取消
            </button>
            <button
              onClick={handleConfirm}
              disabled={selected.size === 0}
              className="px-4 py-2 text-sm font-medium text-white bg-indigo-600 hover:bg-indigo-700 rounded-lg transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            >
              确认添加
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
