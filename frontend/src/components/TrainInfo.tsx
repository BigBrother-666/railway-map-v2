import { useEffect } from 'react';
import { useStore } from '../store/useStore';
import { avatarUrl } from '../config';
import { formatDuration } from '../format';

/** 列车信息面板：基本信息 + 车上玩家；快速车则高亮其路线。 */
export function TrainInfo() {
  const trainId = useStore((s) => s.selectedTrainId);
  const train = useStore((s) => (trainId ? s.trains.get(trainId) : null));
  const graph = useStore((s) => s.graph);
  const close = useStore((s) => s.closeSidebar);

  // 快速车：把其 routeNodeIds 作为临时高亮（复用候选高亮通道）。
  // 依赖用 trainId + 路线内容而非整个 train 对象——列车每次位置刷新都会生成新对象，
  // 若依赖 train 会导致高亮被反复重设，进而反复触发镜头框选、把用户的缩放拉回。
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
            expressRoute: true, // 快速车路线：中途站淡化，仅起终点不透明
          },
        ],
        selectedRouteIndex: 0,
      });
    }
    return () => {
      // 离开列车面板时清掉临时高亮
      if (useStore.getState().sidebar !== 'route') {
        useStore.setState({ candidates: [], selectedRouteIndex: null });
      }
    };
  }, [trainId, routeKey]);

  if (!train) return null;

  // 快速车的始发 / 终到车站：routeNodeIds 首尾可能是道岔，故从两端向内找第一个车站节点。
  const route = train.express ? train.routeNodeIds ?? [] : [];
  const startStation = route.length > 0 ? graph?.firstStationName(route) ?? null : null;
  const endStation =
    route.length > 0 ? graph?.firstStationName(route, true) ?? null : train.destination ?? null;

  return (
    <div className="panel">
      <div className="panel-header">
        <h2>列车 {train.express ? '（快速车）' : '（普通车）'}</h2>
        <button className="icon-btn" onClick={close}>
          ×
        </button>
      </div>
      <div className="panel-body">
        <div className="panel-section info-card">
          <Row label="所在世界" value={train.world} />
          {train.trainName && train.trainName !== 'N/A' && <Row label="列车名称" value={train.trainName} />}
          {/* 快速车不显示所属线路，改为展示始发 / 终到车站（任务 4）；普通车仍显示所属线路。 */}
          {train.express ? (
            <>
              {startStation && <Row label="始发车站" value={startStation} />}
              {endStation && <Row label="终到车站" value={endStation} />}
            </>
          ) : (
            <>
              {train.lineName && <Row label="所属线路" value={train.lineName} />}
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
