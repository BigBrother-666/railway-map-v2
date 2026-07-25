import { useEffect } from 'react';
import { useStore } from '../store/useStore';
import { avatarUrl } from '../config';
import { formatDuration } from '../format';
import { LineRouteDiagram } from './LineRouteDiagram';

/** 列车信息面板：基本信息 + 车上玩家；快速车则高亮其路线。 */
export function TrainInfo() {
  const trainId = useStore((s) => s.selectedTrainId);
  const train = useStore((s) => (trainId ? s.trains.get(trainId) : null));
  const graph = useStore((s) => s.graph);
  const lines = useStore((s) => s.lines);
  const clickLine = useStore((s) => s.clickLine);
  const trackingTrainId = useStore((s) => s.trackingTrainId);
  const toggleTrainTracking = useStore((s) => s.toggleTrainTracking);
  const close = useStore((s) => s.closeSidebar);

  // 快速车：把其 routeNodeIds 作为临时高亮（复用候选高亮通道）。
  // 依赖 trainId + 路线内容而非整个 train 对象，避免位置刷新反复触发路线框选。
  const routeKey =
    train?.express && train.routeNodeIds && train.routeNodeIds.length > 1
      ? train.routeNodeIds.join(',')
      : '';
  useEffect(() => {
    if (routeKey) {
      useStore.setState({
        candidates: [
          {
            stations: [],
            nodeIds: routeKey.split(','),
            lineIdSequence: [],
            distance: 0,
            segments: [],
            estimatedFare: 0,
            expressRoute: true,
          },
        ],
        selectedRouteIndex: 0,
      });
    }
    return () => {
      if (useStore.getState().sidebar !== 'route') {
        useStore.setState({ candidates: [], selectedRouteIndex: null });
      }
    };
  }, [trainId, routeKey]);

  if (!train) return null;

  const route = train.express ? train.routeNodeIds ?? [] : [];
  const startStation = route.length > 0 ? graph?.firstStationName(route) ?? null : null;
  const endStation =
    route.length > 0 ? graph?.firstStationName(route, true) ?? null : train.destination ?? null;
  const currentLine = !train.express && train.lineId ? lines.find((line) => line.id === train.lineId) ?? null : null;
  const tracking = trackingTrainId === train.trainId;

  return (
    <div className="panel">
      <div className="panel-header">
        <h2>列车 {train.express ? '（快速车）' : '（普通车）'}</h2>
        <div className="panel-header-actions">
          <button
            type="button"
            className={`track-btn ${tracking ? 'active' : ''}`}
            title={tracking ? '停止跟踪列车' : '跟踪列车'}
            onClick={() => toggleTrainTracking(train.trainId)}
          >
            {tracking ? '停止跟踪' : '跟踪'}
          </button>
          <button className="icon-btn" onClick={close}>
            ×
          </button>
        </div>
      </div>
      <div className="panel-body">
        <div className="panel-section info-card">
          {currentLine && <RowButton label="所属线路" value={currentLine.name} onClick={() => clickLine(currentLine.id)} />}
          <Row label="所在世界" value={train.world} />
          {train.trainName && train.trainName !== 'N/A' && <Row label="列车名称" value={train.trainName} />}
          {train.express ? (
            <>
              {startStation && <Row label="始发车站" value={startStation} />}
              {endStation && <Row label="终到车站" value={endStation} />}
            </>
          ) : (
            <>
              {train.destination && <Row label="终到站" value={train.destination} />}
            </>
          )}
          <Row label="速度" value={`${train.speedKph.toFixed(1)} km/h`} />
          <Row label="车厢数" value={String(train.cartCount)} />
          {typeof train.secondsLived === 'number' && train.secondsLived >= 0 && (
            <Row label="运行时间" value={formatDuration(train.secondsLived)} />
          )}
        </div>
        <div className="panel-section">
          <div className="label">车上玩家（{train.passengers.length}）</div>
          <div className="passenger-list">
            {train.passengers.length === 0 && <span className="muted">无</span>}
            {train.passengers.map((p) => (
              <span key={p} className="passenger-pill">
                <img src={avatarUrl(p)} alt="" />
                <span>{p}</span>
              </span>
            ))}
          </div>
        </div>
        {currentLine && <LineRouteDiagram line={currentLine} />}
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="info-row">
      <span className="label">{label}</span>
      <span className="value">{value}</span>
    </div>
  );
}

function RowButton({ label, value, onClick }: { label: string; value: string; onClick: () => void }) {
  return (
    <button type="button" className="info-row info-row-button" onClick={onClick}>
      <span className="label">{label}</span>
      <span className="value">{value}</span>
    </button>
  );
}
