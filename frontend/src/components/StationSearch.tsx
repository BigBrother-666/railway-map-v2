import { useMemo, useState } from 'react';
import Fuse from 'fuse.js';

/** 站名模糊搜索输入框（Fuse.js 自动补全）。 */
export function StationSearch({
  value,
  placeholder,
  stations,
  onSelect,
  onClear,
}: {
  value: string | null;
  placeholder: string;
  stations: string[];
  onSelect: (name: string) => void;
  onClear: () => void;
}) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const fuse = useMemo(() => new Fuse(stations, { threshold: 0.4 }), [stations]);

  const results = useMemo(() => {
    if (!query.trim()) return [];
    return fuse.search(query, { limit: 8 }).map((r) => r.item);
  }, [query, fuse]);

  return (
    <div className="station-search">
      <div className="station-search-row">
        <input
          value={open ? query : value ?? ''}
          placeholder={placeholder}
          onFocus={() => {
            setOpen(true);
            setQuery('');
          }}
          onBlur={() => setTimeout(() => setOpen(false), 150)}
          onChange={(e) => setQuery(e.target.value)}
        />
        {value && (
          <button className="icon-btn" title="清除" onClick={onClear}>
            ×
          </button>
        )}
      </div>
      {open && results.length > 0 && (
        <ul className="station-suggest">
          {results.map((name) => (
            <li
              key={name}
              onMouseDown={() => {
                onSelect(name);
                setOpen(false);
              }}
            >
              {name}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
