/** 车站组管理：组内不同名车站之间可换乘（联程票）。与游戏内 /ticketconfig editStationGroup / delStationGroup 相同。 */
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

export function StationGroupPage({ ctx }: { ctx: CenterContext }) {
  const { data, error, loading, reload } = useAsync(() => centerApi.stationGroups(), []);
  const { notify, node } = useNotice();
  const [edit, setEdit] = useState<{ group: string; stations: string[]; isNew: boolean } | null>(null);
  const [del, setDel] = useState<string | null>(null);
  const online = ctx.profile.pluginOnline;
  const groups = useMemo(
    () => Object.entries(data?.groups ?? {}).sort(([a], [b]) => a.localeCompare(b, 'zh-CN')),
    [data],
  );
  const refreshSoon = () => setTimeout(reload, 600);

  return (
    <>
      {node}
      <PageHeader
        title="车站组"
        sub="同一组内的不同名车站可以互相换乘（用于联程票）。一个车站只能属于一个组。"
        actions={
          <button className="c-btn primary" disabled={!online} onClick={() => setEdit({ group: '', stations: [], isNew: true })}>
            新建车站组
          </button>
        }
      />
      <PluginOfflineBanner online={online} />
      {loading && !data ? (
        <Loading />
      ) : error ? (
        <ErrorState error={error} onRetry={reload} />
      ) : groups.length === 0 ? (
        <Card>
          <Empty title="还没有车站组" />
        </Card>
      ) : (
        <div className="c-group-grid">
          {groups.map(([name, stations]) => (
            <Card
              key={name}
              title={name}
              extra={
                <div className="c-actions">
                  <button className="c-btn small" disabled={!online} onClick={() => setEdit({ group: name, stations, isNew: false })}>
                    编辑
                  </button>
                  <button className="c-btn small danger-ghost" disabled={!online} onClick={() => setDel(name)}>
                    删除
                  </button>
                </div>
              }
            >
              <div className="c-chips">
                {stations.map((s) => (
                  <span key={s} className={`c-chip ${data?.stations.includes(s) ? '' : 'warn'}`} title={data?.stations.includes(s) ? undefined : '当前线路图中找不到该车站'}>
                    {s}
                  </span>
                ))}
              </div>
            </Card>
          ))}
        </div>
      )}

      {edit && data && (
        <GroupModal
          init={edit}
          allStations={data.stations}
          groups={data.groups}
          onClose={() => setEdit(null)}
          onSaved={(w) => {
            notify('success', successText('已保存', w));
            refreshSoon();
          }}
        />
      )}
      {del && (
        <ConfirmModal
          title="删除车站组"
          confirmText="删除"
          message={<p>删除车站组「{del}」？组内车站之间将不能再换乘。</p>}
          onConfirm={async () => {
            await centerApi.deleteStationGroup(del);
            notify('success', '已删除');
            refreshSoon();
          }}
          onClose={() => setDel(null)}
        />
      )}
    </>
  );
}

function GroupModal({
  init,
  allStations,
  groups,
  onClose,
  onSaved,
}: {
  init: { group: string; stations: string[]; isNew: boolean };
  allStations: string[];
  groups: Record<string, string[]>;
  onClose: () => void;
  onSaved: (w?: string[] | null) => void;
}) {
  const [group, setGroup] = useState(init.group);
  const [stations, setStations] = useState<string[]>(init.stations);
  const [input, setInput] = useState('');
  const [err, setErr] = useState<CenterError | null>(null);
  const [busy, setBusy] = useState(false);

  // 车站 → 所在的其它组（本组除外）
  const owner = useMemo(() => {
    const m: Record<string, string> = {};
    for (const [g, list] of Object.entries(groups)) {
      if (g === group.trim()) continue;
      for (const s of list) m[s] = g;
    }
    return m;
  }, [groups, group]);

  const add = (name: string) => {
    const s = name.trim();
    if (!s || stations.includes(s)) return;
    setStations([...stations, s]);
    setInput('');
  };

  const submit = async () => {
    setErr(null);
    const g = group.trim();
    if (!g || /\s/.test(g)) return setErr(new CenterError(400, 'invalid', '组名不能为空且不能包含空格', 'group'));
    if (stations.length < 2) return setErr(new CenterError(400, 'invalid', '车站组至少需要两个不同的车站', 'stations'));
    const conflicts = Object.fromEntries(stations.filter((s) => owner[s]).map((s) => [s, owner[s]]));
    if (Object.keys(conflicts).length > 0) {
      return setErr(new CenterError(422, 'conflict', '部分车站已属于其它车站组', 'stations', undefined, conflicts));
    }
    if (init.isNew && groups[g]) return setErr(new CenterError(400, 'invalid', '该组名已存在，请直接编辑', 'group'));
    setBusy(true);
    try {
      const res = await centerApi.upsertStationGroup(g, stations);
      onSaved(res.warnings);
      onClose();
    } catch (e) {
      setErr(toCenterError(e));
    } finally {
      setBusy(false);
    }
  };

  const options = allStations.filter((s) => !stations.includes(s));

  return (
    <EditModalFrame title={init.isNew ? '新建车站组' : `编辑车站组 · ${init.group}`} onClose={onClose} onSubmit={submit} busy={busy} error={err}>
      <label className="c-field">
        <span className="c-label">组名</span>
        <input
          className={`c-input ${err?.field === 'group' ? 'invalid' : ''}`}
          value={group}
          disabled={!init.isNew}
          onChange={(e) => setGroup(e.target.value)}
          autoFocus={init.isNew}
        />
      </label>
      <div className={`c-field ${err?.field === 'stations' ? 'invalid' : ''}`}>
        <span className="c-label">车站（{stations.length}）</span>
        <div className="c-chips">
          {stations.map((s) => (
            <span key={s} className={`c-chip ${owner[s] ? 'error' : allStations.includes(s) ? '' : 'warn'}`}>
              {s}
              {owner[s] && <span className="c-chip-note">已在组 {owner[s]}</span>}
              <button type="button" className="c-chip-x" aria-label={`移除 ${s}`} onClick={() => setStations(stations.filter((x) => x !== s))}>
                ✕
              </button>
            </span>
          ))}
        </div>
        <div className="c-inline">
          <input
            className="c-input"
            list="c-group-station-options"
            placeholder="输入或选择车站后回车"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                add(input);
              }
            }}
          />
          <button type="button" className="c-btn" onClick={() => add(input)}>
            添加
          </button>
        </div>
        <datalist id="c-group-station-options">
          {options.map((s) => (
            <option key={s} value={s} label={owner[s] ? `已在组 ${owner[s]}` : undefined} />
          ))}
        </datalist>
        <span className="c-help">红色表示该车站已属于其它组，需先从那个组移除；黄色表示线路图中找不到该车站（仍可保存）。</span>
      </div>
    </EditModalFrame>
  );
}
