/**
 * 全局状态（Zustand）。集中管理：数据加载、世界切换、选点与候选路线、列车、登录、图层可见性。
 */
import { create } from 'zustand';
import { api, ApiError } from '../api/client';
import { RouteGraph } from '../routing/graph';
import { routeClient, RouteSearchTimeoutError } from '../routing/routeClient';

// 路线查询请求序号：仅应用最新一次查询结果，避免快速改动起终点时旧结果覆盖新结果。
let searchSeq = 0;
// Toast 自增 id：即使连续弹出相同内容，id 变化也能让提示组件重新计时。
let toastSeq = 0;
import { getConfig, setRuntimeConfig } from '../config';
import type {
  FeatureCollection,
  Line,
  LineStringProps,
  Meta,
  Player,
  PointProps,
  RailwaySystem,
  RideHistoryItem,
  RideHistoryResponse,
  RoutePath,
  Train,
} from '../types';

/** 侧栏模式：车站信息 / 路线查询 / 列车信息 / 实时列车列表 / 乘车历史 / 空。 */
export type SidebarMode = 'idle' | 'station' | 'line' | 'route' | 'train' | 'trains' | 'history';

/** 列车高亮后的镜头意图：center=居中到列车位置（列表点击）；route=框选整条路线（地图点击）。 */
export type TrainFocusMode = 'center' | 'route';

/** 顶部临时提示（如登录成功 / 失败）。id 用于触发重复提示的重新计时。 */
export interface Toast {
  id: number;
  kind: 'success' | 'error';
  message: string;
}

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
  /** lineId → 所属世界（由 geojson LineString 反推），用于按世界过滤线路列表。 */
  lineWorlds: Map<string, string>;

  // --- 图层可见性（lineId → 是否显示） ---
  hiddenLines: Set<string>;

  // --- 侧栏 / 选择 ---
  sidebar: SidebarMode;
  selectedStation: string | null; // 点击车站面板显示的站名
  selectedLineId: string | null; // 点击线路面板显示的线路 id
  /** 需要高亮的线路 id（点击地图线路 / 线路面板 / 车站面板线路名时设置）。 */
  highlightLineId: string | null;
  startStation: string | null;
  endStation: string | null;
  nextPick: Endpoint; // 下次点击地图车站设为起点还是终点
  candidates: RoutePath[];
  selectedRouteIndex: number | null;
  searching: boolean; // 路线查询进行中（显示查询中动画）
  searchError: string | null; // 查询失败/超时提示（null 表示无错误）

  // --- 列车 ---
  trains: Map<string, Train>;
  selectedTrainId: string | null;
  /** 选中列车后的镜头意图，供地图决定居中还是框选路线。 */
  trainFocusMode: TrainFocusMode;

  // --- 登录 ---
  player: Player | null;
  rideHistory: RideHistoryResponse | null;
  historyLoading: boolean;
  selectedHistoryId: number | null;

  // --- 顶部提示 ---
  toast: Toast | null;

  // --- actions ---
  init: () => Promise<void>;
  setWorld: (world: string) => void;
  toggleLine: (lineId: string) => void;
  setSystemVisible: (systemId: string, visible: boolean) => void;

  clickStation: (name: string) => void;
  clickLine: (lineId: string) => void;
  highlightLine: (lineId: string | null) => void;
  openRoutePanel: (presetEnd?: string) => void;
  setEndpoint: (role: Endpoint, name: string | null) => void;
  swapEndpoints: () => void;
  computeRoutes: () => Promise<void>;
  selectRoute: (index: number | null) => void;
  closeSidebar: () => void;

  upsertTrains: (trains: Train[]) => void;
  removeTrains: (ids: string[]) => void;
  selectTrain: (id: string | null) => void;
  openTrainList: () => void;
  focusTrain: (id: string) => void;

  refreshPlayer: () => Promise<void>;
  logout: () => Promise<void>;
  loadRideHistory: (page?: number) => Promise<void>;
  openRideHistory: () => Promise<void>;
  selectHistory: (item: RideHistoryItem) => void;

  showToast: (kind: Toast['kind'], message: string) => void;
  dismissToast: () => void;
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
  lineWorlds: new Map(),

  hiddenLines: new Set(),

  sidebar: 'idle',
  selectedStation: null,
  selectedLineId: null,
  highlightLineId: null,
  startStation: null,
  endStation: null,
  nextPick: 'start',
  candidates: [],
  selectedRouteIndex: null,
  searching: false,
  searchError: null,

  trains: new Map(),
  selectedTrainId: null,
  trainFocusMode: 'route',

  player: null,
  rideHistory: null,
  historyLoading: false,
  selectedHistoryId: null,

  toast: null,

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
      routeClient.init(geojson); // 后台线程用同一份数据建图，供寻路使用
      const systemMap = new Map(systems.map((s) => [s.id, s]));
      const reverseSet = new Set<string>();
      for (const line of lines) {
        for (const st of line.reverseStations ?? []) {
          reverseSet.add(`${line.id}|${st}`);
        }
      }
      const worlds = extractWorlds(geojson);
      const lineWorlds = extractLineWorlds(geojson);
      set({
        meta,
        geojson,
        graph,
        lines,
        systems,
        systemMap,
        reverseSet,
        worlds,
        lineWorlds,
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
    set({
      sidebar: 'station',
      selectedStation: name,
      selectedLineId: null,
      highlightLineId: null,
      selectedTrainId: null,
    });
  },

  clickLine(lineId) {
    // 路线查询打开时禁用线路点击，避免用户选点时误点到线路。
    if (get().sidebar === 'route') return;
    // 点击地图线路：打开线路详情并高亮该线路（任务 1）。
    set({
      sidebar: 'line',
      selectedLineId: lineId,
      highlightLineId: lineId,
      selectedStation: null,
      selectedTrainId: null,
    });
  },

  highlightLine(lineId) {
    set({ highlightLineId: lineId });
  },

  openRoutePanel(presetEnd) {
    set({
      sidebar: 'route',
      endStation: presetEnd ?? get().endStation,
      nextPick: 'start',
      candidates: [],
      selectedRouteIndex: null,
      searching: false,
      searchError: null,
      selectedLineId: null,
      highlightLineId: null,
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

  swapEndpoints() {
    const { startStation, endStation } = get();
    set({ startStation: endStation, endStation: startStation });
    if (endStation && startStation) {
      get().computeRoutes();
    }
  },

  async computeRoutes() {
    const { graph, startStation, endStation, systems, reverseSet } = get();
    if (!graph || !startStation || !endStation) {
      set({ candidates: [], selectedRouteIndex: null, searching: false, searchError: null });
      return;
    }
    const cfg = getConfig();
    const seq = ++searchSeq; // 本次查询序号，用于丢弃过期结果
    set({ searching: true, searchError: null, candidates: [], selectedRouteIndex: null });
    try {
      // 后台 Worker 寻路，主线程界面不卡死；超时由 routeClient 终止并 reject。
      const candidates = await routeClient.query(
        { startStation, endStation, systems, reverseKeys: [...reverseSet], cfg },
        cfg.routeSearchTimeoutMs,
      );
      if (seq !== searchSeq) return; // 已有更新的查询，丢弃本次结果
      set({ candidates, selectedRouteIndex: candidates.length > 0 ? 0 : null, searching: false, searchError: null });
    } catch (e) {
      if (seq !== searchSeq) return; // 过期查询的错误也一并忽略
      const searchError =
        e instanceof RouteSearchTimeoutError
          ? `路线查询超时（超过 ${(cfg.routeSearchTimeoutMs / 1000).toFixed(0)} 秒），请稍后重试或更换起终点`
          : '路线查询失败，请重试';
      set({ candidates: [], selectedRouteIndex: null, searching: false, searchError });
    }
  },

  selectRoute(index) {
    set({ selectedRouteIndex: index });
  },

  closeSidebar() {
    set({
      sidebar: 'idle',
      selectedStation: null,
      selectedLineId: null,
      highlightLineId: null,
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
    const removedSelected = ids.includes(get().selectedTrainId ?? '');
    if (removedSelected && get().sidebar === 'train') {
      // 正在跟踪的列车被销毁：整体关闭列车信息面板（含折叠按钮），而非留下空面板。
      get().closeSidebar();
      set({ trains });
    } else {
      set({ trains, selectedTrainId: removedSelected ? null : get().selectedTrainId });
    }
  },

  selectTrain(id) {
    // 地图上点击列车：框选整条路线（沿用既有镜头行为）。清除线路高亮，避免与列车路线高亮叠加。
    set({
      selectedTrainId: id,
      trainFocusMode: 'route',
      sidebar: id ? 'train' : get().sidebar,
      selectedLineId: null,
      highlightLineId: null,
    });
  },

  openTrainList() {
    set({
      sidebar: 'trains',
      selectedTrainId: null,
      candidates: [],
      selectedRouteIndex: null,
      searching: false,
      searchError: null,
      selectedLineId: null,
      highlightLineId: null,
    });
  },

  focusTrain(id) {
    const train = get().trains.get(id);
    if (!train) return;
    // 列车列表点击：若不在当前世界则切到对应世界，并要求地图居中到列车位置。
    set({
      currentWorld: train.world,
      selectedTrainId: id,
      trainFocusMode: 'center',
      sidebar: 'train',
      selectedLineId: null,
      highlightLineId: null,
    });
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
    set({ sidebar: 'history', selectedTrainId: null, candidates: [], selectedRouteIndex: null, selectedHistoryId: null, searching: false, searchError: null, selectedLineId: null, highlightLineId: null });
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

  showToast(kind, message) {
    set({ toast: { id: ++toastSeq, kind, message } });
  },

  dismissToast() {
    set({ toast: null });
  },
}));

/** 从 geojson 提取所有出现过的世界名（按首次出现顺序，首个即默认世界兜底）。 */
function extractWorlds(fc: FeatureCollection): string[] {
  const set = new Set<string>();
  for (const f of fc.features) {
    const w = (f.properties as PointProps)?.world;
    if (w) set.add(w);
  }
  return [...set];
}

/** 从 geojson LineString 反推 lineId → 所属世界，用于按世界过滤线路列表。 */
function extractLineWorlds(fc: FeatureCollection): Map<string, string> {
  const map = new Map<string, string>();
  for (const f of fc.features) {
    if (f.geometry?.type !== 'LineString') continue;
    const p = f.properties as LineStringProps;
    if (p?.lineId && p.world && !map.has(p.lineId)) map.set(p.lineId, p.world);
  }
  return map;
}
