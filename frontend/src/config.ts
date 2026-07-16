import type { FrontendConfig } from './types';

export const API_BASE = '/api/v1';

/** 联络线所属的铁路系统 id：这类线路不在线路栏列出，且随两端节点可见性自动显隐。 */
export const CONTACT_SYSTEM_ID = 'contact';

const DEFAULT_CONFIG: FrontendConfig = {
  realtimeWsPath: '/api/v1/realtime',
  defaultWorld: 'world1',
  currencyName: '帕元',
  themeColor: '#ffd400',
  worldTiles: { world1: { zoom: 14 } },
  mapStyle: {
    lineWidth: 3,
    highlightWidth: 7,
    dimOpacity: 0.2,
    lineOpacity: 0.9,
    stationRadius: 6,
    stationStrokeWidth: 2,
    stationTextSize: 12,
    stationMergePixelDistance: 28,
    trainIconSize: 0.6,
  },
  trainIcons: {
    express:
      'data:image/svg+xml;utf8,' +
      encodeURIComponent(
        '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="13" fill="#ff5252" stroke="#fff" stroke-width="3"/></svg>',
      ),
    normal:
      'data:image/svg+xml;utf8,' +
      encodeURIComponent(
        '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="13" fill="#42a5f5" stroke="#fff" stroke-width="3"/></svg>',
      ),
  },
  defaultSystemLogo:
    'data:image/svg+xml;utf8,' +
    encodeURIComponent(
      '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20"><rect width="20" height="20" rx="4" fill="#3a3f4b"/><path d="M6 5h8v7a3 3 0 0 1-3 3H9a3 3 0 0 1-3-3z" fill="#8a90a0"/><circle cx="8" cy="16" r="1" fill="#8a90a0"/><circle cx="12" cy="16" r="1" fill="#8a90a0"/></svg>',
    ),
  avatarUrlTemplate: 'https://mineskin.eu/helm/{player}',
  defaultPricePerKm: 0.2,
  testAuthEnabled: false,
  testAuthUUIDs: [],
  maxDistanceResults: 5,
  maxPriceResults: 5,
  searchWeightDistance: 0.5,
  searchWeightPrice: 0.5,
  minDirectResults: 1,
  maxTransferResults: 3,
  transferMinImprovement: 0.2,
  routeSearchTimeoutMs: 10000,
};

let runtimeConfig: FrontendConfig = DEFAULT_CONFIG;

export function setRuntimeConfig(config: FrontendConfig) {
  runtimeConfig = {
    ...DEFAULT_CONFIG,
    ...config,
    worldTiles: config.worldTiles ?? DEFAULT_CONFIG.worldTiles,
    mapStyle: { ...DEFAULT_CONFIG.mapStyle, ...config.mapStyle },
    trainIcons: { ...DEFAULT_CONFIG.trainIcons, ...config.trainIcons },
  };
  applyThemeColor(runtimeConfig.themeColor);
}

/** 把主题色写入 CSS 变量 --accent；非法（非 #RRGGBB）时忽略，保留样式表默认值。 */
function applyThemeColor(color: string | undefined) {
  if (typeof document === 'undefined') return; // Worker/SSR 环境无 document
  if (!color || !/^#[0-9a-fA-F]{6}$/.test(color)) return;
  document.documentElement.style.setProperty('--accent', color);
}

export function getConfig(): FrontendConfig {
  return runtimeConfig;
}

export function avatarUrl(player: string): string {
  return getConfig().avatarUrlTemplate.replace('{player}', encodeURIComponent(player));
}
