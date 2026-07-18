import { useStore } from '../store/useStore';
import { avatarUrl } from '../config';
import { formatDuration } from '../format';
import type { Train } from '../types';

/** 实时列车列表：展示所有世界的实时列车卡片，点击定位并显示列车信息。 */
export function TrainList() {
  const trains = useStore((s) => s.trains);
  const online = useStore((s) => s.meta?.online ?? false);
  const currentWorld = useStore((s) => s.currentWorld);
  const focusTrain = useStore((s) => s.focusTrain);
  const close = useStore((s) => s.closeSidebar);

  // 所有世界的列车，按世界名、玩家数（多在前）排序，便于浏览。
  const list = [...trains.values()].sort(
    (a, b) => a.world.localeCompare(b.world) || b.passengers.length - a.passengers.length,
  );

  return (
    <div className="panel panel-fill">
      <div className="panel-header station-header">
        <div className="station-title-block">
          <div className="station-eyebrow">实时</div>
          <h2>实时列车</h2>
          <div className="station-sub">共 {list.length} 趟运行中</div>
        </div>
        <button className="icon-btn" onClick={close}>
          ×
        </button>
      </div>
      <div className="panel-body train-list">
        {!online && <div className="muted">实时列车数据异常，暂无法获取</div>}
        {online && list.length === 0 && <div className="muted">当前没有运行中的列车</div>}
        {list.map((t) => (
          <TrainCard
            key={t.trainId}
            train={t}
            offWorld={t.world !== currentWorld}
            onClick={() => focusTrain(t.trainId)}
          />
        ))}
      </div>
    </div>
  );
}

function TrainCard({
  train,
  offWorld,
  onClick,
}: {
  train: Train;
  offWorld: boolean;
  onClick: () => void;
}) {
  const lines = useStore((s) => s.lines);
  const graph = useStore((s) => s.graph);

  // 普通车：当前所在线路（颜色竖条 + 名称）。
  const line = !train.express && train.lineId ? lines.find((l) => l.id === train.lineId) ?? null : null;
  // 快速车：始发 / 终到车站。routeNodeIds 首尾可能是道岔，故从两端向内找第一个车站节点。
  const route = train.express ? train.routeNodeIds ?? [] : [];
  const startStation = route.length > 0 ? graph?.firstStationName(route) ?? null : null;
  const endStation =
    route.length > 0 ? graph?.firstStationName(route, true) ?? null : train.destination ?? null;
  const runtime =
    typeof train.secondsLived === 'number' && train.secondsLived >= 0
      ? formatDuration(train.secondsLived)
      : null;

  return (
    <button className="train-card" onClick={onClick}>
      <div className="train-card-head">
        <span className={`train-badge ${train.express ? 'express' : ''}`}>
          {train.express ? '快速车' : '普通车'}
        </span>
        <span className="train-world">
          {train.world}
          {offWorld && <span className="train-offworld">其它世界</span>}
        </span>
      </div>

      {/* 普通车：所在线路（颜色竖条 + 名称）；快速车：始发站 → 终到站 */}
      {!train.express && line && (
        <div className="train-body-row">
          <span className="train-line">
            <span className="train-line-bar" style={{ background: line.color }} />
            <span className="train-line-name">{line.name}</span>
          </span>
        </div>
      )}
      {train.express && (startStation || endStation) && (
        <div className="train-route">
          <span className="train-endpoint">
            <span className="train-dot start" />
            {startStation ?? '—'}
          </span>
          <span className="train-endpoint">
            <span className="train-dot end" />
            {endStation ?? '—'}
          </span>
        </div>
      )}

      {runtime && (
        <div className="train-info-line">
          <span className="train-info-label">运行时间</span>
          <span className="train-info-value">{runtime}</span>
        </div>
      )}

      <div className="train-card-meta">
        <span className="train-pill train-passengers">
          乘客:
          {train.passengers.length === 0 ? (
            <span className="train-passengers-empty">无</span>
          ) : (
            <span className="train-avatars">
              {train.passengers.map((name) => (
                <img key={name} className="train-avatar" src={avatarUrl(name)} alt={name} title={name} />
              ))}
            </span>
          )}
        </span>
        {!train.express && train.destination && (
          <span className="train-pill">终到 {train.destination}</span>
        )}
      </div>
    </button>
  );
}
