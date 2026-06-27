/**
 * 全局状态（Zustand）。集中管理：数据加载、世界切换、选点与候选路线、列车、登录、图层可见性。
 */
import { create } from 'zustand';
import { api, ApiError } from '../api/client';
import { RouteGraph } from '../routing/graph';
import { findByStation } from '../routing/pathfind';
import { estimateFare, fareDetails } from '../routing/fare';
import { getConfig, setRuntimeConfig } from '../config';
import type {
  FeatureCollection,
  Line,
  Meta,
  Player,
  PointProps,
  RailwaySystem,
  RideHistoryItem,
  RideHistoryResponse,
  RoutePath,
  Train,
} from '../types';

/** 侧栏模式：车站信息 / 路线查询 / 列车信息 / 空。 */
export type SidebarMode = 'idle' | 'station' | 'route' | 'train' | 'history';

/** 选点角色。 */
type Endpoint = 'start' | 'end';

interface AppState {
  // --- 数据 ---
  meta: Meta | null;
  geojson: FeatureCollection | null;
  graph: RouteGraph | null;
  lines: Line[];
  systems: RailwaySystem[];
  systemMap: Map<string, RailwaySystem>;
  /** 折返站集合，key = `${lineId}|${stationName}`，寻路时跳过。 */
  reverseSet: Set<string>;
  loading: boolean;
  loadError: string | null;

  // --- 世界 ---
  worlds: string[];
  currentWorld: string;

  // --- 图层可见性（lineId → 是否显示） ---
  hiddenLines: Set<string>;

  // --- 侧栏 / 选择 ---
  sidebar: SidebarMode;
  selectedStation: string | null; // 点击车站面板显示的站名
  startStation: string | null;
  endStation: string | null;
  nextPick: Endpoint; // 下次点击地图车站设为起点还是终点
  candidates: RoutePath[];
  selectedRouteIndex: number | null;

  // --- 列车 ---
  trains: Map<string, Train>;
  selectedTrainId: string | null;

  // --- 登录 ---
  player: Player | null;
  rideHistory: RideHistoryResponse | null;
  historyLoading: boolean;
  selectedHistoryId: number | null;

  // --- actions ---
  init: () => Promise<void>;
  setWorld: (world: string) => void;
  toggleLine: (lineId: string) => void;
  setSystemVisible: (systemId: string, visible: boolean) => void;

  clickStation: (name: string) => void;
  openRoutePanel: (presetEnd?: string) => void;
  setEndpoint: (role: Endpoint, name: string | null) => void;
  computeRoutes: () => void;
  selectRoute: (index: number | null) => void;
  closeSidebar: () => void;

  upsertTrains: (trains: Train[]) => void;
  removeTrains: (ids: string[]) => void;
  selectTrain: (id: string | null) => void;

  refreshPlayer: () => Promise<void>;
  logout: () => Promise<void>;
  loadRideHistory: (page?: number) => Promise<void>;
  openRideHistory: () => Promise<void>;
  selectHistory: (item: RideHistoryItem) => void;
}

export const useStore = create<AppState>((set, get) => ({
  meta: null,
  geojson: null,
  graph: null,
  lines: [],
  systems: [],
  systemMap: new Map(),
  reverseSet: new Set(),
  loading: false,
  loadError: null,

  worlds: [],
  currentWorld: getConfig().defaultWorld,

  hiddenLines: new Set(),

  sidebar: 'idle',
  selectedStation: null,
  startStation: null,
  endStation: null,
  nextPick: 'start',
  candidates: [],
  selectedRouteIndex: null,

  trains: new Map(),
  selectedTrainId: null,

  player: null,
  rideHistory: null,
  historyLoading: false,
  selectedHistoryId: null,

  async init() {
    set({ loading: true, loadError: null });
    try {
      const frontendConfig = await api.config();
      setRuntimeConfig(frontendConfig);
      const [meta, geojson, lines, systems] = await Promise.all([
        api.meta(),
        api.geojson(),
        api.lines(),
        api.systems(),
      ]);
      const graph = RouteGraph.fromFeatureCollection(geojson);
      const systemMap = new Map(systems.map((s) => [s.id, s]));
      const reverseSet = new Set<string>();
      for (const line of lines) {
        for (const st of line.reverseStations ?? []) {
          reverseSet.add(`${line.id}|${st}`);
        }
      }
      const worlds = extractWorlds(geojson);
      set({
        meta,
        geojson,
        graph,
        lines,
        systems,
        systemMap,
        reverseSet,
        worlds,
        currentWorld: worlds.includes(get().currentWorld)
          ? get().currentWorld
          : worlds[0] ?? getConfig().defaultWorld,
        loading: false,
      });
    } catch (e) {
      set({ loading: false, loadError: e instanceof Error ? e.message : String(e) });
    }
    // 登录态（失败忽略，按未登录处理）
    get().refreshPlayer();
  },

  setWorld(world) {
    set({ currentWorld: world, selectedTrainId: null });
  },

  toggleLine(lineId) {
    const hidden = new Set(get().hiddenLines);
    if (hidden.has(lineId)) hidden.delete(lineId);
    else hidden.add(lineId);
    set({ hiddenLines: hidden });
  },

  setSystemVisible(systemId, visible) {
    const hidden = new Set(get().hiddenLines);
    for (const line of get().lines) {
      if (line.systemId === systemId) {
        if (visible) hidden.delete(line.id);
        else hidden.add(line.id);
      }
    }
    set({ hiddenLines: hidden });
  },

  clickStation(name) {
    const state = get();
    if (state.sidebar === 'route') {
      // 路线查询中：点击车站设为「下一个待填」端点
      state.setEndpoint(state.nextPick, name);
      return;
    }
    set({ sidebar: 'station', selectedStation: name, selectedTrainId: null });
  },

  openRoutePanel(presetEnd) {
    set({
      sidebar: 'route',
      endStation: presetEnd ?? get().endStation,
      nextPick: 'start',
      candidates: [],
      selectedRouteIndex: null,
    });
  },

  setEndpoint(role, name) {
    if (role === 'start') {
      set({ startStation: name, nextPick: 'end' });
    } else {
      set({ endStation: name, nextPick: 'start' });
    }
    const { startStation, endStation } = get();
    if (startStation && endStation) {
      get().computeRoutes();
    }
  },

  computeRoutes() {
    const { graph, startStation, endStation, systemMap, reverseSet } = get();
    if (!graph || !startStation || !endStation) {
      set({ candidates: [], selectedRouteIndex: null });
      return;
    }
    const globalRate = getConfig().defaultPricePerKm;
    const isReverse = (lineId: string, station: string) => reverseSet.has(`${lineId}|${station}`);
    const paths = findByStation(graph, startStation, endStation, getConfig().defaultRouteResults, isReverse).map((p) => {
      const details = fareDetails(p, systemMap, globalRate);
      return {
        ...p,
        fareDetails: details,
        estimatedFare: estimateFare({ ...p, fareDetails: details }, systemMap, globalRate),
      };
    });
    set({ candidates: paths, selectedRouteIndex: paths.length > 0 ? 0 : null });
  },

  selectRoute(index) {
    set({ selectedRouteIndex: index });
  },

  closeSidebar() {
    set({
      sidebar: 'idle',
      selectedStation: null,
      selectedTrainId: null,
      // 关闭面板时清空查询与高亮，地图恢复正常（问题 1）
      startStation: null,
      endStation: null,
      candidates: [],
      selectedRouteIndex: null,
      nextPick: 'start',
      selectedHistoryId: null,
    });
  },

  upsertTrains(list) {
    const trains = new Map(get().trains);
    for (const t of list) {
      if (t.trainId) trains.set(t.trainId, t);
    }
    set({ trains });
  },

  removeTrains(ids) {
    const trains = new Map(get().trains);
    for (const id of ids) trains.delete(id);
    const selectedTrainId = ids.includes(get().selectedTrainId ?? '') ? null : get().selectedTrainId;
    set({ trains, selectedTrainId });
  },

  selectTrain(id) {
    set({ selectedTrainId: id, sidebar: id ? 'train' : get().sidebar });
  },

  async refreshPlayer() {
    try {
      const player = await api.me();
      set({ player });
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        set({ player: null });
      }
    }
  },

  async logout() {
    await api.logout();
    set({ player: null, rideHistory: null, selectedHistoryId: null });
  },

  async loadRideHistory(page = 1) {
    set({ historyLoading: true });
    try {
      const rideHistory = await api.rideHistory(page, 10);
      set({ rideHistory, historyLoading: false });
    } catch {
      set({ historyLoading: false });
    }
  },

  async openRideHistory() {
    set({ sidebar: 'history', selectedTrainId: null, candidates: [], selectedRouteIndex: null, selectedHistoryId: null });
    await get().loadRideHistory(1);
  },

  selectHistory(item) {
    set({
      selectedHistoryId: item.id,
      candidates: [{
        stations: [item.startStation, item.endStation],
        stationSteps: [{ stationName: item.startStation }, { stationName: item.endStation }],
        nodeIds: item.nodeIds,
        lineIdSequence: [],
        distance: item.distance,
        segments: [],
        estimatedFare: item.paidFare ?? 0,
      }],
      selectedRouteIndex: 0,
    });
  },
}));

/** 从 geojson 提取所有出现过的世界名。 */
function extractWorlds(fc: FeatureCollection): string[] {
  const set = new Set<string>();
  for (const f of fc.features) {
    const w = (f.properties as PointProps)?.world;
    if (w) set.add(w);
  }
  return [...set];
}
