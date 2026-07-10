import { useState } from 'react';
import { useStore } from '../store/useStore';
import { getConfig } from '../config';

/** 右侧边栏：按铁路系统分组的线路显示开关（§二.3），可收起（改进 1）。 */
export function RightSidebar() {
  const systems = useStore((s) => s.systems);
  const lines = useStore((s) => s.lines);
  const [collapsed, setCollapsed] = useState(true); // 默认收起线路显示面板

  if (collapsed) {
    return (
      <button className="sidebar-right-toggle" title="展开线路显示" onClick={() => setCollapsed(false)}>
        线路 ☰
      </button>
    );
  }

  // 按系统分组；无系统归入「其它」
  const groups = new Map<string, { name: string; logoUrl: string | null; lines: typeof lines }>();
  for (const line of lines) {
    const sys = systems.find((s) => s.id === line.systemId);
    const key = line.systemId || '__other__';
    if (!groups.has(key)) {
      groups.set(key, { name: sys?.name ?? '其它', logoUrl: sys?.logoUrl ?? null, lines: [] });
    }
    groups.get(key)!.lines.push(line);
  }

  return (
    <div className="sidebar sidebar-right">
      <div className="panel">
        <div className="panel-header">
          <h3>线路显示</h3>
          <button className="icon-btn" title="收起" onClick={() => setCollapsed(true)}>
            ›
          </button>
        </div>
        {[...groups.entries()].map(([sid, group]) => (
          <LineGroup key={sid} sid={sid} name={group.name} logoUrl={group.logoUrl} lines={group.lines} />
        ))}
      </div>
    </div>
  );
}

/** 一个铁路系统分组：logo + 系统名 + 总开关，线路列表默认收起，点击展开（改进 3）。 */
function LineGroup({
  sid,
  name,
  logoUrl,
  lines,
}: {
  sid: string;
  name: string;
  logoUrl: string | null;
  lines: { id: string; color: string; name: string }[];
}) {
  const hidden = useStore((s) => s.hiddenLines);
  const toggleLine = useStore((s) => s.toggleLine);
  const setSystemVisible = useStore((s) => s.setSystemVisible);
  const [expanded, setExpanded] = useState(false); // 默认收起

  const allHidden = lines.every((l) => hidden.has(l.id));

  return (
    <div className="line-group">
      <div className="line-group-head">
        <button className="group-toggle" onClick={() => setExpanded((v) => !v)} title={expanded ? '收起' : '展开'}>
          <span className={`caret ${expanded ? 'open' : ''}`}>▸</span>
          <img className="system-logo" src={logoUrl || getConfig().defaultSystemLogo} alt="" />
          <span className="group-name">{name}</span>
        </button>
        <label className="switch" onClick={(e) => e.stopPropagation()}>
          <input
            type="checkbox"
            checked={!allHidden}
            onChange={(e) => setSystemVisible(sid === '__other__' ? '' : sid, e.target.checked)}
          />
          <span className="slider" />
        </label>
      </div>
      {expanded &&
        lines.map((l) => (
          <label key={l.id} className="line-row">
            <input type="checkbox" checked={!hidden.has(l.id)} onChange={() => toggleLine(l.id)} />
            <span className="line-dot" style={{ background: l.color }} />
            <span className="line-name">{l.name}</span>
          </label>
        ))}
    </div>
  );
}
