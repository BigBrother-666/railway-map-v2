/**
 * 与后端契约一一对应的共享类型（见 FRONTEND_PROMPT.md §四）。
 */

/** 点（车站 / 道岔）节点属性。 */
export interface PointProps {
  id: string;
  type: 'station' | 'switch';
  world: string;
  name?: string;
  lineIds?: string[];
  railwaySystemIds?: string[];
  prev?: string[];
  next?: string[];
  stationIds?: string[];
  mergedCount?: number;
}

/** 线（轨道段）属性。 */
export interface LineStringProps {
  id: string;
  from: string;
  to: string;
  lineId: string;
  world: string;
  railwaySystemId?: string;
  color: string;
  length: number;
  layer?: number;
  departDir?: string;
}

export type Feature = GeoJSON.Feature<GeoJSON.Geometry, PointProps | LineStringProps>;
export type FeatureCollection = GeoJSON.FeatureCollection<GeoJSON.Geometry, PointProps | LineStringProps>;

/** 线路（GET /api/v1/lines）。 */
export interface Line {
  id: string;
  name: string;
  color: string;
  systemId: string;
  stations: string[];
  ring: boolean;
  /** 折返站（:RV）干净站名，寻路时跳过（与插件同步）。 */
  reverseStations?: string[];
}

/** 铁路系统（GET /api/v1/systems）。 */
export interface RailwaySystem {
  id: string;
  name: string;
  pricePerKm: number | null;
  logoUrl: string | null;
}

/** 元信息（GET /api/v1/meta）。 */
export interface Meta {
  geoVersion: string;
  online: boolean;
  serverTime: number;
}

/** 路径查询结果（本地寻路与 /route/query 统一结构，见 §4.6）。 */
export interface RouteSegment {
  lineId: string;
  distance: number;
  systemId: string;
}

export interface StationStep {
  stationName: string;
  departLineId?: string;
}

export interface FareDetail {
  systemId: string;
  systemName: string;
  distance: number;
  price: number;
  rate: number;
}

export interface RoutePath {
  stations: string[];
  stationSteps?: StationStep[];
  nodeIds: string[];
  lineIdSequence: string[];
  departDirectionSequence?: string[];
  distance: number;
  segments: RouteSegment[];
  fareDetails?: FareDetail[];
  estimatedFare: number;
}

/** 在线购票（§4.7）。 */
export interface PurchaseRequest {
  nodeIds: string[];
  lineIdSequence: string[];
  speedKph?: number;
  maxUses?: number;
}

export type PurchaseReason =
  | 'player-offline'
  | 'invalid-route'
  | 'insufficient-funds'
  | 'traversal-running'
  | 'purchase-disabled'
  | 'internal-error';

export interface PurchaseResult {
  success: boolean;
  reason?: PurchaseReason;
  ticketName?: string;
  price?: number;
  balanceAfter?: number;
}

/** 登录玩家（GET /api/v1/auth/me）。 */
export interface Player {
  uuid: string;
  name: string;
}

export interface WorldTileConfig {
  tileUrl?: string;
  zoom?: number;
  tileSize?: number;
  opacity?: number;
  minNativeZoom?: number;
  maxNativeZoom?: number;
  minZoom?: number;
  maxZoom?: number;
  scheme?: 'xyz' | 'tms';
  /** 1 个游戏方块对应多少「原生瓦片像素」，整体缩放，默认 1。 */
  mapScale?: number;
  /** [x, z] 游戏坐标整体平移（游戏单位），用于对准瓦片，默认 [0, 0]。 */
  mapOffset?: [number, number];
}

export interface FrontendConfig {
  realtimeWsPath: string;
  defaultRouteResults: number;
  maxRouteCandidates: number;
  defaultWorld: string;
  currencyName: string;
  worldTiles: Record<string, WorldTileConfig>;
  mapStyle: {
    lineWidth: number;
    highlightWidth: number;
    dimOpacity: number;
    lineOpacity: number;
    stationRadius: number;
    stationStrokeWidth: number;
    stationTextSize: number;
    stationMergePixelDistance: number;
    trainIconSize: number;
  };
  trainIcons: {
    express: string;
    normal: string;
  };
  defaultSystemLogo: string;
  avatarUrlTemplate: string;
  defaultPricePerKm: number;
  testAuthEnabled: boolean;
  testAuthUUIDs?: string[];
}

export interface RideHistoryItem {
  id: number;
  trainId: string;
  trainType: string;
  express: boolean;
  startedAt: number;
  endedAt: number;
  distance: number;
  startStation: string;
  endStation: string;
  paidFare?: number;
  nodeIds: string[];
}

export interface RideHistoryResponse {
  items: RideHistoryItem[];
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
}

/** 列车实时遥测（§4.8）。 */
export interface Train {
  trainId: string;
  world: string;
  head: { x: number; y: number; z: number; yaw: number };
  speedKph: number;
  cartCount: number;
  passengers: string[];
  express: boolean;
  lineId?: string;
  destination?: string;
  routeNodeIds?: string[];
  [key: string]: unknown; // 可扩展字段
}

/** 实时 WS 消息信封。 */
export interface WsEnvelope<T = unknown> {
  type: string;
  id?: string;
  ts: number;
  data: T;
}
