import { useState } from 'react';
import type { MouseEventHandler } from 'react';
import { useStore } from '../store/useStore';
import { api } from '../api/client';
import type { PurchaseReason, RoutePath, StationStep } from '../types';

const REASON_TEXT: Record<PurchaseReason, string> = {
  'player-offline': '请先进入游戏，在线后才能交付车票',
  'invalid-route': '路线无效，请刷新地图后重试',
  'insufficient-funds': '余额不足',
  'traversal-running': '铁路系统正在遍历，请稍后再试',
  'purchase-disabled': '当前未开放网页购票',
  'internal-error': '游戏服务器无响应，请稍后再试',
};

export function RouteCard({ path, index }: { path: RoutePath; index: number }) {
  const selected = useStore((s) => s.selectedRouteIndex === index);
  const selectRoute = useStore((s) => s.selectRoute);
  const player = useStore((s) => s.player);
  const meta = useStore((s) => s.meta);
  const lines = useStore((s) => s.lines);
  const [busy, setBusy] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const lineColor = (lineId?: string) => lines.find((l) => l.id === lineId)?.color ?? '#8a90a0';

  const canPurchase = !!player && !!meta?.online;
  const purchaseHint = !player
    ? '请先登录后购票'
    : !meta?.online
      ? '游戏服务器离线，暂不可购票'
      : '';

  const purchase = async () => {
    setBusy(true);
    setMsg(null);
    try {
      const result = await api.purchase({
        nodeIds: path.nodeIds,
        lineIdSequence: path.lineIdSequence,
      });
      if (result.success) {
        setMsg(
          `购票成功：${result.ticketName ?? ''}（花费 ${result.price?.toFixed(2)}，余额 ${result.balanceAfter?.toFixed(2)}）`,
        );
      } else {
        setMsg(result.reason ? REASON_TEXT[result.reason] : '购票失败');
      }
    } catch {
      setMsg('购票请求失败');
    } finally {
      setBusy(false);
    }
  };

  return (
    <article className={`route-card ${selected ? 'selected' : ''}`} onClick={() => selectRoute(index)}>
      <div className="route-card-head">
        <span className="route-rank">路线 {index + 1}</span>
        <div className="route-metrics">
          <span className="route-distance">{path.distance.toFixed(2)} km</span>
          <span className="route-price">{path.estimatedFare.toFixed(2)} 银币</span>
        </div>
      </div>

      <StationSteps
        path={path}
        expanded={expanded}
        onToggle={(e) => {
          e.stopPropagation();
          setExpanded((v) => !v);
        }}
        lineColor={lineColor}
      />

      <div className="route-info">
        <span>共 {path.stations.length} 站</span>
      </div>

      {path.fareDetails && path.fareDetails.length > 0 && (
        <details className="fare-details" onClick={(e) => e.stopPropagation()}>
          <summary>票价详情</summary>
          {path.fareDetails.map((d) => (
            <div key={d.systemId} className="fare-row">
              <span>{d.systemName}</span>
              <span>{d.distance.toFixed(2)} km</span>
              <span>{d.price.toFixed(2)} 银币</span>
            </div>
          ))}
        </details>
      )}

      <div className="route-actions" title={purchaseHint}>
        <button
          className="btn primary"
          disabled={!canPurchase || busy}
          onClick={(e) => {
            e.stopPropagation();
            purchase();
          }}
        >
          {busy ? '购票中...' : '购票'}
        </button>
      </div>
      {msg && <div className="route-msg">{msg}</div>}
    </article>
  );
}

function StationSteps({
  path,
  expanded,
  onToggle,
  lineColor,
}: {
  path: RoutePath;
  expanded: boolean;
  onToggle: MouseEventHandler<HTMLButtonElement>;
  lineColor: (lineId?: string) => string;
}) {
  const steps: StationStep[] = path.stationSteps?.length
    ? path.stationSteps
    : path.stations.map((stationName) => ({ stationName }));
  const start = steps[0];
  const end = steps[steps.length - 1];
  const middle = steps.slice(1, -1);
  const compactMiddle = middle.length > 2 && !expanded ? middle.slice(0, 2) : middle;
  const omitted = middle.length - compactMiddle.length;

  return (
    <div className="route-stations">
      {start && <StationRow step={start} role="start" lineColor={lineColor} />}
      {middle.length > 0 && (
        <div className="station-path">
          {compactMiddle.map((step, i) => (
            <StationRow key={`${step.stationName}-${i}`} step={step} role="middle" lineColor={lineColor} />
          ))}
          {(omitted > 0 || expanded) && middle.length > 2 && (
            <button className="route-collapse" onClick={onToggle}>
              {expanded ? '收起' : `+${omitted} 站`}
            </button>
          )}
        </div>
      )}
      {end && end !== start && <StationRow step={end} role="end" lineColor={lineColor} />}
    </div>
  );
}

function StationRow({
  step,
  role,
  lineColor,
}: {
  step: StationStep;
  role: 'start' | 'middle' | 'end';
  lineColor: (lineId?: string) => string;
}) {
  const color = role === 'start' ? '#4ade80' : role === 'end' ? '#ef4444' : lineColor(step.departLineId);
  return (
    <div className={`station-item ${role}-station`}>
      <span className="route-station-dot" style={{ background: color }} />
      <span className="station-name">{step.stationName}</span>
    </div>
  );
}
