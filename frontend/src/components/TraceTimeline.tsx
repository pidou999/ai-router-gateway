import { useMemo } from 'react';
import type { Attempt, RequestTrace, Segment } from '../types';

// ── 辅助 ──────────────────────────────────────────────────────────────────────

function fmtMs(ms: number): string {
  if (ms < 10) return `${ms}µs`;
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

function segColor(seg: Segment): string {
  switch (seg.status) {
    case 'ok':      return 'border-emerald-400 bg-emerald-50 text-emerald-700';
    case 'error':   return 'border-rose-400 bg-rose-50 text-rose-700';
    case 'skip':    return 'border-gray-300 bg-gray-50 text-gray-400';
    default:        return 'border-gray-300 bg-white text-gray-600';
  }
}

function segIcon(seg: Segment): string {
  if (seg.status === 'ok') return '✓';
  if (seg.status === 'error') return '✗';
  if (seg.status === 'skip') return '–';
  return '?';
}

function attemptColor(a: Attempt): string {
  if (a.status_code >= 200 && a.status_code < 400) return 'bg-emerald-50 border-emerald-300 text-emerald-800';
  if (a.status_code >= 400 && a.status_code < 500) return 'bg-amber-50 border-amber-300 text-amber-800';
  if (a.status_code >= 500) return 'bg-rose-50 border-rose-300 text-rose-800';
  return 'bg-gray-50 border-gray-300 text-gray-600';
}

// ── 单段卡片 ───────────────────────────────────────────────────────────────────

interface SegCardProps {
  seg: Segment;
  idx: number;
  total: number;
}

function SegCard({ seg, idx, total }: SegCardProps) {
  const d = seg.detail ?? {};
  return (
    <div className="flex items-stretch">
      <div className={`flex-1 rounded-lg border-2 p-3 ${segColor(seg)}`}>
        {/* header */}
        <div className="flex items-center gap-2 mb-1.5">
          <span className="text-base font-bold leading-none">{segIcon(seg)}</span>
          <span className="text-xs font-semibold uppercase tracking-wider opacity-70">{seg.name}</span>
          <span className="ml-auto text-xs font-mono opacity-60">{fmtMs(seg.duration_ms)}</span>
        </div>
        {/* detail */}
        <div className="text-[11px] space-y-0.5 opacity-80">
          {d.provider_name && (
            <div>
              <span className="font-medium">服务商：</span>
              <span className="truncate block" title={String(d.provider_name)}>
                {d.provider_name}{d.api_type ? ` (${d.api_type})` : ''}
              </span>
            </div>
          )}
          {d.account_id != null && (
            <div>账号 ID：{d.account_id}</div>
          )}
          {d.status_code != null && (
            <div>
              状态码：<span className={`font-mono font-bold ${
                Number(d.status_code) >= 200 && Number(d.status_code) < 400 ? 'text-emerald-600' : 'text-rose-600'
              }`}>{d.status_code}</span>
            </div>
          )}
          {d.translated !== undefined && (
            <div>翻译：{d.translated ? '是' : '否'}</div>
          )}
          {d.req_in_bytes != null && (
            <div>请求 {d.req_in_bytes}B → 响应 {d.resp_in_bytes ?? '?'}B</div>
          )}
          {d.picked_model && d.picked_model !== d.model && (
            <div>
              <span className="text-indigo-500">路由首选：</span>
              {d.picked_model}
            </div>
          )}
        </div>
      </div>
      {/* 箭头 */}
      {idx < total - 1 && (
        <div className="flex items-center px-1 text-gray-300 select-none">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        </div>
      )}
    </div>
  );
}

// ── 尝试时间线 ─────────────────────────────────────────────────────────────────

function AttemptsTimeline({ attempts }: { attempts: Attempt[] }) {
  if (attempts.length === 0) return null;
  return (
    <div className="mt-3 pt-3 border-t border-gray-100">
      <div className="text-[11px] font-semibold text-gray-400 uppercase tracking-wider mb-2">回退尝试（{attempts.length} 次）</div>
      <div className="flex flex-wrap gap-2">
        {attempts.map((a, i) => (
          <div key={i} className={`flex items-center gap-2 rounded-md border px-2 py-1 text-xs ${attemptColor(a)}`}>
            <span className="font-mono opacity-50">#{i + 1}</span>
            <span className="font-medium max-w-[140px] truncate" title={a.provider_name}>
              {a.provider_name}
            </span>
            {a.status_code > 0 && (
              <span className="font-mono font-bold">{a.status_code}</span>
            )}
            <span className="opacity-60">{fmtMs(a.latency_ms)}</span>
            {a.error && (
              <span className="max-w-[160px] truncate opacity-70" title={a.error}>
                {a.error.length > 40 ? a.error.slice(0, 40) + '…' : a.error}
              </span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

// ── 主组件 ─────────────────────────────────────────────────────────────────────

interface TraceTimelineProps {
  trace: RequestTrace;
}

export default function TraceTimeline({ trace }: TraceTimelineProps) {
  const segments = useMemo(() => {
    const order = ['auth', 'route', 'translate', 'upstream'];
    return order
      .filter(name => trace.segments[name])
      .map(name => ({ ...trace.segments[name] })) as Segment[];
  }, [trace.segments]);

  const totalDuration = useMemo(
    () => segments.reduce((s, seg) => s + seg.duration_ms, 0),
    [segments],
  );

  return (
    <div className="mt-4 p-4 bg-slate-50 rounded-xl border border-slate-200">
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
          请求链路追踪
        </h4>
        <div className="flex items-center gap-3 text-[11px] text-slate-400">
          <span>总耗时 <strong className="text-slate-600">{fmtMs(totalDuration)}</strong></span>
          {trace.ended_at && trace.started_at && (
            <span>{new Date(trace.ended_at).toLocaleTimeString()}</span>
          )}
        </div>
      </div>

      {/* 四段时序图 */}
      <div className="flex">
        {segments.map((seg, idx) => (
          <SegCard key={seg.name} seg={{ ...seg }} idx={idx} total={segments.length} />
        ))}
      </div>

      {/* 回退尝试 */}
      {trace.attempts && trace.attempts.length > 0 && (
        <AttemptsTimeline attempts={trace.attempts} />
      )}

      {/* 全量 detail 折叠 */}
      {segments.some(s => s.detail && Object.keys(s.detail).length > 0) && (
        <details className="mt-3 text-[11px] text-slate-500">
          <summary className="cursor-pointer hover:text-slate-700 select-none">显示原始 segment 数据</summary>
          <pre className="mt-1 p-2 bg-white rounded border border-slate-200 overflow-x-auto">
            {JSON.stringify(
              Object.fromEntries(
                segments.map(s => [s.name, { status: s.status, duration_ms: s.duration_ms, ...(s.detail ?? {}) }])
              ), null, 2
            )}
          </pre>
        </details>
      )}
    </div>
  );
}
