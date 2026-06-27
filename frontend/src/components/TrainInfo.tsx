import { useEffect } from 'react';
import { useStore } from '../store/useStore';
import { avatarUrl } from '../config';

/** 列车信息面板：基本信息 + 车上玩家；直达车则高亮其路线。 */
export function TrainInfo() {
  const trainId = useStore((s) => s.selectedTrainId);
  const train = useStore((s) => (trainId ? s.trains.get(trainId) : null));
  const close = useStore((s) => s.closeSidebar);

  // 直达车：把其 routeNodeIds 作为临时高亮（复用候选高亮通道）
  useEffect(() => {
    const st = useStore.getState();
    if (train?.express && train.routeNodeIds && train.routeNodeIds.length > 1) {
      useStore.setState({
        candidates: [
          {
            stations: [],
            nodeIds: train.routeNodeIds,
            lineIdSequence: [],
            distance: 0,
            segments: [],
            estimatedFare: 0,
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
    void st;
  }, [train]);

  if (!train) return null;

  return (
    <div className="panel">
      <div className="panel-header">
        <h2>列车 {train.express ? '（直达车）' : '（普通车）'}</h2>
        <button className="icon-btn" onClick={close}>
          ×
        </button>
      </div>
      <div className="panel-section">
        <Row label="所在世界" value={train.world} />
        {train.lineId && <Row label="所属线路" value={train.lineId} />}
        {train.destination && <Row label="终到站" value={train.destination} />}
        <Row label="速度" value={`${train.speedKph.toFixed(1)} km/h`} />
        <Row label="车厢数" value={String(train.cartCount)} />
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
