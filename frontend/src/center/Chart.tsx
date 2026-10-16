/** 手写 SVG 小图表（零依赖）：按日柱状图，带坐标轴刻度与悬停数值。 */
import { useMemo, useState } from 'react';
import type { DayValue } from './types';

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  const n = v / p;
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10;
  return step * p;
}

function dayLabel(ms: number): string {
  const d = new Date(ms);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

/**
 * 按日柱状图。
 * @param data 按日数据（升序）
 * @param unit 悬停提示中的单位
 */
export function DailyBars({ data, unit, label }: { data: DayValue[]; unit: string; label: string }) {
  const [hover, setHover] = useState<number | null>(null);
  const W = 600;
  const H = 160;
  const pad = { l: 36, r: 8, t: 10, b: 22 };
  const max = useMemo(() => niceMax(Math.max(0, ...data.map((d) => d.value))), [data]);
  const total = data.reduce((s, d) => s + d.value, 0);
  if (data.length === 0) return null;
  const iw = W - pad.l - pad.r;
  const ih = H - pad.t - pad.b;
  const bw = iw / data.length;
  const labelEvery = Math.ceil(data.length / 7);
  const h = hover !== null ? data[hover] : null;

  return (
    <figure className="c-chart" aria-label={`${label}，合计 ${round(total)} ${unit}`}>
      <div className="c-chart-tip" aria-live="polite">
        {h ? `${dayLabel(h.day)}：${round(h.value)} ${unit}` : `合计 ${round(total)} ${unit}`}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" onMouseLeave={() => setHover(null)}>
        {[0, 0.5, 1].map((f) => {
          const y = pad.t + ih * (1 - f);
          return (
            <g key={f}>
              <line x1={pad.l} x2={W - pad.r} y1={y} y2={y} className="c-chart-grid" />
              <text x={pad.l - 6} y={y + 4} textAnchor="end" className="c-chart-axis">
                {round(max * f)}
              </text>
            </g>
          );
        })}
        {data.map((d, i) => {
          const bh = (d.value / max) * ih;
          const x = pad.l + i * bw;
          return (
            <g key={d.day} onMouseEnter={() => setHover(i)}>
              <rect x={x} y={pad.t} width={bw} height={ih} fill="transparent" />
              <rect
                x={x + bw * 0.18}
                y={pad.t + ih - bh}
                width={bw * 0.64}
                height={Math.max(bh, d.value > 0 ? 1.5 : 0)}
                rx={Math.min(3, bw * 0.2)}
                className={`c-chart-bar ${hover === i ? 'active' : ''}`}
              />
              {i % labelEvery === 0 && (
                <text x={x + bw / 2} y={H - 6} textAnchor="middle" className="c-chart-axis">
                  {dayLabel(d.day)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
    </figure>
  );
}

function round(v: number): number {
  return Math.round(v * 100) / 100;
}
