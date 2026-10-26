/** 线路编辑弹窗：结构化编辑车站列表（折返 / 转线）与进出站提示，保存后由插件校验并重载线路配置。 */
import { useEffect, useMemo, useState } from 'react';
import { api } from '../../api/client';
import { CONTACT_SYSTEM_ID } from '../../config';
import type { Line } from '../../types';
import { centerApi, CenterError } from '../api';
import {
  formatNotices,
  formatStations,
  parseNotices,
  parseStations,
  validateNotices,
  type NoticeRow,
  type StationRow,
} from '../lineFormat';
import type { PrivateLine, ProfileSystem } from '../types';
import { toCenterError } from '../ui';
import { EditModalFrame } from './System';

const BOSSBAR_COLORS = ['', 'PINK', 'BLUE', 'RED', 'GREEN', 'YELLOW', 'PURPLE', 'WHITE'];

export function LineEditor({
  line,
  allLines,
  systems,
  onClose,
  onSaved,
}: {
  line: PrivateLine;
  allLines: PrivateLine[];
  systems: ProfileSystem[];
  onClose: () => void;
  onSaved: (warnings?: string[] | null) => void;
}) {
  // 全服线路（转线目标可以是其它铁路系统的线路）；加载完成前先用本系统线路兜底
  const [globalLines, setGlobalLines] = useState<Line[] | null>(null);
  useEffect(() => {
    api
      .lines()
      .then(setGlobalLines)
      .catch(() => setGlobalLines(null));
  }, []);
  // 全部线路 → 车站名（本系统线路以私有配置为准，去掉折返标记与末项转线目标）
  const lineIndex = useMemo(() => {
    const map = new Map<string, { id: string; name: string; systemId: string; stations: string[] }>();
    for (const l of globalLines ?? []) map.set(l.id, { id: l.id, name: l.name, systemId: l.systemId, stations: l.stations });
    const ids = [...new Set([...map.keys(), ...allLines.map((l) => l.id)])];
    for (const l of allLines) {
      const names = parseStations(l.bossbarStations, l.id, ids).stations.map((s) => s.name.trim()).filter(Boolean);
      map.set(l.id, { id: l.id, name: l.name, systemId: l.systemId, stations: names });
    }
    return map;
  }, [globalLines, allLines]);
  const targets = useMemo(() => {
    // 联络线不能作为转线目标
    const list = [...lineIndex.values()].filter(
      (l) => l.id !== line.id && l.id !== CONTACT_SYSTEM_ID && l.systemId !== CONTACT_SYSTEM_ID,
    );
    // 本系统在前，其余按系统、线路 id 排序
    return list.sort((a, b) =>
      a.systemId === line.systemId && b.systemId !== line.systemId
        ? -1
        : b.systemId === line.systemId && a.systemId !== line.systemId
          ? 1
          : a.systemId.localeCompare(b.systemId) || a.id.localeCompare(b.id),
    );
  }, [lineIndex, line]);
  // 识别末项仍用全部线路 id（含联络线），以便旧配置里转入联络线的末项不被误当成车站
  const knownIds = useMemo(() => [...lineIndex.keys()], [lineIndex]);
  const systemName = (id: string) => systems.find((s) => s.id === id)?.name ?? id;
  const [parsed, setParsed] = useState(() => parseStations(line.bossbarStations, line.id, knownIds));
  const [name, setName] = useState(line.name);
  const [color, setColor] = useState(line.color || '#a9a9a9');
  const [systemId, setSystemId] = useState(line.systemId);
  const [stations, setStations] = useState<StationRow[]>(parsed.stations);
  const [nextLineId, setNextLineId] = useState(parsed.nextLineId);
  const [nextEntry, setNextEntry] = useState(parsed.nextEntryStation);
  const [arrivalNotice, setArrivalNotice] = useState(line.bossbarArrivalNotice ?? '');
  const [bossbarColor, setBossbarColor] = useState(line.bossbarColor ?? '');
  const [noticeArrival, setNoticeArrival] = useState<NoticeRow[]>(parseNotices(line.noticeArrival));
  const [noticeDeparture, setNoticeDeparture] = useState<NoticeRow[]>(parseNotices(line.noticeDeparture));
  const [displayOnly, setDisplayOnly] = useState(line.displayOnly ?? false);

  // 全服线路加载完成后重新识别末项（可能是转入其它系统线路的转线目标）；仅在用户尚未改动车站列表时进行
  useEffect(() => {
    if (!globalLines) return;
    const reparsed = parseStations(line.bossbarStations, line.id, knownIds);
    if (reparsed.nextLineId === parsed.nextLineId) return;
    setStations((cur) => (JSON.stringify(cur) === JSON.stringify(parsed.stations) ? reparsed.stations : cur));
    setNextLineId((cur) => (cur === parsed.nextLineId ? reparsed.nextLineId : cur));
    setNextEntry((cur) => (cur === parsed.nextEntryStation ? reparsed.nextEntryStation : cur));
    setParsed(reparsed);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [globalLines]);
  const [err, setErr] = useState<CenterError | null>(null);
  const [busy, setBusy] = useState(false);

  const entryOptions = lineIndex.get(nextLineId)?.stations ?? [];
  const changeNextLine = (id: string) => {
    setNextLineId(id);
    // 进入站不属于新线路时清空，强制重新选择
    const opts = lineIndex.get(id)?.stations ?? [];
    setNextEntry((cur) => (opts.includes(cur.trim()) ? cur : ''));
  };

  const local = (field: string, msg: string) => setErr(new CenterError(400, 'invalid', msg, field));

  const submit = async () => {
    setErr(null);
    if (!name.trim()) return local('name', '线路名称不能为空');
    if (!/^#[0-9a-fA-F]{6}$/.test(color)) return local('color', '颜色必须是 #RRGGBB 格式');
    if (stations.filter((s) => s.name.trim()).length < 2) return local('bossbarStations', '至少需要两个车站');
    if (nextLineId) {
      if (!targets.some((t) => t.id === nextLineId)) return local('nextLineId', '不能转入该线路');
      if (!nextEntry.trim()) return local('nextEntryStation', '请选择转线后的进入站');
      if (!entryOptions.includes(nextEntry.trim())) return local('nextEntryStation', '进入站不是转入线路的车站');
    }
    const na = validateNotices(noticeArrival);
    if (na) return local('noticeArrival', `进站提示：${na}`);
    const nd = validateNotices(noticeDeparture);
    if (nd) return local('noticeDeparture', `出站提示：${nd}`);
    setBusy(true);
    try {
      const res = await centerApi.updateLine({
        id: line.id,
        systemId,
        name: name.trim(),
        color,
        bossbarStations: formatStations({ stations, nextLineId, nextEntryStation: nextEntry }),
        bossbarArrivalNotice: arrivalNotice,
        bossbarColor,
        noticeArrival: formatNotices(noticeArrival),
        noticeDeparture: formatNotices(noticeDeparture),
        displayOnly,
      });
      onSaved(res.warnings);
      onClose();
    } catch (e) {
      setErr(toCenterError(e));
    } finally {
      setBusy(false);
    }
  };

  const bad = (f: string) => (err?.field === f ? 'invalid' : '');
  const move = (i: number, d: number) => {
    const j = i + d;
    if (j < 0 || j >= stations.length) return;
    const next = [...stations];
    [next[i], next[j]] = [next[j], next[i]];
    setStations(next);
  };

  return (
    <EditModalFrame title={`编辑线路 · ${line.id}`} onClose={onClose} onSubmit={submit} busy={busy} error={err} wide>
      <fieldset className="c-fieldset">
        <legend>基础信息</legend>
        <div className="c-form-grid">
          <label className="c-field">
            <span className="c-label">线路名称</span>
            <input className={`c-input ${bad('name')}`} value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="c-field">
            <span className="c-label">线路颜色</span>
            <div className="c-inline">
              <input
                type="color"
                className="c-color"
                value={/^#[0-9a-fA-F]{6}$/.test(color) ? color : '#a9a9a9'}
                onChange={(e) => setColor(e.target.value)}
                aria-label="选择线路颜色"
              />
              <input className={`c-input c-mono ${bad('color')}`} value={color} onChange={(e) => setColor(e.target.value)} />
            </div>
          </label>
          <label className="c-field">
            <span className="c-label">所属铁路系统</span>
            <select className={`c-input ${bad('systemId')}`} value={systemId} onChange={(e) => setSystemId(e.target.value)}>
              {!systems.some((s) => s.id === systemId) && <option value={systemId}>{systemId}</option>}
              {systems.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
          <label className="c-check" title="仅在地图上展示，查询车票 / 寻路时不会经过该线路">
            <input type="checkbox" checked={displayOnly} onChange={(e) => setDisplayOnly(e.target.checked)} />
            仅展示线路（不参与购票）
          </label>
        </div>
      </fieldset>

      <fieldset className={`c-fieldset ${bad('bossbarStations')}`}>
        <legend>车站列表（按运行顺序）</legend>
        <ol className="c-station-list">
          {stations.map((st, i) => (
            <li key={i} className="c-station-row">
              <span className="c-station-no">{i + 1}</span>
              <input
                className="c-input"
                value={st.name}
                aria-label={`第 ${i + 1} 站站名`}
                onChange={(e) => setStations(stations.map((x, k) => (k === i ? { ...x, name: e.target.value } : x)))}
              />
              <label className="c-check" title="尽头式折返站：进站后反向驶出">
                <input
                  type="checkbox"
                  checked={st.reverse}
                  onChange={(e) => setStations(stations.map((x, k) => (k === i ? { ...x, reverse: e.target.checked } : x)))}
                />
                折返
              </label>
              <button type="button" className="c-icon-btn" onClick={() => move(i, -1)} aria-label="上移" disabled={i === 0}>
                ↑
              </button>
              <button
                type="button"
                className="c-icon-btn"
                onClick={() => move(i, 1)}
                aria-label="下移"
                disabled={i === stations.length - 1}
              >
                ↓
              </button>
              <button
                type="button"
                className="c-icon-btn"
                onClick={() => setStations(stations.filter((_, k) => k !== i))}
                aria-label="删除该站"
              >
                ✕
              </button>
            </li>
          ))}
        </ol>
        <button type="button" className="c-btn small" onClick={() => setStations([...stations, { name: '', reverse: false }])}>
          + 添加车站
        </button>
        <div className="c-form-grid">
          <label className="c-field">
            <span className="c-label">到达终点后转入线路（可选）</span>
            <select className={`c-input ${bad('nextLineId')}`} value={nextLineId} onChange={(e) => changeNextLine(e.target.value)}>
              <option value="">不转线</option>
              {groupBySystem(targets).map(([sys, list]) => (
                <optgroup key={sys} label={sys === line.systemId ? `本系统 · ${systemName(sys)}` : systemName(sys)}>
                  {list.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name}（{t.id}）
                    </option>
                  ))}
                </optgroup>
              ))}
              {nextLineId && !targets.some((t) => t.id === nextLineId) && (
                <option value={nextLineId} disabled>
                  {nextLineId}（不可转入）
                </option>
              )}
            </select>
            {/* <span className="c-help">可选择任意铁路系统的线路（例如经联络线转入其它系统），联络线本身不能作为转入线路。</span> */}
          </label>
          {nextLineId && (
            <label className="c-field">
              <span className="c-label">转线后的进入站</span>
              <select
                className={`c-input ${bad('nextEntryStation')}`}
                value={nextEntry}
                required
                onChange={(e) => setNextEntry(e.target.value)}
              >
                <option value="" disabled>
                  {entryOptions.length ? '请选择进入站' : '正在加载车站…'}
                </option>
                {entryOptions.map((st) => (
                  <option key={st} value={st}>
                    {st}
                  </option>
                ))}
                {nextEntry && !entryOptions.includes(nextEntry) && (
                  <option value={nextEntry} disabled>
                    {nextEntry}（不在该线路上）
                  </option>
                )}
              </select>
            </label>
          )}
        </div>
      </fieldset>

      <fieldset className="c-fieldset">
        <legend>BossBar</legend>
        <div className="c-form-grid">
          <label className="c-field">
            <span className="c-label">到站提示（可选）</span>
            <input
              className="c-input"
              value={arrivalNotice}
              placeholder="&6列车已到达 {line_color}{curr_station}站"
              onChange={(e) => setArrivalNotice(e.target.value)}
            />
            <span className="c-help">占位符：{'{curr_station} {line_name} {line_color}'}</span>
          </label>
          <label className="c-field">
            <span className="c-label">BossBar 颜色</span>
            <select className={`c-input ${bad('bossbarColor')}`} value={bossbarColor} onChange={(e) => setBossbarColor(e.target.value)}>
              {BOSSBAR_COLORS.map((c) => (
                <option key={c} value={c}>
                  {c || '默认'}
                </option>
              ))}
            </select>
          </label>
        </div>
      </fieldset>

      <NoticeEditor title="进站提示" rows={noticeArrival} onChange={setNoticeArrival} invalid={err?.field === 'noticeArrival'} />
      <NoticeEditor title="出站提示" rows={noticeDeparture} onChange={setNoticeDeparture} invalid={err?.field === 'noticeDeparture'} />
      <p className="c-help">
        保存后插件会重载线路配置。若修改了车站列表，可能需要在游戏内执行 <code>/railgeo walkAll</code> 重新遍历线路。
      </p>
    </EditModalFrame>
  );
}

function groupBySystem<T extends { systemId: string }>(items: T[]): [string, T[]][] {
  const groups = new Map<string, T[]>();
  for (const it of items) {
    const g = groups.get(it.systemId) ?? [];
    g.push(it);
    groups.set(it.systemId, g);
  }
  return [...groups.entries()];
}

function NoticeEditor({
  title,
  rows,
  onChange,
  invalid,
}: {
  title: string;
  rows: NoticeRow[];
  onChange: (r: NoticeRow[]) => void;
  invalid: boolean;
}) {
  const set = (i: number, r: NoticeRow) => onChange(rows.map((x, k) => (k === i ? r : x)));
  return (
    <fieldset className={`c-fieldset ${invalid ? 'invalid' : ''}`}>
      <legend>{title}（可选）</legend>
      {rows.length === 0 && <div className="c-muted">无</div>}
      {rows.map((r, i) => (
        <div key={i} className="c-notice-row">
          <select
            className="c-input c-notice-kind"
            value={r.kind}
            aria-label="提示类型"
            onChange={(e) =>
              set(i, e.target.value === 'sound' ? { kind: 'sound', name: '', pitch: '1', volume: '1' } : { kind: 'announce', text: '' })
            }
          >
            <option value="announce">文字</option>
            <option value="sound">声音</option>
          </select>
          {r.kind === 'announce' ? (
            <input
              className="c-input"
              value={r.text}
              placeholder="列车即将到达 {line_color}{curr_station}站"
              onChange={(e) => set(i, { ...r, text: e.target.value })}
              aria-label="文字"
            />
          ) : (
            <>
              <input className="c-input" value={r.name} placeholder="block.note_block.bell" onChange={(e) => set(i, { ...r, name: e.target.value })} aria-label="声音名称" />
              <input className="c-input c-num" value={r.pitch} inputMode="decimal" onChange={(e) => set(i, { ...r, pitch: e.target.value })} aria-label="音调" title="音调" />
              <input className="c-input c-num" value={r.volume} inputMode="decimal" onChange={(e) => set(i, { ...r, volume: e.target.value })} aria-label="音量" title="音量" />
            </>
          )}
          <button type="button" className="c-icon-btn" onClick={() => onChange(rows.filter((_, k) => k !== i))} aria-label="删除该条提示">
            ✕
          </button>
        </div>
      ))}
      <button type="button" className="c-btn small" onClick={() => onChange([...rows, { kind: 'announce', text: '' }])}>
        + 添加提示
      </button>
      <span className="c-help">文字可用占位符：{'{curr_station} {next_station} {line_name} {railway_system} {line_color}'}</span>
      <span className="c-help">声音后的两个数字分别是 音调(0.5-2) 和 音量(0-1)</span>
    </fieldset>
  );
}
