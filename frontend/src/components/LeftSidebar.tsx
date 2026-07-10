import { useStore } from '../store/useStore';
import { StationSearch } from './StationSearch';
import { RouteCard } from './RouteCard';
import { TrainInfo } from './TrainInfo';
import { getConfig } from '../config';
import type { RideHistoryItem } from '../types';

/** 左侧边栏：车站信息 / 路线查询 / 列车信息（仿 Google 地图）。 */
export function LeftSidebar() {
  const sidebar = useStore((s) => s.sidebar);
  if (sidebar === 'idle') return null;

  return (
    <div className="sidebar sidebar-left">
      {sidebar === 'station' && <StationPanel />}
      {sidebar === 'route' && <RoutePanel />}
      {sidebar === 'train' && <TrainInfo />}
      {sidebar === 'history' && <HistoryPanel />}
    </div>
  );
}

function StationPanel() {
  const name = useStore((s) => s.selectedStation);
  const graph = useStore((s) => s.graph);
  const lines = useStore((s) => s.lines);
  const systems = useStore((s) => s.systemMap);
  const openRoutePanel = useStore((s) => s.openRoutePanel);
  const close = useStore((s) => s.closeSidebar);
  if (!name) return null;

  // 收集该站所属线路 / 系统
  const lineIds = new Set<string>();
  const systemIds = new Set<string>();
  for (const id of graph?.stationNodes(name) ?? []) {
    const n = graph?.nodes.get(id);
    n?.lineIds.forEach((l) => lineIds.add(l));
    n?.systemIds.forEach((s) => systemIds.add(s));
  }
  const lineList = lines.filter((l) => lineIds.has(l.id));

  return (
    <div className="panel">
      <div className="panel-header station-header">
        <div className="station-title-block">
          <div className="station-eyebrow">车站</div>
          <h2>{name}</h2>
          <div className="station-sub">
            {lineList.length} 条线路 · {systemIds.size} 个铁路系统
          </div>
        </div>
        <button className="icon-btn" onClick={close}>
          ×
        </button>
      </div>
      <div className="panel-body">
        <div className="panel-section">
          <div className="label">所属线路</div>
          <div className="chips">
            {lineList.length === 0 && <span className="muted">—</span>}
            {lineList.map((l) => (
              <span key={l.id} className="chip line-chip">
                <span className="chip-dot" style={{ background: l.color }} />
                {l.name}
              </span>
            ))}
          </div>
        </div>
        <div className="panel-section">
          <div className="label">所属铁路系统</div>
          <div className="chips">
            {[...systemIds].map((sid) => (
              <span key={sid} className="chip chip-outline">
                {systems.get(sid)?.name ?? sid}
              </span>
            ))}
          </div>
        </div>
      </div>
      <button className="btn primary" onClick={() => openRoutePanel(name)}>
        路线
      </button>
    </div>
  );
}

function HistoryPanel() {
  const history = useStore((s) => s.rideHistory);
  const loading = useStore((s) => s.historyLoading);
  const selectedId = useStore((s) => s.selectedHistoryId);
  const loadRideHistory = useStore((s) => s.loadRideHistory);
  const selectHistory = useStore((s) => s.selectHistory);
  const close = useStore((s) => s.closeSidebar);

  return (
    <div className="panel panel-fill">
      <div className="panel-header station-header">
        <div className="station-title-block">
          <div className="station-eyebrow">我的行程</div>
          <h2>乘车历史</h2>
          {history && <div className="station-sub">共 {history.total} 条记录</div>}
        </div>
        <button className="icon-btn" onClick={close}>
          ×
        </button>
      </div>
      <div className="panel-body history-list">
        {loading && <div className="muted">加载中…</div>}
        {!loading && history?.items.length === 0 && <div className="muted">暂无乘车历史</div>}
        {history?.items.map((item) => (
          <HistoryCard
            key={item.id}
            item={item}
            selected={selectedId === item.id}
            onClick={() => selectHistory(item)}
          />
        ))}
      </div>
      {history && history.totalPages > 1 && (
        <div className="pager">
          <button className="btn ghost" disabled={history.page <= 1 || loading} onClick={() => loadRideHistory(history.page - 1)}>
            上一页
          </button>
          <span className="muted">{history.page} / {history.totalPages}</span>
          <button
            className="btn ghost"
            disabled={history.page >= history.totalPages || loading}
            onClick={() => loadRideHistory(history.page + 1)}
          >
            下一页
          </button>
        </div>
      )}
    </div>
  );
}

function HistoryCard({
  item,
  selected,
  onClick,
}: {
  item: RideHistoryItem;
  selected: boolean;
  onClick: () => void;
}) {
  const distance = item.distance > 0 ? item.distance : estimateNodeDistance(item);
  return (
    <button className={`history-card ${selected ? 'selected' : ''}`} onClick={onClick}>
      <div className="history-card-head">
        <span className={`history-badge ${item.express ? 'express' : ''}`}>
          {item.express ? '直达车' : '普通车'}
        </span>
        <span className="history-time">{formatTime(item.startedAt)}</span>
      </div>
      <div className="history-route">
        <span className="history-endpoint">
          <span className="history-dot start" />
          {item.startStation}
        </span>
        <span className="history-endpoint">
          <span className="history-dot end" />
          {item.endStation}
        </span>
      </div>
      <div className="history-meta">
        <span className="history-pill">{distance.toFixed(2)} km</span>
        {item.express && (
          <span className="history-pill">实付 {Number(item.paidFare ?? 0).toFixed(2)} {getConfig().currencyName}</span>
        )}
      </div>
    </button>
  );
}

function formatTime(ms: number): string {
  return new Date(ms).toLocaleString();
}

function estimateNodeDistance(item: RideHistoryItem): number {
  const graph = useStore.getState().graph;
  if (!graph) return 0;
  let meters = 0;
  for (let i = 0; i < item.nodeIds.length - 1; i++) {
    const link = graph.links(item.nodeIds[i]).find((l) => l.to === item.nodeIds[i + 1]);
    if (link) meters += link.distance;
  }
  return meters / 1000;
}

function RoutePanel() {
  const graph = useStore((s) => s.graph);
  const startStation = useStore((s) => s.startStation);
  const endStation = useStore((s) => s.endStation);
  const nextPick = useStore((s) => s.nextPick);
  const candidates = useStore((s) => s.candidates);
  const setEndpoint = useStore((s) => s.setEndpoint);
  const close = useStore((s) => s.closeSidebar);

  const stationNames = graph?.allStationNames() ?? [];

  return (
    <div className="panel">
      <div className="panel-header">
        <h2>路线查询</h2>
        <button className="icon-btn" onClick={close}>
          ×
        </button>
      </div>
      <div className="panel-section route-search-fixed">
        <StationSearch
          value={startStation}
          placeholder={nextPick === 'start' ? '选择起点（或点击地图车站）' : '起点'}
          stations={stationNames}
          onSelect={(n) => setEndpoint('start', n)}
          onClear={() => setEndpoint('start', null)}
        />
        <StationSearch
          value={endStation}
          placeholder={nextPick === 'end' ? '选择终点（或点击地图车站）' : '终点'}
          stations={stationNames}
          onSelect={(n) => setEndpoint('end', n)}
          onClear={() => setEndpoint('end', null)}
        />
        <div className="hint muted">下一次点击地图车站将设为：{nextPick === 'start' ? '起点' : '终点'}</div>
      </div>
      <div className="panel-body route-list">
        {startStation && endStation && candidates.length === 0 && (
          <div className="muted">所选两站间暂无可用路线（直达 / 联程票）</div>
        )}
        {candidates.map((p, i) => (
          <RouteCard key={i} path={p} index={i} />
        ))}
      </div>
    </div>
  );
}
