/**
 * 个人中心 REST 客户端（/api/v1/me/*、/api/v1/admin/*）。
 * 失败统一抛 CenterError：status + 后端错误码 / 文案，以及字段级错误（field / fields / conflicts）。
 */
import { API_BASE } from '../config';
import type {
  AuditPage,
  FrontendConfig,
  OverviewResponse,
  PlayerRow,
  PrivateLine,
  Profile,
  RbacOverview,
  RouteSettings,
  SettingsResponse,
  StationAliasesResponse,
  StationGroupsResponse,
  SystemDetail,
  WriteResult,
} from './types';

export interface FieldError {
  field: string;
  message: string;
}

export class CenterError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public field?: string,
    public fields?: FieldError[],
    public conflicts?: Record<string, string>,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'include' };
  if (body instanceof FormData) {
    init.body = body;
  } else if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  let resp: Response;
  try {
    resp = await fetch(`${API_BASE}${path}`, init);
  } catch {
    throw new CenterError(0, 'network', '网络错误，请检查连接后重试');
  }
  const text = await resp.text();
  let data: any = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    // 非 JSON 响应（如网关错误页）
  }
  if (!resp.ok) {
    const err = data?.error;
    throw new CenterError(
      resp.status,
      err?.code ?? 'http-' + resp.status,
      err?.message ?? defaultMessage(resp.status),
      data?.field || undefined,
      data?.fields,
      data?.conflicts ?? undefined,
    );
  }
  return data as T;
}

function defaultMessage(status: number): string {
  if (status === 401) return '请先登录';
  if (status === 403) return '没有权限';
  if (status === 404) return '内容不存在';
  return `请求失败（${status}）`;
}

const enc = encodeURIComponent;

export const centerApi = {
  profile: () => request<Profile>('GET', '/me/profile'),
  overview: () => request<OverviewResponse>('GET', '/me/overview'),
  searchPlayers: (q: string) => request<PlayerRow[]>('GET', `/me/players?q=${enc(q)}`),

  system: (id: string) => request<SystemDetail>('GET', `/me/systems/${enc(id)}`),
  updateSystem: (id: string, body: { name: string; pricePerKm: number | null; members?: string[] }) =>
    request<WriteResult>('PUT', `/me/systems/${enc(id)}`, body),
  deleteSystem: (id: string) => request<WriteResult>('DELETE', `/me/systems/${enc(id)}`),
  uploadLogo: (id: string, kind: 'web' | 'mc', file: File) => {
    const fd = new FormData();
    fd.append('file', file);
    return request<WriteResult>('PUT', `/me/systems/${enc(id)}/logo/${kind}`, fd);
  },
  clearLogo: (id: string, kind: 'web' | 'mc') => request<WriteResult>('DELETE', `/me/systems/${enc(id)}/logo/${kind}`),
  systemLines: (id: string) => request<PrivateLine[]>('GET', `/me/systems/${enc(id)}/lines`),
  updateLine: (line: PrivateLine) => request<WriteResult>('PUT', `/me/lines/${enc(line.id)}`, line),
  deleteLine: (id: string) => request<WriteResult>('DELETE', `/me/lines/${enc(id)}`),

  stationAliases: () => request<StationAliasesResponse>('GET', '/me/station-aliases/'),
  upsertStationAlias: (station: string, englishName: string) =>
    request<WriteResult>('PUT', `/me/station-aliases/${enc(station)}`, { englishName }),
  deleteStationAlias: (station: string) => request<WriteResult>('DELETE', `/me/station-aliases/${enc(station)}`),
  stationGroups: () => request<StationGroupsResponse>('GET', '/me/station-groups/'),
  upsertStationGroup: (group: string, stations: string[]) =>
    request<WriteResult>('PUT', `/me/station-groups/${enc(group)}`, { stations }),
  deleteStationGroup: (group: string) => request<WriteResult>('DELETE', `/me/station-groups/${enc(group)}`),

  rbac: () => request<RbacOverview>('GET', '/admin/rbac/'),
  createRole: (body: { name: string; description: string; permissions: string[] }) =>
    request<{ ok: true; id: number }>('POST', '/admin/rbac/roles', body),
  updateRole: (id: number, body: { name: string; description: string; permissions: string[] }) =>
    request<WriteResult>('PUT', `/admin/rbac/roles/${id}`, body),
  deleteRole: (id: number) => request<WriteResult>('DELETE', `/admin/rbac/roles/${id}`),
  grantRole: (uuid: string, roleId: number) => request<WriteResult>('POST', '/admin/rbac/users', { uuid, roleId }),
  revokeRole: (uuid: string, roleId: number) =>
    request<WriteResult>('DELETE', `/admin/rbac/users/${enc(uuid)}/roles/${roleId}`),

  settings: () => request<SettingsResponse>('GET', '/admin/settings/'),
  saveFrontend: (f: FrontendConfig) => request<SettingsResponse>('PUT', '/admin/settings/frontend', f),
  saveRoute: (r: RouteSettings) => request<SettingsResponse>('PUT', '/admin/settings/route', r),
  resetSettings: (section: 'frontend' | 'route') =>
    request<SettingsResponse>('POST', `/admin/settings/${section}/reset`),

  audit: (q: { page: number; actor?: string; action?: string; from?: number; to?: number }) => {
    const p = new URLSearchParams({ page: String(q.page), pageSize: '20' });
    if (q.actor) p.set('actor', q.actor);
    if (q.action) p.set('action', q.action);
    if (q.from) p.set('from', String(q.from));
    if (q.to) p.set('to', String(q.to));
    return request<AuditPage>('GET', `/admin/audit?${p}`);
  },
};
