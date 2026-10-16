/** 概览：玩家基本信息与乘车统计；所在铁路系统的营收、规模与客流。 */
import { avatarUrl, getConfig } from '../../config';
import { centerApi } from '../api';
import type { CenterContext } from '../CenterApp';
import { DailyBars } from '../Chart';
import { onLinkClick } from '../router';
import { Card, Empty, ErrorState, formatNumber, formatTime, Loading, PageHeader, useAsync } from '../ui';

function Stat({ label, value, unit, hint }: { label: string; value: string | number; unit?: string; hint?: string }) {
  return (
    <div className="c-stat">
      <div className="c-stat-label">{label}</div>
      <div className="c-stat-value">
        {value}
        {unit && <span className="c-stat-unit">{unit}</span>}
      </div>
      {hint && <div className="c-stat-hint">{hint}</div>}
    </div>
  );
}

export function OverviewPage({ ctx }: { ctx: CenterContext }) {
  const { data, error, loading, reload } = useAsync(() => centerApi.overview(), []);
  const currency = getConfig().currencyName;
  const player = ctx.profile.player;

  if (loading && !data) return <Loading rows={6} />;
  if (error || !data) return error ? <ErrorState error={error} onRetry={reload} /> : null;

  const s = data.stats;
  const lineName = (id: string) => data.lineNames[id] ?? id;
  const noRides = s.rideCount === 0 && s.visitedStations === 0;

  return (
    <>
      <PageHeader title="概览" sub="你的乘车与购票统计" />
      <Card className="c-profile-card">
        <img className="c-profile-avatar" src={avatarUrl(player.uuid || player.name)} alt="" />
        <div className="c-profile-info">
          <div className="c-profile-name">{player.name}</div>
          <div className="c-muted c-mono">{player.uuid}</div>
          <div className="c-muted">
            {s.firstRideAt ? `首次乘车：${formatTime(s.firstRideAt)}` : '还没有乘车记录'}
            {ctx.profile.systems.some((x) => x.isMember) && ' · 铁路系统成员'}
          </div>
        </div>
      </Card>

      <div className="c-stat-grid">
        <Stat label="乘车次数" value={s.rideCount} unit="次" />
        <Stat label="累计里程" value={formatNumber(s.totalKm)} unit="km" />
        <Stat label="购票次数" value={s.ticketCount} unit="次" hint={s.webTicketCount ? `其中网页购票 ${s.webTicketCount} 次` : undefined} />
        <Stat label="购票花费" value={formatNumber(s.ticketSpent)} unit={currency} />
        <Stat label="到访车站" value={s.visitedStations} unit="站" />
        <Stat label="交通卡充值" value={formatNumber(s.cardChargeAmount)} unit={currency} hint={`${s.cardChargeCount} 次`} />
        <Stat label="刷卡乘车" value={s.cardRideCount} unit="次" hint={`花费 ${formatNumber(s.cardRideSpent)} ${currency}`} />
      </div>

      {noRides ? (
        <Card>
          <Empty title="还没有乘车数据" hint="在游戏内乘车后，这里会显示你的统计。" />
        </Card>
      ) : (
        <div className="c-grid-2">
          <Card title="近 30 天乘车">
            <DailyBars data={s.dailyRides} unit="次" label="近 30 天每日乘车次数" />
          </Card>
          <Card title="出行偏好">
            <div className="c-kv-list">
              <div className="c-kv-title">最喜欢的线路</div>
              {s.favoriteLines.length === 0 && <div className="c-muted">暂无</div>}
              {s.favoriteLines.map((l, i) => (
                <div key={l.key} className="c-rank-row">
                  <span className="c-rank-no">{i + 1}</span>
                  <span className="c-rank-name">{lineName(l.key)}</span>
                  <span className="c-rank-val">{l.count} 次</span>
                </div>
              ))}
              <div className="c-kv-title">最常经过的车站</div>
              {s.topStations.length === 0 && <div className="c-muted">暂无</div>}
              {s.topStations.map((st, i) => (
                <div key={st.key} className="c-rank-row">
                  <span className="c-rank-no">{i + 1}</span>
                  <span className="c-rank-name">{st.key}</span>
                  <span className="c-rank-val">{st.count} 次</span>
                </div>
              ))}
              {s.longestRide && (
                <>
                  <div className="c-kv-title">最长一次行程</div>
                  <div className="c-rank-row">
                    <span className="c-rank-name">
                      {s.longestRide.startStation} → {s.longestRide.endStation}
                    </span>
                    <span className="c-rank-val">{formatNumber(s.longestRide.distance)} km</span>
                  </div>
                </>
              )}
            </div>
          </Card>
        </div>
      )}

      {data.systems.map((sys) => (
        <Card
          key={sys.id}
          title={
            <>
              {sys.name}
              <span className="c-tag">{sys.isCreator ? '创建者' : '成员'}</span>
            </>
          }
          extra={
            <a
              className="c-link"
              href={`/me/systems/${encodeURIComponent(sys.id)}`}
              onClick={(e) => onLinkClick(e, `/me/systems/${encodeURIComponent(sys.id)}`)}
            >
              管理 →
            </a>
          }
        >
          <div className="c-stat-grid compact">
            <Stat label="累计营收" value={formatNumber(sys.income)} unit={currency} />
            <Stat label="待提取" value={formatNumber(Math.max(0, sys.income - sys.withdrawn))} unit={currency} hint="创建者上线时自动发放" />
            <Stat label="线路" value={sys.lineCount} unit="条" />
            <Stat label="车站" value={sys.stationCount} unit="站" />
            <Stat label="轨道里程" value={formatNumber(sys.trackKm)} unit="km" />
          </div>
          <div className="c-grid-2">
            <div>
              <div className="c-chart-title">近 30 天营收</div>
              <DailyBars data={sys.dailyRevenue} unit={currency} label="近 30 天每日营收" />
            </div>
            <div>
              <div className="c-chart-title">近 14 天客流（人次）</div>
              <DailyBars data={sys.dailyRiders} unit="人次" label="近 14 天每日客流" />
            </div>
          </div>
        </Card>
      ))}
    </>
  );
}
