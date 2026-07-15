import { useEffect } from 'react';
import { useStore } from '../store/useStore';
import { avatarUrl } from '../config';

/** 列车信息面板：基本信息 + 车上玩家；直达车则高亮其路线。 */
export function TrainInfo() {
  const trainId = useStore((s) => s.selectedTrainId);
  const train = useStore((s) => (trainId ? s.trains.get(trainId) : null));
  const close = useStore((s) => s.closeSidebar);

  // 直达车：把其 routeNodeIds 作为临时高亮（复用候选高亮通道）。
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

  return (
    <div className="panel">
      <div className="panel-header">
        <h2>列车 {train.express ? '（直达车）' : '（普通车）'}</h2>
        <button className="icon-btn" onClick={close}>
          ×
        </button>
      </div>
      <div className="panel-body">
        <div className="panel-section info-card">
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
