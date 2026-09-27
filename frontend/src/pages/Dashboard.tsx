import { useState, useEffect } from 'react';
import { BarChart3, Zap, Coins, Maximize2 } from 'lucide-react';
import api from '../api/client';
import type { DashboardStats } from '../types';

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [rtkStats, setRtkStats] = useState<any>(null);
  const [rtkLoading, setRtkLoading] = useState(false);

  const fetchStats = async () => {
    try {
      const { data } = await api.get<DashboardStats>('/dashboard/stats');
      setStats(data);
      setError('');
    } catch {
      setError('加载仪表盘统计失败');
    } finally {
      setLoading(false);
    }
  };

  const fetchRTKStats = async () => {
    try {
      setRtkLoading(true);
      const { data } = await api.get<{ data: any }>('/admin/rtk/stats');
      setRtkStats(data.data);
    } catch (err) {
      console.error('Failed to fetch RTK stats', err);
    } finally {
      setRtkLoading(false);
    }
  };

  useEffect(() => {
    fetchStats();
    fetchRTKStats();
    const interval = setInterval(fetchStats, 30000);
    const rtkInterval = setInterval(fetchRTKStats, 60000);
    return () => {
      clearInterval(interval);
      clearInterval(rtkInterval);
    };
  }, []);

  if (loading) {
    return (
      <div className="space-y-6">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
          {[...Array(3)].map((_, i) => (
            <div key={i} className="bg-white rounded-xl shadow-sm p-6 animate-pulse">
              <div className="h-4 bg-gray-200 rounded w-24 mb-3" />
              <div className="h-8 bg-gray-200 rounded w-32" />
            </div>
          ))}
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="bg-red-50 border border-red-200 rounded-xl p-6 text-red-700">
        {error}
      </div>
    );
  }

  if (!stats) return null;

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
        <div className="bg-white rounded-xl shadow-sm p-6">
          <div className="flex items-center gap-3 mb-4">
            <div className="p-2 bg-indigo-100 rounded-lg">
              <BarChart3 className="w-5 h-5 text-indigo-600" />
            </div>
            <span className="text-sm text-gray-500 font-medium">今日请求数</span>
          </div>
          <p className="text-3xl font-bold text-gray-900">
            {stats.today_requests.toLocaleString()}
          </p>
        </div>

        <div className="bg-white rounded-xl shadow-sm p-6">
          <div className="flex items-center gap-3 mb-4">
            <div className="p-2 bg-violet-100 rounded-lg">
              <Zap className="w-5 h-5 text-violet-600" />
            </div>
            <span className="text-sm text-gray-500 font-medium">今日 Token 数</span>
          </div>
          <p className="text-3xl font-bold text-gray-900">
            {stats.today_tokens.toLocaleString()}
          </p>
        </div>

        <div className="bg-white rounded-xl shadow-sm p-6">
          <div className="flex items-center gap-3 mb-4">
            <div className="p-2 bg-emerald-100 rounded-lg">
              <Coins className="w-5 h-5 text-emerald-600" />
            </div>
            <span className="text-sm text-gray-500 font-medium">今日成本</span>
          </div>
          <p className="text-3xl font-bold text-gray-900">
            ${stats.today_cost.toFixed(4)}
          </p>
        </div>
      </div>

      <div className="bg-white rounded-xl shadow-sm p-6">
        <h3 className="text-lg font-semibold text-gray-900 mb-4">提供商明细</h3>
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-gray-200">
                <th className="text-left py-3 px-4 text-sm font-medium text-gray-500">提供商</th>
                <th className="text-right py-3 px-4 text-sm font-medium text-gray-500">请求数</th>
                <th className="text-right py-3 px-4 text-sm font-medium text-gray-500">Token 数</th>
                <th className="text-right py-3 px-4 text-sm font-medium text-gray-500">成本</th>
              </tr>
            </thead>
            <tbody>
              {stats.provider_breakdown.map((item) => (
                <tr key={item.provider} className="border-b border-gray-100 hover:bg-gray-50">
                  <td className="py-3 px-4 text-sm font-medium text-gray-900">{item.provider}</td>
                  <td className="py-3 px-4 text-sm text-gray-600 text-right">{item.requests}</td>
                  <td className="py-3 px-4 text-sm text-gray-600 text-right">{item.tokens.toLocaleString()}</td>
                  <td className="py-3 px-4 text-sm text-gray-600 text-right">${item.cost.toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* RTK 压缩统计 */}
      {rtkStats && (
        <div className="bg-gradient-to-br from-indigo-50 to-purple-50 rounded-xl shadow-sm p-6">
          <div className="flex items-center justify-between mb-4">
            <div className="flex items-center gap-3">
              <div className="p-2 bg-indigo-100 rounded-lg">
                <Maximize2 className="w-5 h-5 text-indigo-600" />
              </div>
              <h3 className="text-lg font-semibold text-gray-900">RTK 压缩统计</h3>
            </div>
            {rtkLoading && <span className="text-xs text-indigo-600">刷新中...</span>}
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <div className="bg-white rounded-lg p-4">
              <div className="text-2xl font-bold text-indigo-600">{rtkStats.total_compressions}</div>
              <div className="text-xs text-gray-500">总压缩次数</div>
            </div>
            <div className="bg-white rounded-lg p-4">
              <div className="text-2xl font-bold text-green-600">{rtkStats.total_saved_tokens}</div>
              <div className="text-xs text-gray-500">节省 Tokens</div>
            </div>
            <div className="bg-white rounded-lg p-4">
              <div className="text-2xl font-bold text-purple-600">
                {rtkStats.total_compressions > 0
                  ? Math.round((rtkStats.total_saved_tokens / Math.max(rtkStats.total_saved_tokens + rtkStats.total_compressions * 20, 1)) * 100)
                  : 0}%
              </div>
              <div className="text-xs text-gray-500">压缩率</div>
            </div>
            <div className="bg-white rounded-lg p-4">
              <div className="text-2xl font-bold text-blue-600">
                {Object.keys(rtkStats.filter_usage || {}).length}
              </div>
              <div className="text-xs text-gray-500">活跃过滤器</div>
            </div>
          </div>
          {rtkStats.filter_usage && Object.keys(rtkStats.filter_usage).length > 0 && (
            <div className="mt-4">
              <div className="text-xs text-gray-500 mb-2">过滤器使用分布</div>
              <div className="flex flex-wrap gap-2">
                {Object.entries(rtkStats.filter_usage as Record<string, number>).map(([filter, count]) => (
                  <span key={filter} className="px-3 py-1 bg-white rounded-full text-xs font-medium text-indigo-700 border border-indigo-100">
                    {filter}: {count}
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
