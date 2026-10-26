/** 个人中心接口类型（与后端 internal/httpapi/center*.go、internal/model/admin.go 对应）。 */
import type { FrontendConfig, Player, WorldTileConfig } from '../types';

export type { FrontendConfig };

/** 权限点（与后端 internal/rbac 常量一致）。 */
export const PERM = {
  rbac: 'admin.rbac',
  settings: 'admin.settings',
  audit: 'admin.audit',
  systemManage: 'system.manage_all',
  stationAlias: 'station.alias',
  stationGroup: 'station.group',
} as const;

export interface ProfileSystem {
  id: string;
  name: string;
  isCreator: boolean;
  isMember: boolean;
  logoUrl?: string;
}

export interface Profile {
  player: Player;
  permissions: string[];
  systems: ProfileSystem[];
  pluginOnline: boolean;
}

export interface WriteResult {
  ok: boolean;
  warnings?: string[] | null;
}

export interface PlayerRow {
  uuid: string;
  name: string;
}

export interface DayValue {
  day: number;
  value: number;
}

export interface CountItem {
  key: string;
  count: number;
}

export interface PlayerStats {
  rideCount: number;
  totalKm: number;
  totalFare: number;
  ticketCount: number;
  ticketSpent: number;
  webTicketCount: number;
  cardChargeCount: number;
  cardChargeAmount: number;
  /** 刷卡乘车次数与花费（卡内余额支付，不计入购票）。 */
  cardRideCount: number;
  cardRideSpent: number;
  visitedStations: number;
  firstRideAt?: number;
  longestRide?: { startStation: string; endStation: string; distance: number; endedAt: number };
  favoriteLines: CountItem[];
  topStations: CountItem[];
  dailyRides: DayValue[];
}

export interface SystemOverview {
  id: string;
  name: string;
  isCreator: boolean;
  income: number;
  withdrawn: number;
  lineCount: number;
  stationCount: number;
  trackKm: number;
  dailyRevenue: DayValue[];
  dailyRiders: DayValue[];
}

export interface OverviewResponse {
  player: Player;
  stats: PlayerStats;
  lineNames: Record<string, string>;
  systems: SystemOverview[];
}

export interface SystemDetail {
  id: string;
  name: string;
  pricePerKm: number | null;
  logoUrl?: string | null;
  mcLogoUrl?: string | null;
  creator: string;
  members: string[];
  income: number;
  withdrawn: number;
  creatorName: string;
  memberNames: Record<string, string>;
  lineCount: number;
  /** 实际生效的每公里票价（未单独配置时为插件全局默认值）。 */
  effectivePricePerKm?: number | null;
  canEdit: boolean;
  canDelete: boolean;
  canEditMembers: boolean;
}

/** 线路完整配置（字段与插件 railway_routes.yml 一一对应）。 */
export interface PrivateLine {
  id: string;
  systemId: string;
  name: string;
  color: string;
  bossbarStations: string[];
  bossbarArrivalNotice?: string;
  bossbarColor?: string;
  noticeArrival?: string;
  noticeDeparture?: string;
  /** 仅展示线路（railway_routes.yml 的 display-only）：寻路 / 购票不经过该线路。 */
  displayOnly: boolean;
}

export interface StationAliasesResponse {
  aliases: Record<string, string>;
  stations: string[];
}

export interface StationGroupsResponse {
  groups: Record<string, string[]>;
  stations: string[];
}

export interface PermissionInfo {
  key: string;
  name: string;
  description: string;
}

export interface Role {
  id: number;
  name: string;
  description: string;
  builtin: boolean;
  createdAt: number;
  permissions: string[];
  userCount: number;
}

export interface UserRoleRow {
  uuid: string;
  name: string;
  roleId: number;
  roleName: string;
  grantedBy: string;
  grantedAt: number;
}

export interface RbacOverview {
  permissions: PermissionInfo[];
  roles: Role[];
  users: UserRoleRow[];
  superAdminUuid: string;
  superRoleId: number;
}

export interface RouteSettings {
  queryRateLimitPerSecond: number;
  maxDistanceResults: number;
  maxPriceResults: number;
  searchWeightDistance: number;
  searchWeightPrice: number;
  minDirectResults: number;
  maxTransferResults: number;
  transferMinImprovement: number;
  computeTimeoutMs: number;
}

export interface PluginSettings {
  heartbeatSeconds: number;
  purchaseTimeoutSeconds: number;
  purchaseMinIntervalSeconds: number;
  configTimeoutSeconds: number;
}

export interface RealtimeSettings {
  trainTimeoutSeconds: number;
  clientSendBuffer: number;
}

/** 服务器配置的 frontend 段（世界底图在 tiles 段，GET /config 合并下发）。 */
export type FrontendSettings = Omit<FrontendConfig, 'worldTiles' | 'testAuthEnabled' | 'testAuthUUIDs'>;

export interface TileSourceSettings {
  id: string;
  type: string;
  url: string;
  map?: string;
}

/** 某世界使用某外部来源时的参数（对齐参数由外部地图推导）。 */
export interface ExternalLayerSettings {
  source: string;
  zoom?: number;
  opacity?: number;
  minZoom?: number;
  maxZoom?: number;
  center?: [number, number];
  synthZoomLevels?: number;
  synthMarginBlocks?: number;
}

/** 某世界的一个底图配置：static 与 external 恰有一个。 */
export interface WorldLayerSettings {
  static?: WorldTileConfig;
  external?: ExternalLayerSettings;
}

export interface TileSourcesSettings {
  /** 每个世界的底图列表，顺序即优先级。 */
  worlds: Record<string, WorldLayerSettings[]>;
  sources: TileSourceSettings[];
  cacheMaxMb: number;
  cacheTtlSeconds: number;
  synthCacheTtlSeconds: number;
  blankCacheTtlSeconds: number;
  browserCacheSeconds: number;
  discoveryRefreshSeconds: number;
  upstreamTimeoutSeconds: number;
  upstreamConcurrency: number;
  renderJpegQuality: number;
}

/** GET /admin/settings/tiles/status：外部地图来源发现状态。 */
export interface TileSourcesStatus {
  sources: {
    id: string;
    type: string;
    url: string;
    error?: string;
    discoveredAt?: number;
    /** tile：上游原生底图参数（未含合成层级），用于展示推导值。 */
    worlds: { name: string; map?: string; skip?: string; tile?: WorldTileConfig }[];
  }[];
  cacheItems: number;
  cacheBytes: number;
}

export type SettingsSection = 'frontend' | 'route' | 'plugin' | 'realtime' | 'tiles';

export interface SettingsMeta {
  updatedBy?: string;
  updatedAt?: number;
}

export interface SettingsResponse {
  frontend: FrontendSettings;
  route: RouteSettings;
  plugin: PluginSettings;
  realtime: RealtimeSettings;
  tiles: TileSourcesSettings;
  defaults: {
    frontend: FrontendSettings;
    route: RouteSettings;
    plugin: PluginSettings;
    realtime: RealtimeSettings;
    tiles: TileSourcesSettings;
  };
  frontendMeta: SettingsMeta;
  routeMeta: SettingsMeta;
  pluginMeta: SettingsMeta;
  realtimeMeta: SettingsMeta;
  tilesMeta: SettingsMeta;
}

export interface AuditEntry {
  id: number;
  actorUuid: string;
  actorName: string;
  action: string;
  target: string;
  detail?: string;
  success: boolean;
  reason?: string;
  createdAt: number;
}

export interface AuditPage {
  items: AuditEntry[];
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
}
