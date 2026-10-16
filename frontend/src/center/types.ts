/** 个人中心接口类型（与后端 internal/httpapi/center*.go、internal/model/admin.go 对应）。 */
import type { FrontendConfig, Player } from '../types';

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

export interface SettingsMeta {
  updatedBy?: string;
  updatedAt?: number;
}

export interface SettingsResponse {
  frontend: FrontendConfig;
  route: RouteSettings;
  defaults: { frontend: FrontendConfig; route: RouteSettings };
  frontendMeta: SettingsMeta;
  routeMeta: SettingsMeta;
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
