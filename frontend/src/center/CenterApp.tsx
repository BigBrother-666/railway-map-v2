/** 个人中心外壳：鉴权、侧栏导航（按权限动态生成）、顶栏（面包屑 / 主题切换 / 头像）与页面路由。 */
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { api } from '../api/client';
import { avatarUrl, getConfig, setRuntimeConfig } from '../config';
import { centerApi } from './api';
import { navigate, onLinkClick, parseRoute, usePath, type Route } from './router';
import { useTheme, type ThemeChoice } from '../theme';
import { ThemeToggle } from '../components/ThemeToggle';
import { PERM, type Profile } from './types';
import { Empty, ErrorState, Loading, useAsync } from './ui';
import { OverviewPage } from './pages/Overview';
import { SystemPage } from './pages/System';
import { StationAliasPage } from './pages/StationAliases';
import { StationGroupPage } from './pages/StationGroups';
import { RbacPage } from './pages/Rbac';
import { SettingsPage } from './pages/Settings';
import { AuditPage } from './pages/Audit';
import './center.css';

export interface CenterContext {
  profile: Profile;
  /** 系统 / 权限变化后刷新侧栏。 */
  refreshProfile: () => Promise<void>;
}

const MOBILE_QUERY = '(max-width: 860px)';
const COLLAPSE_KEY = 'center-sidebar-collapsed';

function readCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSE_KEY) === '1';
  } catch {
    return false;
  }
}

const ICON_PROPS = {
  viewBox: '0 0 24 24',
  width: 18,
  height: 18,
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
} as const;

/** 概览：仪表盘四宫格 */
const OverviewIcon = (
  <svg {...ICON_PROPS}>
    <rect x="3" y="3" width="7" height="9" rx="1.5" />
    <rect x="14" y="3" width="7" height="5" rx="1.5" />
    <rect x="14" y="12" width="7" height="9" rx="1.5" />
    <rect x="3" y="16" width="7" height="5" rx="1.5" />
  </svg>
);

/** 车站别名：标签 */
const AliasIcon = (
  <svg {...ICON_PROPS}>
    <path d="M20.6 13.4 13.4 20.6a2 2 0 0 1-2.8 0L3 13V3h10l7.6 7.6a2 2 0 0 1 0 2.8Z" />
    <circle cx="8" cy="8" r="1.5" fill="currentColor" stroke="none" />
  </svg>
);

export function CenterApp() {
  const path = usePath();
  const route = parseRoute(path);
  const { choice, setChoice, theme } = useTheme();
  const [drawer, setDrawer] = useState(false);
  const [collapsed, setCollapsed] = useState(readCollapsed);
  const [configReady, setConfigReady] = useState(false);
  const profile = useAsync(() => centerApi.profile(), []);

  // 头像模板、货币名等来自运行时配置
  useEffect(() => {
    api
      .config()
      .then(setRuntimeConfig)
      .catch(() => undefined)
      .finally(() => setConfigReady(true));
  }, []);

  useEffect(() => {
    document.documentElement.dataset.centerTheme = theme;
    document.title = '个人中心 · 帕拉伦铁路';
  }, [theme]);

  useEffect(() => setDrawer(false), [path]);

  // 窄屏切换抽屉，宽屏折叠 / 展开侧栏（记住选择）
  const toggleNav = () => {
    if (window.matchMedia(MOBILE_QUERY).matches) {
      setDrawer((v) => !v);
      return;
    }
    setCollapsed((v) => {
      try {
        localStorage.setItem(COLLAPSE_KEY, v ? '0' : '1');
      } catch {
        // localStorage 不可用时只在本次会话生效
      }
      return !v;
    });
  };

  let body: ReactNode;
  if (profile.loading && !profile.data) {
    body = <div className="c-boot"><Loading rows={4} /></div>;
  } else if (profile.error?.status === 401) {
    body = <LoginRequired />;
  } else if (profile.error || !profile.data) {
    body = <div className="c-boot">{profile.error && <ErrorState error={profile.error} onRetry={profile.reload} />}</div>;
  } else {
    const ctx: CenterContext = { profile: profile.data, refreshProfile: profile.reload };
    body = (
      <div className={`c-shell ${drawer ? 'drawer-open' : ''} ${collapsed ? 'sidebar-collapsed' : ''}`}>
        <Sidebar profile={profile.data} route={route} />
        <div className="c-drawer-mask" onClick={() => setDrawer(false)} />
        <div className="c-main">
          <TopBar
            profile={profile.data}
            route={route}
            themeChoice={choice}
            setTheme={setChoice}
            onMenu={toggleNav}
          />
          <main className="c-content">{configReady && <Page route={route} ctx={ctx} />}</main>
        </div>
      </div>
    );
  }
  return <div className={`center theme-${theme}`}>{body}</div>;
}

function Page({ route, ctx }: { route: Route; ctx: CenterContext }) {
  const perms = ctx.profile.permissions;
  const need = (perm: string, node: ReactNode) =>
    perms.includes(perm) ? node : <Empty title="没有权限" hint="你没有访问该页面的权限，如有需要请联系管理员。" />;
  switch (route.page) {
    case 'overview':
      return <OverviewPage ctx={ctx} />;
    case 'system':
      return <SystemPage key={route.id} id={route.id} ctx={ctx} />;
    case 'station-aliases':
      return need(PERM.stationAlias, <StationAliasPage ctx={ctx} />);
    case 'station-groups':
      return need(PERM.stationGroup, <StationGroupPage ctx={ctx} />);
    case 'rbac':
      return need(PERM.rbac, <RbacPage ctx={ctx} />);
    case 'settings':
      return need(PERM.settings, <SettingsPage />);
    case 'audit':
      return need(PERM.audit, <AuditPage />);
    default:
      return <Empty title="页面不存在" action={<a className="c-btn" href="/me" onClick={(e) => onLinkClick(e, '/me')}>返回概览</a>} />;
  }
}

function NavLink({ to, active, icon, children }: { to: string; active: boolean; icon: ReactNode; children: ReactNode }) {
  return (
    <a
      href={to}
      className={`c-nav-link ${active ? 'active' : ''}`}
      aria-current={active ? 'page' : undefined}
      onClick={(e) => onLinkClick(e, to)}
    >
      <span className="c-nav-icon" aria-hidden>
        {icon}
      </span>
      <span className="c-nav-text">{children}</span>
    </a>
  );
}

function Sidebar({ profile, route }: { profile: Profile; route: Route }) {
  const perms = profile.permissions;
  const has = (p: string) => perms.includes(p);
  const stationItems = [
    has(PERM.stationAlias) && (
      <NavLink key="a" to="/me/station-aliases" icon={AliasIcon} active={route.page === 'station-aliases'}>
        车站别名
      </NavLink>
    ),
    has(PERM.stationGroup) && (
      <NavLink key="g" to="/me/station-groups" icon="⇄" active={route.page === 'station-groups'}>
        车站组
      </NavLink>
    ),
  ].filter(Boolean);
  const adminItems = [
    has(PERM.rbac) && (
      <NavLink key="r" to="/admin/rbac" icon="⚿" active={route.page === 'rbac'}>
        权限管理
      </NavLink>
    ),
    has(PERM.settings) && (
      <NavLink key="s" to="/admin/settings" icon="⚙" active={route.page === 'settings'}>
        服务器配置
      </NavLink>
    ),
    has(PERM.audit) && (
      <NavLink key="l" to="/admin/audit" icon="☰" active={route.page === 'audit'}>
        操作日志
      </NavLink>
    ),
  ].filter(Boolean);

  return (
    <aside className="c-sidebar" aria-label="个人中心导航">
      <a href="/" className="c-brand">
        <img className="c-brand-mark" src="/favicon.png" alt="" />
        <span>帕拉伦铁路</span>
      </a>
      <nav className="c-nav">
        <NavLink to="/me" icon={OverviewIcon} active={route.page === 'overview'}>
          概览
        </NavLink>
        {profile.systems.length > 0 && (
          <div className="c-nav-group">
            <div className="c-nav-group-title">铁路系统</div>
            {profile.systems.map((s) => {
              const to = `/me/systems/${encodeURIComponent(s.id)}`;
              return (
                <NavLink
                  key={s.id}
                  to={to}
                  icon={<img className="c-nav-logo" src={s.logoUrl || getConfig().defaultSystemLogo} alt="" />}
                  active={route.page === 'system' && route.id === s.id}
                >
                  {s.name}
                  {!s.isMember && <span className="c-nav-tag">管理</span>}
                </NavLink>
              );
            })}
          </div>
        )}
        {stationItems.length > 0 && (
          <div className="c-nav-group">
            <div className="c-nav-group-title">车站</div>
            {stationItems}
          </div>
        )}
        {adminItems.length > 0 && (
          <div className="c-nav-group">
            <div className="c-nav-group-title">管理</div>
            {adminItems}
          </div>
        )}
      </nav>
      <a href="/" className="c-nav-back">
        ← 返回地图
      </a>
    </aside>
  );
}

function TopBar({
  profile,
  route,
  themeChoice,
  setTheme,
  onMenu,
}: {
  profile: Profile;
  route: Route;
  themeChoice: ThemeChoice;
  setTheme: (c: ThemeChoice) => void;
  onMenu: () => void;
}) {
  const [menu, setMenu] = useState(false);
  const crumb = useMemo(() => breadcrumb(route, profile), [route, profile]);
  const logout = async () => {
    await api.logout();
    location.href = '/';
  };
  return (
    <header className="c-topbar">
      <button className="c-icon-btn c-menu-btn" onClick={onMenu} aria-label="展开 / 收起导航" title="展开 / 收起导航">
        ☰
      </button>
      <div className="c-crumb">
        <span className="c-crumb-root">个人中心</span>
        {crumb.map((c) => (
          <span key={c} className="c-crumb-item">
            {c}
          </span>
        ))}
      </div>
      <div className="c-topbar-right">
        <ThemeToggle className="c-icon-btn" choice={themeChoice} onChange={setTheme} />
        <div className="c-user">
          <button className="c-avatar" onClick={() => setMenu((v) => !v)} aria-haspopup="menu" aria-expanded={menu}>
            <img src={avatarUrl(profile.player.uuid || profile.player.name)} alt="" />
            <span className="c-user-name">{profile.player.name}</span>
          </button>
          {menu && (
            <div className="c-popover" role="menu" onMouseLeave={() => setMenu(false)}>
              <a role="menuitem" href="/" className="c-popover-item">
                返回地图
              </a>
              <button role="menuitem" className="c-popover-item" onClick={logout}>
                退出登录
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}

function breadcrumb(route: Route, profile: Profile): string[] {
  switch (route.page) {
    case 'overview':
      return ['概览'];
    case 'system':
      return ['铁路系统', profile.systems.find((s) => s.id === route.id)?.name ?? route.id];
    case 'station-aliases':
      return ['车站别名'];
    case 'station-groups':
      return ['车站组'];
    case 'rbac':
      return ['权限管理'];
    case 'settings':
      return ['服务器配置'];
    case 'audit':
      return ['操作日志'];
    default:
      return [];
  }
}

function LoginRequired() {
  const cfg = getConfig();
  return (
    <div className="c-boot">
      <div className="c-login">
        <h1>个人中心</h1>
        <p>登录后可查看乘车统计、管理所在的铁路系统。</p>
        <div className="c-login-actions">
          <a className="c-btn primary" href={api.loginUrl()}>
            使用 Microsoft 账号登录
          </a>
          <a className="c-btn" href="/" onClick={() => navigate('/')}>
            返回地图
          </a>
        </div>
        {cfg.testAuthEnabled && <p className="c-muted">测试登录请在地图页右上角进行。</p>}
      </div>
    </div>
  );
}
