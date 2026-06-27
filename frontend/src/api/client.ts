/**
 * 后端 REST 客户端（见 FRONTEND_PROMPT.md §4.1）。
 */
import { API_BASE } from '../config';
import type {
  FeatureCollection,
  FrontendConfig,
  Line,
  Meta,
  Player,
  PurchaseRequest,
  PurchaseResult,
  RailwaySystem,
  RideHistoryResponse,
  Train,
} from '../types';

async function getJSON<T>(path: string): Promise<T> {
  const resp = await fetch(`${API_BASE}${path}`, { credentials: 'include' });
  if (!resp.ok) {
    throw new ApiError(resp.status, await safeText(resp));
  }
  return resp.json() as Promise<T>;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function safeText(resp: Response): Promise<string> {
  try {
    return await resp.text();
  } catch {
    return resp.statusText;
  }
}

export const api = {
  config: () => getJSON<FrontendConfig>('/config'),
  meta: () => getJSON<Meta>('/meta'),
  geojson: () => getJSON<FeatureCollection>('/geojson'),
  lines: () => getJSON<Line[]>('/lines'),
  systems: () => getJSON<RailwaySystem[]>('/systems'),
  trains: () => getJSON<Train[]>('/trains'),

  /** 当前登录玩家；未登录抛 ApiError(401)。 */
  me: () => getJSON<Player>('/auth/me'),

  /** 启动微软登录（整页跳转）。 */
  loginUrl: () => `${API_BASE}/auth/login`,

  async testLogin(uuid: string): Promise<Player> {
    const resp = await fetch(`${API_BASE}/auth/test-login`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ uuid }),
    });
    if (!resp.ok) {
      throw new ApiError(resp.status, await safeText(resp));
    }
    return resp.json() as Promise<Player>;
  },

  async logout(): Promise<void> {
    await fetch(`${API_BASE}/auth/logout`, { method: 'POST', credentials: 'include' });
  },

  rideHistory: (page = 1, pageSize = 10) => getJSON<RideHistoryResponse>(`/me/history?page=${page}&pageSize=${pageSize}`),

  async purchase(req: PurchaseRequest): Promise<PurchaseResult> {
    const resp = await fetch(`${API_BASE}/purchase`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    if (!resp.ok) {
      throw new ApiError(resp.status, await safeText(resp));
    }
    return resp.json() as Promise<PurchaseResult>;
  },
};
