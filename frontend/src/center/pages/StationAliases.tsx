/** 车站别名（英文站名）管理：与游戏内 /ticketconfig editStationAlias / delStationAlias 相同。 */
import { useMemo, useState } from 'react';
import { centerApi, CenterError } from '../api';
import type { CenterContext } from '../CenterApp';
import {
  Card,
  ConfirmModal,
  Empty,
  ErrorState,
  Loading,
  PageHeader,
  PluginOfflineBanner,
  successText,
  toCenterError,
  useAsync,
  useNotice,
} from '../ui';
import { EditModalFrame } from './System';

export function StationAliasPage({ ctx }: { ctx: CenterContext }) {
  const { data, error, loading, reload } = useAsync(() => centerApi.stationAliases(), []);
  const { notify, node } = useNotice();
  const [q, setQ] = useState('');
  const [edit, setEdit] = useState<{ station: string; english: string; isNew: boolean } | null>(null);
  const [del, setDel] = useState<string | null>(null);
  const online = ctx.profile.pluginOnline;

  const rows = useMemo(() => {
    if (!data) return [];
    const known = new Set(data.stations);
    return Object.entries(data.aliases)
      .map(([station, english]) => ({ station, english, exists: known.has(station) }))
      .filter((r) => !q || r.station.includes(q) || r.english.toLowerCase().includes(q.toLowerCase()))
      .sort((a, b) => a.station.localeCompare(b.station, 'zh-CN'));
  }, [data, q]);

  const refreshSoon = () => setTimeout(reload, 600);

  return (
    <>
      {node}
      <PageHeader
        title="车站别名"
        sub="为车站设置英文名，显示在地图与游戏内。全服共享。"
        actions={
          <button className="c-btn primary" disabled={!online} onClick={() => setEdit({ station: '', english: '', isNew: true })}>
            新增别名
          </button>
        }
      />
      <PluginOfflineBanner online={online} />
      <Card
        extra={
          <input className="c-input c-search" placeholder="搜索中文或英文站名" value={q} onChange={(e) => setQ(e.target.value)} aria-label="搜索" />
        }
      >
        {loading && !data ? (
          <Loading />
        ) : error ? (
          <ErrorState error={error} onRetry={reload} />
        ) : rows.length === 0 ? (
          <Empty title={q ? '没有匹配的别名' : '还没有车站别名'} />
        ) : (
          <div className="c-table-wrap">
            <table className="c-table">
              <thead>
                <tr>
                  <th>中文站名</th>
                  <th>英文名</th>
                  <th className="c-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.station}>
                    <td data-label="中文站名">
                      {r.station}
                      {!r.exists && <span className="c-tag warn" title="当前线路图中找不到该车站">车站不存在</span>}
                    </td>
                    <td data-label="英文名">{r.english}</td>
                    <td className="c-right c-actions">
                      <button className="c-btn small" disabled={!online} onClick={() => setEdit({ station: r.station, english: r.english, isNew: false })}>
                        编辑
                      </button>
                      <button className="c-btn small danger-ghost" disabled={!online} onClick={() => setDel(r.station)}>
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {edit && data && (
        <AliasModal
          init={edit}
          stations={data.stations}
          existing={data.aliases}
          onClose={() => setEdit(null)}
          onSaved={(w) => {
            notify('success', successText('已保存', w));
            refreshSoon();
          }}
        />
      )}
      {del && (
        <ConfirmModal
          title="删除车站别名"
          confirmText="删除"
          message={<p>删除车站「{del}」的英文名？</p>}
          onConfirm={async () => {
            await centerApi.deleteStationAlias(del);
            notify('success', '已删除');
            refreshSoon();
          }}
          onClose={() => setDel(null)}
        />
      )}
    </>
  );
}

function AliasModal({
  init,
  stations,
  existing,
  onClose,
  onSaved,
}: {
  init: { station: string; english: string; isNew: boolean };
  stations: string[];
  existing: Record<string, string>;
  onClose: () => void;
  onSaved: (w?: string[] | null) => void;
}) {
  const [station, setStation] = useState(init.station);
  const [english, setEnglish] = useState(init.english);
  const [err, setErr] = useState<CenterError | null>(null);
  const [busy, setBusy] = useState(false);
  const unknown = station.trim() !== '' && !stations.includes(station.trim());
  const overwrite = init.isNew && existing[station.trim()] !== undefined;

  const submit = async () => {
    setErr(null);
    if (!station.trim()) return setErr(new CenterError(400, 'invalid', '请选择车站', 'station'));
    if (!english.trim()) return setErr(new CenterError(400, 'invalid', '英文名不能为空', 'englishName'));
    setBusy(true);
    try {
      const res = await centerApi.upsertStationAlias(station.trim(), english);
      onSaved(res.warnings);
      onClose();
    } catch (e) {
      setErr(toCenterError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <EditModalFrame title={init.isNew ? '新增车站别名' : '编辑车站别名'} onClose={onClose} onSubmit={submit} busy={busy} error={err}>
      <label className="c-field">
        <span className="c-label">中文站名</span>
        <input
          className={`c-input ${err?.field === 'station' ? 'invalid' : ''}`}
          list="c-station-options"
          value={station}
          disabled={!init.isNew}
          onChange={(e) => setStation(e.target.value)}
          autoFocus={init.isNew}
        />
        <datalist id="c-station-options">
          {stations.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
        {unknown && <span className="c-help warn">当前线路图中找不到该车站，仍可保存。</span>}
        {overwrite && <span className="c-help warn">该车站已有英文名「{existing[station.trim()]}」，保存会覆盖。</span>}
      </label>
      <label className="c-field">
        <span className="c-label">英文名</span>
        <input
          className={`c-input ${err?.field === 'englishName' ? 'invalid' : ''}`}
          value={english}
          onChange={(e) => setEnglish(e.target.value)}
          autoFocus={!init.isNew}
        />
        <span className="c-help">连续空格会合并为一个。</span>
      </label>
    </EditModalFrame>
  );
}
