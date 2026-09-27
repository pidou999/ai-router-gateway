import { useState, useEffect } from 'react';
import api from '../api/client';
import type { SettingsMap } from '../types';
import type { RTKStats } from '../types/rtk';

export default function Settings() {
  const [settings, setSettings] = useState<SettingsMap>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [successMsg, setSuccessMsg] = useState('');
  const [rtkStats, setRtkStats] = useState<RTKStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(false);

  const fetchSettings = async () => {
    try {
      const { data } = await api.get<SettingsMap>('/admin/settings');
      setSettings(data);
      setError('');
    } catch (err: any) {
      if (err?.response?.status === 403) {
        setError('无访问权限：仅管理员可管理设置');
      } else {
        setError('加载设置失败');
      }
    } finally {
      setLoading(false);
    }
  };

  const fetchRTKStats = async () => {
    const enabled = Boolean(settings.rtk_compression);
    if (!enabled) return;
    setStatsLoading(true);
    try {
      const { data } = await api.get<{ data: RTKStats }>('/admin/rtk/stats');
      setRtkStats(data.data);
    } catch (err) {
      console.error('Failed to fetch RTK stats', err);
    } finally {
      setStatsLoading(false);
    }
  };

  useEffect(() => {
    fetchSettings();
  }, []);

  useEffect(() => {
    if (settings.rtk_compression) {
      fetchRTKStats();
      const interval = setInterval(fetchRTKStats, 30000);
      return () => clearInterval(interval);
    }
  }, [settings.rtk_compression]);

  const handleToggle = (key: string) => {
    setSettings((prev) => ({ ...prev, [key]: !prev[key] }));
  };

  const handleNumberChange = (key: string, value: number) => {
    setSettings((prev) => ({ ...prev, [key]: value }));
  };

  const handleSave = async () => {
    setSaving(true);
    setSuccessMsg('');
    try {
      await api.put('/admin/settings', settings);
      setSuccessMsg('设置已保存');
      // 刷新统计
      if (settings.rtk_compression) {
        fetchRTKStats();
      }
    } catch (err: any) {
      if (err?.response?.status === 403) {
        setError('无访问权限：仅管理员可管理设置');
      } else {
        setError('保存设置失败');
      }
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="space-y-4 animate-pulse">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="h-20 bg-gray-200 rounded-xl" />
        ))}
      </div>
    );
  }

  const cavemanEnabled = Boolean(settings.caveman_mode);
  const ponytailEnabled = Boolean(settings.ponytail_mode);
  const headroomEnabled = Boolean(settings.headroom_mode);
  const rtkEnabled = Boolean(settings.rtk_compression);

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-2xl font-bold text-gray-900">设置</h2>

      {error && (
        <div className="bg-red-50 border border-red-200 rounded-xl p-4 text-red-700 text-sm">{error}</div>
      )}
      {successMsg && (
        <div className="bg-green-50 border border-green-200 rounded-xl p-4 text-green-700 text-sm">{successMsg}</div>
      )}

      <div className="bg-white rounded-xl shadow-sm divide-y divide-gray-100">
        <div className="p-6">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-semibold text-gray-900">原始人模式</h3>
              <p className="text-sm text-gray-500 mt-0.5">极致简化模型行为</p>
            </div>
            <button
              onClick={() => handleToggle('caveman_mode')}
              className={`relative w-11 h-6 rounded-full transition-colors ${
                cavemanEnabled ? 'bg-indigo-600' : 'bg-gray-300'
              }`}
            >
              <span
                className={`absolute top-0.5 left-0.5 w-5 h-5 bg-white rounded-full shadow transition-transform ${
                  cavemanEnabled ? 'translate-x-5' : ''
                }`}
              />
            </button>
          </div>
          {cavemanEnabled && (
            <div className="mt-4">
              <label className="text-xs text-gray-500 mb-1 block">等级（1-5）</label>
              <div className="flex items-center gap-3">
                <input
                  type="range"
                  min={1}
                  max={5}
                  value={Number(settings.caveman_level) || 1}
                  onChange={(e) => handleNumberChange('caveman_level', Number(e.target.value))}
                  className="flex-1 accent-indigo-600"
                />
                <span className="text-sm font-mono text-gray-900 w-4 text-center">
                  {Number(settings.caveman_level) || 1}
                </span>
              </div>
            </div>
          )}
        </div>

        <div className="p-6">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-semibold text-gray-900">马尾辫模式</h3>
              <p className="text-sm text-gray-500 mt-0.5">让模型写更少的代码</p>
            </div>
            <button
              onClick={() => handleToggle('ponytail_mode')}
              className={`relative w-11 h-6 rounded-full transition-colors ${
                ponytailEnabled ? 'bg-indigo-600' : 'bg-gray-300'
              }`}
            >
              <span
                className={`absolute top-0.5 left-0.5 w-5 h-5 bg-white rounded-full shadow transition-transform ${
                  ponytailEnabled ? 'translate-x-5' : ''
                }`}
              />
            </button>
          </div>
          {ponytailEnabled && (
            <div className="mt-4">
              <label className="text-xs text-gray-500 mb-2 block">模式</label>
              <div className="grid grid-cols-3 gap-2">
                {['lite', 'full', 'ultra'].map((mode) => (
                  <button
                    key={mode}
                    onClick={() => handleNumberChange('ponytail_level', mode === 'lite' ? 1 : mode === 'full' ? 2 : 3)}
                    className={`px-3 py-2 rounded-lg border text-left transition-colors ${
                      settings.ponytail_level === mode
                        ? 'border-indigo-300 bg-indigo-50 text-indigo-700'
                        : 'border-gray-200 hover:border-gray-300'
                    }`}
                  >
                    <div className="text-xs font-medium capitalize">{mode}</div>
                    <div className="text-xs text-gray-500">
                      {mode === 'lite' ? '最小代码' : mode === 'full' ? '极致精简' : '能省则省'}
                    </div>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>

        <div className="p-6">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-semibold text-gray-900">Headroom 模式</h3>
              <p className="text-sm text-gray-500 mt-0.5">智能压缩历史消息</p>
            </div>
            <button
              onClick={() => handleToggle('headroom_mode')}
              className={`relative w-11 h-6 rounded-full transition-colors ${
                headroomEnabled ? 'bg-indigo-600' : 'bg-gray-300'
              }`}
            >
              <span
                className={`absolute top-0.5 left-0.5 w-5 h-5 bg-white rounded-full shadow transition-transform ${
                  headroomEnabled ? 'translate-x-5' : ''
                }`}
              />
            </button>
          </div>
          {headroomEnabled && (
            <div className="mt-4">
              <label className="text-xs text-gray-500 mb-1 block">压缩等级（1-5）</label>
              <div className="flex items-center gap-3">
                <input
                  type="range"
                  min={1}
                  max={5}
                  value={Number(settings.headroom_level) || 1}
                  onChange={(e) => handleNumberChange('headroom_level', Number(e.target.value))}
                  className="flex-1 accent-indigo-600"
                />
                <span className="text-sm font-mono text-gray-900 w-4 text-center">
                  {Number(settings.headroom_level) || 1}
                </span>
              </div>
            </div>
          )}
        </div>

        <div className="p-6">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-semibold text-gray-900">RTK 压缩</h3>
              <p className="text-sm text-gray-500 mt-0.5">自动检测内容类型并压缩，节省20-40% Token</p>
            </div>
            <button
              onClick={() => handleToggle('rtk_compression')}
              className={`relative w-11 h-6 rounded-full transition-colors ${
                rtkEnabled ? 'bg-indigo-600' : 'bg-gray-300'
              }`}
            >
              <span
                className={`absolute top-0.5 left-0.5 w-5 h-5 bg-white rounded-full shadow transition-transform ${
                  rtkEnabled ? 'translate-x-5' : ''
                }`}
              />
            </button>
          </div>
          {rtkEnabled && (
            <div className="mt-4 space-y-4">
              {/* 压缩等级 */}
              <div>
                <label className="text-xs text-gray-500 mb-1 block">压缩等级（1-9）</label>
                <div className="flex items-center gap-3">
                  <input
                    type="range"
                    min={1}
                    max={9}
                    value={Number(settings.rtk_level) || 1}
                    onChange={(e) => handleNumberChange('rtk_level', Number(e.target.value))}
                    className="flex-1 accent-indigo-600"
                  />
                  <span className="text-sm font-mono text-gray-900 w-4 text-center">
                    {Number(settings.rtk_level) || 1}
                  </span>
                </div>
                <p className="text-xs text-gray-400 mt-1">
                  等级越高压缩越激进，建议保持默认值
                </p>
              </div>

              {/* 过滤模式选择 */}
              <div>
                <label className="text-xs text-gray-500 mb-2 block">过滤模式</label>
                <div className="grid grid-cols-2 gap-2">
                  {[
                    { key: 'rtk_auto_detect', label: '自动检测', desc: '智能识别内容类型' },
                    { key: 'rtk_git_diff', label: 'Git Diff', desc: '优化代码差异' },
                    { key: 'rtk_build_log', label: '构建日志', desc: '过滤构建输出' },
                    { key: 'rtk_dedup', label: '去重日志', desc: '消除重复行' },
                    { key: 'rtk_smart_trunc', label: '智能截断', desc: '保留首尾内容' },
                    { key: 'rtk_ls_summary', label: '目录摘要', desc: 'ls输出汇总' },
                  ].map((item) => (
                    <button
                      key={item.key}
                      onClick={() => handleToggle(item.key)}
                      className={`px-3 py-2 rounded-lg text-left border transition-colors ${
                        settings[item.key]
                          ? 'border-indigo-300 bg-indigo-50 text-indigo-700'
                          : 'border-gray-200 hover:border-gray-300'
                      }`}
                    >
                      <div className="text-xs font-medium">{item.label}</div>
                      <div className="text-xs text-gray-500">{item.desc}</div>
                    </button>
                  ))}
                </div>
              </div>

              {/* 压缩效果预览 */}
              <div className="bg-gray-50 rounded-lg p-3 text-xs text-gray-600">
                <div className="font-medium mb-1">压缩效果说明：</div>
                <ul className="space-y-1">
                  <li>• <strong>自动检测</strong>：识别 git diff/grep/find/tree/ls 等输出</li>
                  <li>• <strong>Git Diff</strong>：限制每个 hunk 行数，折叠重复更改</li>
                  <li>• <strong>构建日志</strong>：保留 ERROR/WARN，压缩普通输出</li>
                  <li>• <strong>去重日志</strong>：合并连续重复行，显示计数</li>
                  <li>• <strong>智能截断</strong>：保留前N行和后N行，中间省略</li>
                  <li>• <strong>目录摘要</strong>：ls 输出按目录/扩展名汇总</li>
                </ul>
              </div>

              {/* 压缩统计 */}
              <div className="bg-indigo-50 rounded-lg p-4">
                <div className="flex items-center justify-between mb-3">
                  <div className="font-medium text-indigo-900">压缩统计</div>
                  {statsLoading && <span className="text-xs text-indigo-600">刷新中...</span>}
                </div>
                {rtkStats ? (
                  <div className="space-y-3">
                    <div className="grid grid-cols-2 gap-4">
                      <div className="bg-white rounded-lg p-3">
                        <div className="text-2xl font-bold text-indigo-600">{rtkStats.total_compressions}</div>
                        <div className="text-xs text-gray-500">总压缩次数</div>
                      </div>
                      <div className="bg-white rounded-lg p-3">
                        <div className="text-2xl font-bold text-green-600">{rtkStats.total_saved_tokens}</div>
                        <div className="text-xs text-gray-500">节省 Tokens</div>
                      </div>
                    </div>
                    <div>
                      <div className="text-xs text-gray-500 mb-2">过滤器使用分布</div>
                      <div className="flex flex-wrap gap-2">
                        {Object.entries(rtkStats.filter_usage).map(([filter, count]) => (
                          <span key={filter} className="px-2 py-1 bg-white rounded text-xs text-gray-700 border border-indigo-100">
                            {filter}: {count}
                          </span>
                        ))}
                      </div>
                    </div>
                    {rtkStats.recent_compressions.length > 0 && (
                      <div>
                        <div className="text-xs text-gray-500 mb-2">最近压缩记录</div>
                        <div className="space-y-1">
                          {rtkStats.recent_compressions.slice(0, 3).map((item, idx) => (
                            <div key={idx} className="bg-white rounded px-3 py-2 text-xs flex justify-between items-center">
                              <span className="text-gray-600">{item.filter}</span>
                              <span className="text-green-600 font-medium">-{item.saved_tokens} tokens</span>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                ) : (
                  <div className="text-xs text-gray-400 text-center py-2">暂无数据，发送请求后显示</div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>

      <button
        onClick={handleSave}
        disabled={saving}
        className="px-6 py-2.5 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 disabled:opacity-50 transition-colors"
      >
        {saving ? '保存中…' : '保存设置'}
      </button>
    </div>
  );
}
