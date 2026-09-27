import { useState, useEffect, useMemo } from 'react';
import { ChevronDown, ChevronRight, Filter } from 'lucide-react';
import api from '../api/client';
import type { RequestLog, RequestTrace } from '../types';
import TraceTimeline from '../components/TraceTimeline';

/**
 * 兼容两种后端时间格式：
 *   - 旧格式 "2026-08-06 06:33:25"（无时区，按本地时间显示）
 *   - 新格式 RFC3339 "2026-08-06T06:33:25+08:00"（带时区）
 * 旧格式被 JS 当 UTC 导致多 8h，这里补丁：无 T 分隔符时直接原样展示。
 */
function formatLogTime(s: string): string {
  if (!s) return '-';
  if (s.includes('T') || s.endsWith('Z') || /[+-]\d{2}:\d{2}$/.test(s)) {
    try { return new Date(s).toLocaleString(); } catch { return s; }
  }
  return s;
}

function parseTrace(raw: string | undefined): RequestTrace | null {
  if (!raw) return null;
  try { return JSON.parse(raw) as RequestTrace; } catch { return null; }
}

export default function Logs() {
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [filters, setFilters] = useState({ model: '', status: '', start: '', end: '' });

  const pageSize = 20;

  const fetchLogs = async () => {
    setLoading(true);
    try {
      const params: Record<string, string | number> = { page, page_size: pageSize };
      if (filters.model) params.model = filters.model;
      if (filters.status) params.status = Number(filters.status);
      if (filters.start) params.start = filters.start;
      if (filters.end) params.end = filters.end;

      const { data } = await api.get<{ data: RequestLog[]; total: number }>('/logs', { params });
      setLogs(data.data);
      setTotal(data.total);
      setError('');
    } catch {
      setError('加载日志失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchLogs();
  }, [page]);

  const applyFilters = () => {
    setPage(1);
    fetchLogs();
  };

  const totalPages = Math.ceil(total / pageSize);

  const getLatencyColor = (ms: number) => {
    if (!ms || ms < 500) return 'text-emerald-600';
    if (ms < 2000) return 'text-yellow-600';
    return 'text-rose-600';
  };

  const getStatusBadge = (status: number) => {
    if (status >= 200 && status < 400) return 'bg-emerald-100 text-emerald-700 border border-emerald-200';
    if (status >= 400 && status < 500) return 'bg-amber-100 text-amber-700 border border-amber-200';
    return 'bg-rose-100 text-rose-700 border border-rose-200';
  };

  const formatJSON = (raw?: string) => {
    if (!raw) return 'N/A';
    try {
      return JSON.stringify(JSON.parse(raw), null, 2);
    } catch {
      return raw;
    }
  };

  // 为当前页的日志预解析 trace，避免展开时重复解析
  const traceMap = useMemo(() => {
    const m = new Map<number, RequestTrace | null>();
    for (const log of logs) {
      m.set(log.id, parseTrace(log.request_details));
    }
    return m;
  }, [logs]);

  const safeCost = (log: RequestLog): number => log.cost ?? 0;

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold text-gray-900">请求日志</h2>

      {/* Filters */}
      <div className="bg-white rounded-xl shadow-sm p-4">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="block text-xs text-gray-500 mb-1">模型</label>
            <input
              type="text"
              value={filters.model}
              onChange={(e) => setFilters({ ...filters, model: e.target.value })}
              className="px-3 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 w-40"
              placeholder="gpt-4"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">状态</label>
            <select
              value={filters.status}
              onChange={(e) => setFilters({ ...filters, status: e.target.value })}
              className="px-3 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            >
              <option value="">全部</option>
              <option value="200">成功</option>
              <option value="500">错误</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">开始时间</label>
            <input
              type="datetime-local"
              value={filters.start}
              onChange={(e) => setFilters({ ...filters, start: e.target.value })}
              className="px-3 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">结束时间</label>
            <input
              type="datetime-local"
              value={filters.end}
              onChange={(e) => setFilters({ ...filters, end: e.target.value })}
              className="px-3 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            />
          </div>
          <button
            onClick={applyFilters}
            className="flex items-center gap-1.5 px-4 py-1.5 bg-indigo-600 text-white rounded-lg text-sm font-medium hover:bg-indigo-700 transition-colors"
          >
            <Filter className="w-4 h-4" />
            筛选
          </button>
        </div>
      </div>

      {error && (
        <div className="bg-red-50 border border-red-200 rounded-xl p-4 text-red-700 text-sm">{error}</div>
      )}

      {loading ? (
        <div className="space-y-2 animate-pulse">
          {[...Array(10)].map((_, i) => (
            <div key={i} className="h-12 bg-gray-200 rounded-xl" />
          ))}
        </div>
      ) : (
        <div className="bg-white rounded-xl shadow-sm overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-gray-200 bg-gray-50">
                <th className="w-8 py-3 px-4" />
                <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">ID</th>
                <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">模型</th>
                <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">服务商</th>
                <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">状态</th>
                <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">耗时</th>
                <th className="text-right py-3 px-4 text-sm font-medium text-gray-500">Token 数</th>
                <th className="text-right py-3 px-4 text-sm font-medium text-gray-500">成本</th>
                <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">时间</th>
              </tr>
            </thead>
            <tbody>
              {logs.map((log) => {
                const trace = traceMap.get(log.id);
                return (
                  <>
                    <tr
                      key={log.id}
                      className={`border-b border-gray-100 hover:bg-gray-50 cursor-pointer transition-colors ${
                        expandedId === log.id ? 'bg-indigo-50 hover:bg-indigo-50' : ''
                      }`}
                      onClick={() => setExpandedId(expandedId === log.id ? null : log.id)}
                    >
                      <td className="py-3 px-4">
                        {expandedId === log.id ? (
                          <ChevronDown className="w-4 h-4 text-indigo-400" />
                        ) : (
                          <ChevronRight className="w-4 h-4 text-gray-400" />
                        )}
                      </td>
                      <td className="py-3 px-4 text-xs font-mono text-gray-400">{log.id}</td>
                      <td className="py-3 px-4 text-sm text-gray-900 max-w-[220px]">
                        <span className="truncate block" title={log.model}>{log.model}</span>
                        {trace?.segments?.route?.detail?.picked_model && (
                          <span className="text-[11px] text-indigo-500 truncate block" title={`智能路由首选: ${trace.segments.route.detail.picked_model}`}>
                            ↑ {trace.segments.route.detail.picked_model}
                          </span>
                        )}
                      </td>
                      <td className="py-3 px-4 text-xs text-gray-500 max-w-[100px]">
                        <span className="truncate block" title={log.provider_name}>{log.provider_name || '-'}</span>
                      </td>
                      <td className="py-3 px-4">
                        <span className={`inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${getStatusBadge(log.status)}`}>
                          {log.status === 200 ? '成功' : '错误'}
                          {log.error_message && (
                            <span className="ml-1 text-[10px] opacity-70" title={log.error_message}>!</span>
                          )}
                        </span>
                      </td>
                      <td className={`py-3 px-4 text-sm font-mono ${getLatencyColor(log.latency_ms)}`}>
                        {log.latency_ms ? `${log.latency_ms}ms` : '-'}
                      </td>
                      <td className="py-3 px-4 text-sm text-gray-600 text-right">
                        {(log.prompt_tokens || 0) + (log.completion_tokens || 0)}
                      </td>
                      <td className="py-3 px-4 text-sm text-gray-600 text-right">
                        ${safeCost(log).toFixed(6)}
                      </td>
                      <td className="py-3 px-4 text-xs text-gray-500 whitespace-nowrap">
                        {formatLogTime(log.created_at)}
                      </td>
                    </tr>
                    {expandedId === log.id && (
                      <tr key={`detail-${log.id}`}>
                        <td colSpan={9} className="py-4 px-6 bg-white">
                          <div className="space-y-4">
                            {/* Trace timeline */}
                            {trace ? (
                              <TraceTimeline trace={trace} />
                            ) : (
                              <p className="text-xs text-gray-400 italic">无追踪数据</p>
                            )}

                            {/* Raw bodies */}
                            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                              {log.request_body && (
                                <div>
                                  <h4 className="text-xs font-semibold text-gray-500 uppercase mb-2">请求体</h4>
                                  <pre className="p-3 bg-gray-50 rounded-lg border border-gray-200 text-xs overflow-x-auto max-h-48 overflow-y-auto">
                                    {formatJSON(log.request_body)}
                                  </pre>
                                </div>
                              )}
                              {log.response_body && (
                                <div>
                                  <h4 className="text-xs font-semibold text-gray-500 uppercase mb-2">响应体</h4>
                                  <pre className="p-3 bg-gray-50 rounded-lg border border-gray-200 text-xs overflow-x-auto max-h-48 overflow-y-auto">
                                    {formatJSON(log.response_body)}
                                  </pre>
                                </div>
                              )}
                            </div>
                            {log.error_message && (
                              <div className="p-3 bg-rose-50 rounded-lg border border-rose-200">
                                <h4 className="text-xs font-semibold text-rose-600 uppercase mb-1">错误信息</h4>
                                <p className="text-sm text-rose-700 break-all">{log.error_message}</p>
                              </div>
                            )}
                          </div>
                        </td>
                      </tr>
                    )}
                  </>
                );
              })}
              {logs.length === 0 && (
                <tr>
                  <td colSpan={9} className="py-8 text-center text-gray-400 text-sm">
                    暂无日志
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <p className="text-sm text-gray-500">
            显示第 {(page - 1) * pageSize + 1}–{Math.min(page * pageSize, total)} 条，共 {total} 条
          </p>
          <div className="flex gap-2">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="px-3 py-1.5 text-sm font-medium border border-gray-300 rounded-lg disabled:opacity-50 hover:bg-gray-50 transition-colors"
            >
              上一页
            </button>
            <button
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
              className="px-3 py-1.5 text-sm font-medium border border-gray-300 rounded-lg disabled:opacity-50 hover:bg-gray-50 transition-colors"
            >
              下一页
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
