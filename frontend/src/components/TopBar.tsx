import { useState } from 'react';
import { useStore } from '../store/useStore';
import { api } from '../api/client';
import { avatarUrl, getConfig } from '../config';

/** 顶栏：标题、世界切换、插件在线状态、登录 / 玩家信息。 */
export function TopBar() {
  const worlds = useStore((s) => s.worlds);
  const currentWorld = useStore((s) => s.currentWorld);
  const setWorld = useStore((s) => s.setWorld);
  const meta = useStore((s) => s.meta);
  const player = useStore((s) => s.player);
  const logout = useStore((s) => s.logout);
  const refreshPlayer = useStore((s) => s.refreshPlayer);
  const openRideHistory = useStore((s) => s.openRideHistory);
  const openTrainList = useStore((s) => s.openTrainList);
  const [menuOpen, setMenuOpen] = useState(false);

  const testLogin = async () => {
    const uuids = getConfig().testAuthUUIDs ?? [];
    if (uuids.length === 0) return;
    const uuid = window.prompt(`输入测试登录 UUID：\n${uuids.join('\n')}`, uuids[0]);
    if (!uuid) return;
    await api.testLogin(uuid.trim());
    await refreshPlayer();
  };

  return (
    <div className="topbar">
      <div className="topbar-left">
        <span className="brand">帕拉伦铁路线路图</span>
        <button className="btn ghost topbar-trains-btn" onClick={openTrainList} title="实时列车列表">
          🚆 实时列车
        </button>
        <span className={`status-dot ${meta?.online ? 'online' : 'offline'}`} />
        <span className="status-text">{meta?.online ? '实时列车数据正常' : '实时列车数据异常'}</span>
      </div>
      {worlds.length > 1 && (
        <div className="topbar-center">
          <select value={currentWorld} onChange={(e) => setWorld(e.target.value)}>
            {worlds.map((w) => (
              <option key={w} value={w}>
                {w}
              </option>
            ))}
          </select>
        </div>
      )}
      <div className="topbar-right">
        {player ? (
          <div className="player-menu">
            <button className="avatar-btn" title={player.name} onClick={() => setMenuOpen((v) => !v)}>
              <img src={avatarUrl(player.uuid || player.name)} alt="" />
            </button>
            {menuOpen && (
              <div className="player-popover">
                <div className="player-popover-name">{player.name}</div>
                <button
                  className="menu-action"
                  onClick={() => {
                    setMenuOpen(false);
                    openRideHistory();
                  }}
                >
                  乘车历史
                </button>
                <button
                  className="menu-action"
                  onClick={() => {
                    setMenuOpen(false);
                    logout();
                  }}
                >
                  退出登录
                </button>
              </div>
            )}
          </div>
        ) : (
          <div className="login-actions">
            {getConfig().testAuthEnabled && (
              <button className="btn ghost" onClick={testLogin}>
                测试登录
              </button>
            )}
            <a className="btn primary" href={api.loginUrl()}>
              登录
            </a>
          </div>
        )}
      </div>
    </div>
  );
}
