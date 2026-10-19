/**
 * 线路配置字段与插件 railway_routes.yml / RouteWizard 输入格式互转。
 * - bossbar-stations：每项一个站名，"站名:RV" 表示折返站；末项若为 "<线路id>" 或 "<线路id>:<进入站名>" 表示到终点后转线。
 * - notice-arrival / notice-departure：游戏内向导用 "||" 分隔的多条，每条为 "sound:名称,音调,音量" 或 "announce:文字"。
 */

export interface StationRow {
  name: string;
  reverse: boolean;
}

export interface ParsedStations {
  stations: StationRow[];
  /** 转线目标线路 id（空表示不转线）。 */
  nextLineId: string;
  /** 转线后的进入站名（可空）。 */
  nextEntryStation: string;
}

/** 解析 bossbar-stations；knownLineIds 用于识别末项是否为转线目标（与插件 LineConfig 规则一致）。 */
export function parseStations(raw: string[], selfId: string, knownLineIds: string[]): ParsedStations {
  const items = [...raw];
  let nextLineId = '';
  let nextEntryStation = '';
  const last = items[items.length - 1]?.trim();
  if (last) {
    const sep = last.indexOf(':');
    const candidate = sep >= 0 ? last.slice(0, sep).trim() : last;
    if (candidate !== selfId && knownLineIds.includes(candidate)) {
      nextLineId = candidate;
      nextEntryStation = sep >= 0 ? last.slice(sep + 1).trim() : '';
      items.pop();
    }
  }
  const stations = items.map((item) => {
    const sep = item.lastIndexOf(':');
    if (sep >= 0 && item.slice(sep + 1).trim().toUpperCase() === 'RV') {
      return { name: item.slice(0, sep).trim(), reverse: true };
    }
    return { name: item, reverse: false };
  });
  return { stations, nextLineId, nextEntryStation };
}

export function formatStations(p: ParsedStations): string[] {
  const out = p.stations
    .filter((s) => s.name.trim() !== '')
    .map((s) => (s.reverse ? `${s.name.trim()}:RV` : s.name.trim()));
  if (p.nextLineId) {
    out.push(p.nextEntryStation.trim() ? `${p.nextLineId}:${p.nextEntryStation.trim()}` : p.nextLineId);
  }
  return out;
}

export type NoticeRow =
  | { kind: 'announce'; text: string }
  | { kind: 'sound'; name: string; pitch: string; volume: string };

export function parseNotices(raw: string | undefined): NoticeRow[] {
  if (!raw) return [];
  return raw
    .split('||')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((entry): NoticeRow => {
      if (entry.startsWith('sound:')) {
        const [name = '', pitch = '1', volume = '1'] = entry.slice(6).split(',');
        return { kind: 'sound', name: name.trim(), pitch: pitch.trim(), volume: volume.trim() };
      }
      return { kind: 'announce', text: entry.startsWith('announce:') ? entry.slice(9) : entry };
    });
}

export function formatNotices(rows: NoticeRow[]): string {
  return rows
    .map((r) => (r.kind === 'sound' ? `sound:${r.name.trim()},${r.pitch.trim()},${r.volume.trim()}` : `announce:${r.text}`))
    .filter((s) => s !== 'announce:')
    .join('||');
}

/** 前端预校验提示行（与插件规则一致：sound 需要名称与数字音调 / 音量，announce 文字非空）。 */
export function validateNotices(rows: NoticeRow[]): string | null {
  for (const r of rows) {
    if (r.kind === 'announce' && !r.text.trim()) return '文字不能为空';
    if (r.kind === 'sound') {
      if (!r.name.trim()) return '声音名称不能为空';
      if (!Number.isFinite(Number(r.pitch)) || !Number.isFinite(Number(r.volume)) || r.pitch === '' || r.volume === '') {
        return '声音的音调和音量必须是数字';
      }
    }
  }
  return null;
}
