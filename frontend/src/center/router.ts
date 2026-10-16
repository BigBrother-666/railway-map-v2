/**
 * 个人中心的极简路由：基于 location.pathname + history.pushState。
 * 只有个人中心一个多页面场景，路由表很小，不引入路由库。
 */
import { useEffect, useState } from 'react';

const EVENT = 'bcts:navigate';

/** 是否为个人中心路径（main.tsx 据此决定加载地图还是个人中心）。 */
export function isCenterPath(pathname: string): boolean {
  return pathname === '/me' || pathname.startsWith('/me/') || pathname.startsWith('/admin');
}

export function navigate(to: string) {
  if (to === location.pathname + location.search) return;
  history.pushState(null, '', to);
  window.dispatchEvent(new Event(EVENT));
  window.scrollTo(0, 0);
}

/** 订阅当前路径（含浏览器前进 / 后退）。 */
export function usePath(): string {
  const [path, setPath] = useState(location.pathname);
  useEffect(() => {
    const update = () => setPath(location.pathname);
    window.addEventListener(EVENT, update);
    window.addEventListener('popstate', update);
    return () => {
      window.removeEventListener(EVENT, update);
      window.removeEventListener('popstate', update);
    };
  }, []);
  return path;
}

export type Route =
  | { page: 'overview' }
  | { page: 'system'; id: string }
  | { page: 'station-aliases' }
  | { page: 'station-groups' }
  | { page: 'rbac' }
  | { page: 'settings' }
  | { page: 'audit' }
  | { page: 'not-found' };

/** 解析路径为页面。 */
export function parseRoute(path: string): Route {
  const p = path.replace(/\/+$/, '') || '/';
  if (p === '/me') return { page: 'overview' };
  const sys = /^\/me\/systems\/([^/]+)$/.exec(p);
  if (sys) return { page: 'system', id: decodeURIComponent(sys[1]) };
  switch (p) {
    case '/me/station-aliases':
      return { page: 'station-aliases' };
    case '/me/station-groups':
      return { page: 'station-groups' };
    case '/admin/rbac':
      return { page: 'rbac' };
    case '/admin/settings':
      return { page: 'settings' };
    case '/admin/audit':
      return { page: 'audit' };
  }
  return { page: 'not-found' };
}

/** 站内链接点击：普通左键走 pushState，其余（新标签等）交给浏览器。 */
export function onLinkClick(e: React.MouseEvent<HTMLAnchorElement>, to: string) {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  navigate(to);
}
