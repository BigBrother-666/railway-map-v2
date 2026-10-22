import { useId, useMemo, useState } from 'react';
import Fuse from 'fuse.js';
import { Search, X } from 'lucide-react';
import { useStore } from '../store/useStore';
import { CONTACT_SYSTEM_ID } from '../config';
import type { PointProps } from '../types';

/** 搜索候选项：线路或车站（车站按「站名 + 世界」聚合，坐标取该世界内各站台节点的中心）。 */
type SearchItem =
  | { kind: 'line'; key: string; name: string; world: string; lineId: string; color: string; systemName?: string }
  | { kind: 'station'; key: string; name: string; nameEn?: string; world: string; x: number; z: number };

const MAX_SUGGESTIONS = 8;

/** 地图左上角搜索框：按线路名 / 车站名 / 车站英文名模糊搜索，选中后定位到线路或车站。 */
export function MapSearch() {
  const geojson = useStore((s) => s.geojson);
  const lines = useStore((s) => s.lines);
  const lineWorlds = useStore((s) => s.lineWorlds);
  const systemMap = useStore((s) => s.systemMap);
  const worlds = useStore((s) => s.worlds);
  const selectSearchLine = useStore((s) => s.selectSearchLine);
  const focusStation = useStore((s) => s.focusStation);

  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const listId = useId();

  const items = useMemo<SearchItem[]>(() => {
    const out: SearchItem[] = [];
    // 联络线不在线路栏列出，搜索同样排除；无 geojson 段（lineWorlds 中没有）的线路无法定位，也排除。
    for (const l of lines) {
      const world = lineWorlds.get(l.id);
      if (!world || l.systemId === CONTACT_SYSTEM_ID) continue;
      out.push({
        kind: 'line',
        key: `line|${l.id}`,
        name: l.name || l.id,
        world,
        lineId: l.id,
        color: l.color,
        systemName: systemMap.get(l.systemId)?.name,
      });
    }
    const stations = new Map<string, { name: string; nameEn?: string; world: string; sx: number; sz: number; n: number }>();
    for (const f of geojson?.features ?? []) {
      if (f.geometry?.type !== 'Point') continue;
      const p = f.properties as PointProps | null;
      if (!p || p.type !== 'station' || !p.name) continue;
      const c = (f.geometry as GeoJSON.Point).coordinates;
      const key = `station|${p.world}|${p.name}`;
      const acc = stations.get(key) ?? { name: p.name, nameEn: p.nameEn, world: p.world, sx: 0, sz: 0, n: 0 };
      acc.sx += c[0];
      acc.sz += c[1];
      acc.n++;
      stations.set(key, acc);
    }
    for (const [key, s] of stations) {
      out.push({ kind: 'station', key, name: s.name, nameEn: s.nameEn, world: s.world, x: s.sx / s.n, z: s.sz / s.n });
    }
    return out;
  }, [geojson, lines, lineWorlds, systemMap]);

  const fuse = useMemo(
    () => new Fuse(items, { keys: ['name', 'nameEn'], threshold: 0.4, ignoreLocation: true }),
    [items],
  );

  const results = useMemo(() => {
    const q = query.trim();
    if (!q) return [];
    return fuse.search(q, { limit: MAX_SUGGESTIONS }).map((r) => r.item);
  }, [query, fuse]);

  const showList = open && results.length > 0;
  const multiWorld = worlds.length > 1;

  function choose(item: SearchItem) {
    setQuery(item.name);
    setOpen(false);
    if (item.kind === 'line') selectSearchLine(item.lineId);
    else focusStation(item.world, item.x, item.z);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      if (!results.length) return;
      e.preventDefault();
      setOpen(true);
      const step = e.key === 'ArrowDown' ? 1 : -1;
      setActive((i) => (i + step + results.length) % results.length);
    } else if (e.key === 'Enter') {
      // 回车：选中当前高亮的提示（默认第一条）
      const item = results[Math.min(active, results.length - 1)];
      if (item) {
        e.preventDefault();
        choose(item);
      }
    } else if (e.key === 'Escape') {
      setOpen(false);
    }
  }

  return (
    <div className="map-search">
      <div className="map-search-row">
        <Search className="map-search-icon" size={16} strokeWidth={2.25} aria-hidden="true" />
        <input
          type="text"
          role="combobox"
          aria-label="搜索线路或车站"
          aria-autocomplete="list"
          aria-expanded={showList}
          aria-controls={listId}
          aria-activedescendant={showList ? `${listId}-${active}` : undefined}
          value={query}
          placeholder="搜索线路 / 车站"
          onFocus={() => setOpen(true)}
          onBlur={() => setTimeout(() => setOpen(false), 150)}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
            setOpen(true);
          }}
          onKeyDown={onKeyDown}
        />
        {query && (
          <button
            className="icon-btn"
            title="清除"
            aria-label="清除搜索"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => {
              setQuery('');
              setActive(0);
            }}
          >
            <X size={16} aria-hidden="true" />
          </button>
        )}
      </div>
      {showList && (
        <ul className="map-search-suggest" id={listId} role="listbox">
          {results.map((item, i) => (
            <li
              key={item.key}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              className={i === active ? 'active' : ''}
              onMouseEnter={() => setActive(i)}
              onMouseDown={(e) => {
                e.preventDefault(); // 保持输入框焦点，避免 blur 先关闭列表
                choose(item);
              }}
            >
              {item.kind === 'line' ? (
                <span className="map-search-swatch" style={{ background: item.color }} aria-hidden="true" />
              ) : (
                <span className="map-search-dot" aria-hidden="true" />
              )}
              <span className="map-search-text">
                <span className="map-search-name">{item.name}</span>
                {item.kind === 'station' && item.nameEn && <span className="map-search-sub">{item.nameEn}</span>}
                {item.kind === 'line' && item.systemName && <span className="map-search-sub">{item.systemName}</span>}
              </span>
              <span className="map-search-tag">
                {item.kind === 'line' ? '线路' : '车站'}
                {multiWorld && ` · ${item.world}`}
              </span>
            </li>
          ))}
        </ul>
      )}
      {open && query.trim() && results.length === 0 && <div className="map-search-empty">无匹配的线路或车站</div>}
    </div>
  );
}
