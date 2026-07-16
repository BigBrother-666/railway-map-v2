/**
 * MapLibre 地图控制器：管理线路 / 车站 / 列车 / 高亮路线四类图层。
 * 按当前世界过滤数据，按 lineId 控制可见性，游戏坐标渲染前转经纬度。
 * 渲染与寻路分离——这里用完整几何渲染，寻路图在 routing/ 内单独构建。
 */
import maplibregl from 'maplibre-gl';
import type { FeatureCollection, LineStringProps, PointProps, Train } from '../types';
import { gameToLngLat, gameLineToLngLat } from './coords';
import { getConfig, CONTACT_SYSTEM_ID } from '../config';

const SRC_LINES = 'lines';
const SRC_STATIONS = 'stations';
const SRC_TRAINS = 'trains';
const SRC_ENDPOINTS = 'route-endpoints';
const SRC_WORLD_TILES = 'world-tiles';
const LAYER_WORLD_TILES = 'world-tiles-layer';
/** 底图背景色（与 style 的 bg 图层一致）；淡化时把颜色混向它而非降透明度，避免重叠处 alpha 累加变实。 */
const BG_COLOR: [number, number, number] = [0x0f, 0x11, 0x15];

export class MapController {
  private map: maplibregl.Map;
  private fc: FeatureCollection | null = null;
  private world = '';
  private hidden: Set<string> = new Set();
  private highlightEdges: Set<string> = new Set();
  /** 高亮是否生效（用于车站淡化：生效时非高亮车站按 dimOpacity 变半透明）。 */
  private highlightActive = false;
  /** 当前高亮边集合覆盖到的车站节点 id（这些车站保持不透明，其余淡化）。 */
  private highlightStationIds: Set<string> = new Set();
  private onStationClick?: (name: string) => void;
  private onTrainClick?: (id: string) => void;
  private onLineClick?: (lineId: string) => void;
  /** 左侧被侧边栏遮挡的像素宽度，框选时作为左侧内边距，避免内容落在侧边栏下方。 */
  private leftInset = 0;

  constructor(container: HTMLElement) {
    this.map = new maplibregl.Map({
      container,
      fadeDuration: 80,
      refreshExpiredTiles: false,
      // 关闭世界副本：不显示重复地图
      renderWorldCopies: false,
      // 直通式约束：中心原样返回（禁用「把世界锁进视口」约束，可任意拖出瓦片/世界范围，
      // 且不显示重复地图）；缩放仍夹取到当前 min/max，保证 minZoom/maxZoom 生效。
      transformConstrain: (lngLat, zoom) => ({
        center: lngLat,
        zoom: this.clampZoom(zoom ?? 0),
      }),
      style: {
        version: 8,
        // 纯色底图（无外部瓦片依赖；如配置了世界瓦片，可在此扩展 raster source）
        sources: {},
        layers: [
          { id: 'bg', type: 'background', paint: { 'background-color': '#0f1115' } },
        ],
        glyphs: 'https://demotiles.maplibre.org/font/{fontstack}/{range}.pbf',
      },
      center: [0, 0],
      zoom: 14,
      maxZoom: 20,
      minZoom: 0,
      attributionControl: false,
    });
    this.map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'bottom-right');
  }

  onReady(cb: () => void) {
    this.map.on('load', cb);
  }

  setHandlers(
    onStationClick: (name: string) => void,
    onTrainClick: (id: string) => void,
    onLineClick: (lineId: string) => void,
  ) {
    this.onStationClick = onStationClick;
    this.onTrainClick = onTrainClick;
    this.onLineClick = onLineClick;
  }

  /** 设置左侧侧边栏遮挡宽度（0 表示未打开），供框选时留出左侧内边距。 */
  setLeftInset(px: number) {
    this.leftInset = Math.max(0, px);
  }

  destroy() {
    this.map.remove();
  }

  /** 容器尺寸变化后重算画布（侧栏开合导致地图区收缩/扩张时调用）。 */
  resize() {
    this.map.resize();
  }

  /** 加载（或切换世界后重载）数据并建图层。 */
  setData(fc: FeatureCollection, world: string) {
    this.fc = fc;
    this.world = world;
    this.applyWorldZoomLimits();
    this.ensureSources();
    this.refreshWorldData();
    this.fitToData();
  }

  /** 切换世界：只重渲染该世界子集。 */
  setWorld(world: string) {
    if (world === this.world) return;
    this.world = world;
    this.applyWorldZoomLimits();
    this.refreshWorldData();
    this.fitToData();
  }

  /**
   * 按 lineId 隐藏集合更新线路可见性，同时联动车站：一个车站只要还有可见线路连接就显示，
   * 否则隐藏（问题 3）。用 filter 而非删除图层。
   */
  setHiddenLines(hidden: Set<string>) {
    this.hidden = hidden;
    if (!this.map.getLayer('lines-layer')) return;
    this.applyLineFilters();

    // 车站显隐与同名近距合并都通过重建 stations source 完成。
    this.updateStationSource();
  }

  /**
   * 当前世界 + 未隐藏的可见性过滤（base 与 highlight 共用的基础部分）。
   * 联络线（lineId===contact）不受 hiddenLines 直接控制，而是按两端节点是否可见自动显隐：
   * 仅当该联络线段的 edge id 落在「可见联络线」集合内才渲染。
   */
  private visibilityFilter(): maplibregl.FilterSpecification {
    const visibleContact = [...this.computeVisibleContactEdges()];
    return [
      'all',
      ['==', ['get', 'world'], this.world],
      [
        'case',
        ['==', ['get', 'lineId'], CONTACT_SYSTEM_ID],
        ['in', ['get', 'id'], ['literal', visibleContact]],
        ['!', ['in', ['get', 'lineId'], ['literal', [...this.hidden]]]],
      ],
    ] as maplibregl.FilterSpecification;
  }

  /**
   * 计算当前世界下应显示的联络线段 edge id 集合。
   *
   * 联络线两端节点通常是道岔：有的道岔本身挂在普通线路上（PointProps.lineIds，排除 contact 自身），
   * 有的则是「纯中转道岔」——不属于任何普通线路，只作为两条联络线之间的连接点（联络线链式相连）。
   *
   * 规则：把「经纯中转道岔互连」的联络线段并为一组，组的边界是挂有普通线路的<b>终端节点</b>；
   * 当且仅当该组所有终端节点都仍有可见普通线路时，整组联络线显示——任一终端的普通线路全被隐藏，则整组隐藏。
   * 这样链中间的纯中转道岔（无普通线路）不会被误判为「不可见」而拖垮整条链。
   */
  private computeVisibleContactEdges(): Set<string> {
    const result = new Set<string>();
    if (!this.fc) return result;

    // 节点 id → 关联的非联络线 lineId 列表（当前世界）
    const nodeNormalLines = new Map<string, string[]>();
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'Point') continue;
      const p = f.properties as PointProps;
      if (p.world !== this.world || !p.id) continue;
      nodeNormalLines.set(p.id, (p.lineIds ?? []).filter((id) => id !== CONTACT_SYSTEM_ID));
    }
    const hasNormalLine = (id: string) => (nodeNormalLines.get(id)?.length ?? 0) > 0;
    // 终端节点可见：至少一条普通线路未被隐藏（与车站显隐口径一致）。
    const terminalVisible = (id: string) =>
      (nodeNormalLines.get(id) ?? []).some((lineId) => !this.hidden.has(lineId));

    // 收集当前世界所有联络线段，并建立「节点 → 关联联络线段索引」邻接。
    const contactEdges: { id: string; from: string; to: string }[] = [];
    const nodeContactEdges = new Map<string, number[]>();
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'LineString') continue;
      const l = f.properties as LineStringProps;
      if (l.lineId !== CONTACT_SYSTEM_ID || l.world !== this.world || !l.id) continue;
      const idx = contactEdges.length;
      contactEdges.push({ id: l.id, from: l.from, to: l.to });
      for (const n of [l.from, l.to]) {
        const arr = nodeContactEdges.get(n) ?? [];
        arr.push(idx);
        nodeContactEdges.set(n, arr);
      }
    }

    // 逐组 BFS：只经纯中转道岔（无普通线路）跨到相邻联络线段，终端节点作为边界不跨越。
    const visited = new Array(contactEdges.length).fill(false);
    for (let start = 0; start < contactEdges.length; start++) {
      if (visited[start]) continue;
      const group: number[] = [];
      const terminals = new Set<string>();
      const stack = [start];
      visited[start] = true;
      while (stack.length) {
        const ei = stack.pop() as number;
        group.push(ei);
        const e = contactEdges[ei];
        for (const n of [e.from, e.to]) {
          if (hasNormalLine(n)) {
            terminals.add(n); // 终端：不再向外扩散
            continue;
          }
          for (const adj of nodeContactEdges.get(n) ?? []) {
            if (!visited[adj]) {
              visited[adj] = true;
              stack.push(adj);
            }
          }
        }
      }
      // 组可见：存在终端且所有终端仍有可见普通线路。
      const groupVisible = terminals.size > 0 && [...terminals].every((n) => terminalVisible(n));
      if (groupVisible) for (const ei of group) result.add(contactEdges[ei].id);
    }
    return result;
  }

  /**
   * 同步两个线路图层的 filter：
   * - base(lines-layer)：仅可见性过滤。
   * - highlight(lines-highlight)：可见性 AND 命中高亮边集合；无高亮时匹配空集（不渲染，故默认正常粗度）。
   */
  private applyLineFilters() {
    this.map.setFilter('lines-layer', this.visibilityFilter());
    const edges = [...this.highlightEdges];
    const highlightFilter: maplibregl.FilterSpecification = edges.length
      ? (['all', this.visibilityFilter(), ['in', ['get', 'id'], ['literal', edges]]] as maplibregl.FilterSpecification)
      : (['==', ['get', 'id'], '__none__'] as maplibregl.FilterSpecification);
    this.map.setFilter('lines-highlight', highlightFilter);
  }

  /** 当前世界下，仍有至少一条可见线路连接的车站节点 id。 */
  private computeVisibleStationIds(): Set<string> {
    const result = new Set<string>();
    if (!this.fc) return result;
    // 该世界所有车站节点 id
    const stationIds = new Set<string>();
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'Point') continue;
      const p = f.properties as PointProps;
      if (p.type === 'station' && p.world === this.world && p.id) stationIds.add(p.id);
    }
    // 遍历可见边，把端点是车站的标记为可见
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'LineString') continue;
      const l = f.properties as LineStringProps;
      if (l.world !== this.world || this.hidden.has(l.lineId)) continue;
      if (stationIds.has(l.from)) result.add(l.from);
      if (stationIds.has(l.to)) result.add(l.to);
    }
    return result;
  }

  /**
   * 高亮一条路线（节点 id 序列）：在同一 lines source 上用 lines-highlight 图层按边 id 过滤，
   * 加粗并用<b>线路本身颜色</b>（问题 2）；其余线路淡化。null 时清除高亮、恢复正常（问题 1）。
   */
  highlightRoute(legs: string[][] | null) {
    if (!this.fc || !this.map.getLayer('lines-highlight')) return;
    const validLegs = (legs ?? []).filter((leg) => leg.length >= 2);
    if (validLegs.length === 0) {
      // 清除高亮：highlight 边集合清空（filter 变空集，不渲染），base 恢复本色与正常透明度，车站恢复不透明
      this.highlightEdges = new Set();
      this.highlightActive = false;
      this.highlightStationIds = new Set();
      this.applyLineFilters();
      this.setBaseLineDimmed(false);
      this.setRouteEndpoints(null);
      this.updateStationSource();
      return;
    }
    // 收集各段各区间的 edge id（geojson LineString 的 id 属性）与途经车站节点 id。
    const edgeIds = new Set<string>();
    const stationIds = new Set<string>();
    const edgeByEndpoints = this.edgeIdIndex();
    for (const leg of validLegs) {
      for (const nodeId of leg) stationIds.add(nodeId);
      for (let i = 0; i < leg.length - 1; i++) {
        const id = edgeByEndpoints.get(`${leg[i]}__${leg[i + 1]}`);
        if (id) edgeIds.add(id);
      }
    }
    this.highlightEdges = edgeIds;
    this.highlightActive = true;
    this.highlightStationIds = stationIds;
    this.applyLineFilters();
    // 其它线路淡化（改用不透明淡化色，避免半透明叠加变实），突出高亮
    this.setBaseLineDimmed(true);
    // 非高亮车站淡化（任务 2）
    this.updateStationSource();
    // 端点：整程起点（首段首节点）、终点（末段末节点）
    const first = validLegs[0];
    const last = validLegs[validLegs.length - 1];
    this.setRouteEndpoints(first[0], last[last.length - 1]);
  }

  /**
   * 高亮整条线路（任务 1 / 3）：按 lineId 收集该线路的所有轨道段与途经车站节点，
   * 复用 highlightRoute 的高亮通道（加粗、其余淡化、非高亮车站半透明）。lineId 为 null 时清除高亮。
   */
  highlightLine(lineId: string | null) {
    if (!this.fc || !this.map.getLayer('lines-highlight')) return;
    if (!lineId) {
      this.highlightRoute(null);
      return;
    }
    const edgeIds = new Set<string>();
    const stationIds = new Set<string>();
    const stationNodeIds = new Set<string>();
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'Point') continue;
      const p = f.properties as PointProps;
      if (p.type === 'station' && p.world === this.world && p.id) stationNodeIds.add(p.id);
    }
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'LineString') continue;
      const l = f.properties as LineStringProps;
      if (l.lineId !== lineId || l.world !== this.world || !l.id) continue;
      edgeIds.add(l.id);
      if (stationNodeIds.has(l.from)) stationIds.add(l.from);
      if (stationNodeIds.has(l.to)) stationIds.add(l.to);
    }
    if (edgeIds.size === 0) {
      this.highlightRoute(null);
      return;
    }
    this.highlightEdges = edgeIds;
    this.highlightActive = true;
    this.highlightStationIds = stationIds;
    this.applyLineFilters();
    this.setBaseLineDimmed(true);
    this.updateStationSource();
    this.setRouteEndpoints(null);
  }

  /**
   * 切换基础线路层的淡化态：
   * - 淡化：用预计算的不透明 dimColor（本色混向背景），line-opacity=1 —— 重叠处不透明覆盖，不再叠加变实。
   * - 恢复：用线路本色 + config 的 lineOpacity。
   */
  private setBaseLineDimmed(dimmed: boolean) {
    if (!this.map.getLayer('lines-layer')) return;
    if (dimmed) {
      this.map.setPaintProperty('lines-layer', 'line-color', ['coalesce', ['get', 'dimColor'], '#3a3f4b']);
      this.map.setPaintProperty('lines-layer', 'line-opacity', 1);
    } else {
      this.map.setPaintProperty('lines-layer', 'line-color', ['coalesce', ['get', 'color'], '#888888']);
      this.map.setPaintProperty('lines-layer', 'line-opacity', getConfig().mapStyle.lineOpacity);
    }
  }

  private setRouteEndpoints(startId: string | null, endId?: string) {
    if (!this.fc || !this.map.getSource(SRC_ENDPOINTS)) return;
    const src = this.map.getSource(SRC_ENDPOINTS) as maplibregl.GeoJSONSource;
    if (!startId || !endId) {
      src.setData({ type: 'FeatureCollection', features: [] });
      return;
    }
    const features: GeoJSON.Feature<GeoJSON.Point>[] = [];
    const pushByNodeId = (id: string, role: string) => {
      const f = this.fc!.features.find((feat) => {
        const p = feat.properties as PointProps;
        return feat.geometry?.type === 'Point' && p.id === id;
      });
      if (!f || f.geometry?.type !== 'Point') return;
      const c = (f.geometry as GeoJSON.Point).coordinates;
      features.push({
        type: 'Feature',
        properties: { role },
        geometry: { type: 'Point', coordinates: gameToLngLat(c[0], c[1], this.world) },
      });
    };
    pushByNodeId(startId, 'start');
    pushByNodeId(endId, 'end');
    // 联程票换乘站不额外打点，保持默认车站样式
    src.setData({ type: 'FeatureCollection', features });
  }

  /** from__to → edge id 索引（按当前世界，懒构建一次性使用）。 */
  private edgeIdIndex(): Map<string, string> {
    const idx = new Map<string, string>();
    if (!this.fc) return idx;
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'LineString') continue;
      const l = f.properties as LineStringProps;
      if (l.from && l.to && l.id) idx.set(`${l.from}__${l.to}`, l.id);
    }
    return idx;
  }

  /** 更新列车图层。 */
  setTrains(trains: Train[]) {
    if (!this.map.getSource(SRC_TRAINS)) return;
    const feats = trains
      .filter((t) => t.world === this.world)
      .map((t) => ({
        type: 'Feature' as const,
        properties: { trainId: t.trainId, express: t.express, yaw: t.head.yaw },
        geometry: { type: 'Point' as const, coordinates: gameToLngLat(t.head.x, t.head.z, t.world) },
      }));
    (this.map.getSource(SRC_TRAINS) as maplibregl.GeoJSONSource).setData({
      type: 'FeatureCollection',
      features: feats,
    });
  }

  // --- 内部 ---

  private ensureSources() {
    if (this.map.getSource(SRC_LINES)) return;
    const empty: GeoJSON.FeatureCollection = { type: 'FeatureCollection', features: [] };
    // GeoJSON source 会在内部按瓦片切分：默认 tolerance(0.375) 的简化会在高缩放抹掉细短线段，
    // 默认 maxzoom(18) 之上靠 overzoom 拉伸、落在瓦片 buffer 外的短段会被裁掉——
    // 表现为「放大到一定级别少量线段消失、缩小又出现」。故对线路关闭简化并抬高 maxzoom + 加大 buffer。
    this.map.addSource(SRC_LINES, { type: 'geojson', data: empty, tolerance: 0, maxzoom: 24, buffer: 512 });
    this.map.addSource(SRC_STATIONS, { type: 'geojson', data: empty, maxzoom: 24, buffer: 512 });
    this.map.addSource(SRC_TRAINS, { type: 'geojson', data: empty });
    this.map.addSource(SRC_ENDPOINTS, { type: 'geojson', data: empty });

    // 线路（按 layer 排序叠层；color 来自属性；固定像素线宽，不随 zoom / 坐标缩放变）
    this.map.addLayer({
      id: 'lines-layer',
      type: 'line',
      source: SRC_LINES,
      layout: { 'line-cap': 'round', 'line-join': 'round', 'line-sort-key': ['get', 'layer'] },
      paint: {
        'line-color': ['coalesce', ['get', 'color'], '#888888'],
        'line-width': getConfig().mapStyle.lineWidth,
        'line-opacity': getConfig().mapStyle.lineOpacity,
      },
    });
    // 高亮路线：复用 lines source，按 edge id 过滤，用线路本身颜色、更粗（问题 2）
    this.map.addLayer({
      id: 'lines-highlight',
      type: 'line',
      source: SRC_LINES,
      layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: {
        'line-color': ['coalesce', ['get', 'color'], '#888888'],
        'line-width': getConfig().mapStyle.highlightWidth,
      },
      filter: ['==', ['get', 'id'], '__none__'],
    });
    // 车站圆点（固定像素半径）；高亮线路时非高亮车站按 dim 标记淡化（透明度取 config.dimOpacity）。
    const dim = getConfig().mapStyle.dimOpacity;
    const stationOpacity: maplibregl.DataDrivenPropertyValueSpecification<number> = [
      'case',
      ['get', 'dim'],
      dim,
      1,
    ];
    this.map.addLayer({
      id: 'stations-layer',
      type: 'circle',
      source: SRC_STATIONS,
      paint: {
        'circle-radius': getConfig().mapStyle.stationRadius,
        'circle-color': '#ffffff',
        'circle-stroke-color': '#222222',
        'circle-stroke-width': getConfig().mapStyle.stationStrokeWidth,
        'circle-opacity': stationOpacity,
        'circle-stroke-opacity': stationOpacity,
      },
    });
    // 车站名
    this.map.addLayer({
      id: 'stations-label',
      type: 'symbol',
      source: SRC_STATIONS,
      layout: {
        'text-field': ['get', 'name'],
        'text-size': getConfig().mapStyle.stationTextSize,
        'text-offset': [0, 1.2],
        'text-anchor': 'top',
        // 碰撞优先级：sort-key 小者优先占位。非淡化（高亮线路上）站名给 0，淡化站名给 1，
        // 使同名的高亮 label 抢占、透明 label 让位，避免高亮站名被透明副本盖成透明。
        'symbol-sort-key': ['case', ['get', 'dim'], 1, 0],
      },
      paint: {
        'text-color': '#eaeaea',
        'text-halo-color': '#000000',
        'text-halo-width': 1.2,
        'text-opacity': stationOpacity,
      },
    });
    this.map.addLayer({
      id: 'route-endpoints-layer',
      type: 'circle',
      source: SRC_ENDPOINTS,
      paint: {
        'circle-radius': getConfig().mapStyle.stationRadius + 2,
        'circle-color': ['case', ['==', ['get', 'role'], 'start'], '#2fbf71', '#ef4444'],
        'circle-stroke-color': '#ffffff',
        'circle-stroke-width': 2,
      },
    });
    // 列车：可配置图标（直达 / 普通各一），按 express 选用
    this.registerTrainIcons();
    this.map.addLayer({
      id: 'trains-layer',
      type: 'symbol',
      source: SRC_TRAINS,
      layout: {
        'icon-image': ['case', ['get', 'express'], 'train-express', 'train-normal'],
        'icon-size': getConfig().mapStyle.trainIconSize,
        'icon-allow-overlap': true,
        'icon-rotate': ['get', 'yaw'],
        'icon-rotation-alignment': 'map',
      },
    });

    this.bindEvents();
  }

  /** 注册列车图标（SVG/PNG → ImageBitmap → map.addImage）。 */
  private registerTrainIcons() {
    const load = (name: string, url: string) => {
      const img = new Image();
      img.onload = () => {
        if (!this.map.hasImage(name)) this.map.addImage(name, img);
      };
      img.src = url;
    };
    load('train-express', getConfig().trainIcons.express);
    load('train-normal', getConfig().trainIcons.normal);
  }

  private bindEvents() {
    this.map.on('click', 'stations-layer', (e) => {
      const name = e.features?.[0]?.properties?.name as string | undefined;
      if (name) this.onStationClick?.(name);
    });
    this.map.on('click', 'trains-layer', (e) => {
      const id = e.features?.[0]?.properties?.trainId as string | undefined;
      if (id) this.onTrainClick?.(id);
    });
    // 线路点击：弹出线路详情（任务 2）。车站圆点 / 列车图标叠在线路之上，
    // 若同一点还命中车站或列车则让位给它们，避免点击车站时误触线路。
    this.map.on('click', 'lines-layer', (e) => {
      const hitOverlay = this.map.queryRenderedFeatures(e.point, {
        layers: ['stations-layer', 'trains-layer'],
      });
      if (hitOverlay.length > 0) return;
      const lineId = e.features?.[0]?.properties?.lineId as string | undefined;
      if (lineId && lineId !== CONTACT_SYSTEM_ID) this.onLineClick?.(lineId);
    });
    for (const layer of ['stations-layer', 'trains-layer', 'lines-layer']) {
      this.map.on('mouseenter', layer, () => (this.map.getCanvas().style.cursor = 'pointer'));
      this.map.on('mouseleave', layer, () => (this.map.getCanvas().style.cursor = ''));
    }
    this.map.on('zoomend', () => this.updateStationSource());
  }

  private refreshWorldData() {
    if (!this.fc) return;
    this.updateWorldTiles();
    const lineFeats = this.fc.features
      .filter((f) => f.geometry?.type === 'LineString' && (f.properties as LineStringProps).world === this.world)
      // 预计算淡化色：把线路本色按 dimOpacity 混向背景色。淡化时基础层用此不透明色，
      // 重叠处是不透明像素直接覆盖、不再累加 alpha，避免「半透明叠加变实」（改善项）。
      .map((f) => {
        const p = f.properties as LineStringProps;
        return { ...f, properties: { ...p, dimColor: mixTowardBg(p.color, getConfig().mapStyle.dimOpacity) } };
      });
    (this.map.getSource(SRC_LINES) as maplibregl.GeoJSONSource)?.setData(this.toLngLatFC(lineFeats));
    this.updateStationSource();
    this.setHiddenLines(this.hidden);
  }

  private updateWorldTiles() {
    const tile = getConfig().worldTiles[this.world];
    if (this.map.getLayer(LAYER_WORLD_TILES)) {
      this.map.removeLayer(LAYER_WORLD_TILES);
    }
    if (this.map.getSource(SRC_WORLD_TILES)) {
      this.map.removeSource(SRC_WORLD_TILES);
    }
    if (!tile?.tileUrl) return;

    const source: maplibregl.RasterSourceSpecification = {
      type: 'raster',
      tiles: [tile.tileUrl],
      tileSize: tile.tileSize ?? 256,
    };
    if (tile.minNativeZoom !== undefined) source.minzoom = tile.minNativeZoom;
    if (tile.maxNativeZoom !== undefined) source.maxzoom = tile.maxNativeZoom;
    if (tile.scheme) source.scheme = tile.scheme;

    this.map.addSource(SRC_WORLD_TILES, source);
    const layer: maplibregl.RasterLayerSpecification = {
      id: LAYER_WORLD_TILES,
      type: 'raster',
      source: SRC_WORLD_TILES,
      paint: { 'raster-opacity': tile.opacity ?? 1 },
    };
    if (tile.minZoom !== undefined) layer.minzoom = tile.minZoom;
    this.map.addLayer(
      layer,
      this.map.getLayer('lines-layer') ? 'lines-layer' : undefined,
    );
  }

  /** 给车站要素写入 dim 标记（不改动其它属性）。 */
  private setDim(
    f: FeatureCollection['features'][number],
    dim: boolean,
  ): FeatureCollection['features'][number] {
    const p = f.properties as PointProps;
    return { ...f, properties: { ...p, dim } } as FeatureCollection['features'][number];
  }

  private updateStationSource() {
    if (!this.fc || !this.map.getSource(SRC_STATIONS)) return;
    const source = this.map.getSource(SRC_STATIONS) as maplibregl.GeoJSONSource;
    const visibleStationIds = this.computeVisibleStationIds();
    const stationFeats = this.fc.features.filter((f) => {
      if (f.geometry?.type !== 'Point') return false;
      const p = f.properties as PointProps;
      return p.type === 'station' && p.world === this.world && visibleStationIds.has(p.id);
    });

    if (!this.highlightActive) {
      const merged = this.mergeNearbyStations(stationFeats).map((f) => this.setDim(f, false));
      source.setData(this.toLngLatFC(merged));
      return;
    }

    // 高亮生效：只有落在高亮线路上的<b>节点</b>高亮（不透明），按真实位置单独渲染，避免被合并到簇质心而偏离线路。
    // 其余车站（含同名的其它站台节点）正常合并并淡化。同名的高亮点与淡化点位置相近时，靠 symbol-sort-key
    // 让不透明的高亮 label 优先占位、透明 label 让位（见 stations-label 的 symbol-sort-key），故站名不会显示为透明。
    const onRoute = stationFeats.filter((f) => this.highlightStationIds.has((f.properties as PointProps).id));
    const rest = stationFeats.filter((f) => !this.highlightStationIds.has((f.properties as PointProps).id));
    const merged = [
      ...onRoute.map((f) => this.setDim(f, false)),
      ...this.mergeNearbyStations(rest).map((f) => this.setDim(f, true)),
    ];
    source.setData(this.toLngLatFC(merged));
  }

  private mergeNearbyStations(feats: FeatureCollection['features']): FeatureCollection['features'] {
    const threshold = getConfig().mapStyle.stationMergePixelDistance;
    if (threshold <= 0 || feats.length < 2) return feats;

    type Cluster = {
      name: string;
      world: string;
      features: FeatureCollection['features'];
      x: number;
      y: number;
      z: number;
      px: number;
      py: number;
    };

    const clusters: Cluster[] = [];
    for (const f of feats) {
      if (f.geometry?.type !== 'Point') continue;
      const p = f.properties as PointProps;
      const c = (f.geometry as GeoJSON.Point).coordinates;
      const screen = this.map.project(gameToLngLat(c[0], c[1], p.world));
      const name = p.name ?? p.id;
      const cluster = clusters.find((candidate) => {
        if (candidate.name !== name || candidate.world !== p.world) return false;
        return Math.hypot(candidate.px - screen.x, candidate.py - screen.y) <= threshold;
      });
      if (!cluster) {
        clusters.push({
          name,
          world: p.world,
          features: [f],
          x: c[0],
          y: c[2] ?? 0,
          z: c[1],
          px: screen.x,
          py: screen.y,
        });
        continue;
      }
      cluster.features.push(f);
      const n = cluster.features.length;
      cluster.x += (c[0] - cluster.x) / n;
      cluster.y += ((c[2] ?? 0) - cluster.y) / n;
      cluster.z += (c[1] - cluster.z) / n;
      cluster.px += (screen.x - cluster.px) / n;
      cluster.py += (screen.y - cluster.py) / n;
    }

    return clusters.map((cluster) => {
      if (cluster.features.length === 1) return cluster.features[0];
      const first = cluster.features[0].properties as PointProps;
      const ids = cluster.features
        .map((feature) => (feature.properties as PointProps).id)
        .filter(Boolean)
        .sort();
      return {
        type: 'Feature',
        properties: {
          ...first,
          id: `station-cluster:${cluster.world}:${cluster.name}:${ids.join(',')}`,
          stationIds: ids,
          mergedCount: ids.length,
        },
        geometry: { type: 'Point', coordinates: [cluster.x, cluster.z, cluster.y] },
      } as FeatureCollection['features'][number];
    });
  }

  /** 把游戏坐标要素转成经纬度要素（保留 properties）。 */
  private toLngLatFC(feats: FeatureCollection['features']): GeoJSON.FeatureCollection {
    return {
      type: 'FeatureCollection',
      features: feats.map((f) => {
        if (f.geometry?.type === 'LineString') {
          return {
            type: 'Feature',
            properties: f.properties,
            geometry: { type: 'LineString', coordinates: gameLineToLngLat((f.geometry as GeoJSON.LineString).coordinates as number[][], this.world) },
          };
        }
        const c = (f.geometry as GeoJSON.Point).coordinates;
        return {
          type: 'Feature',
          properties: f.properties,
          geometry: { type: 'Point', coordinates: gameToLngLat(c[0], c[1], this.world) },
        };
      }),
    };
  }

  private fitToData() {
    if (!this.fc) return;
    const tile = getConfig().worldTiles[this.world];
    const bounds = new maplibregl.LngLatBounds();
    let has = false;
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'Point') continue;
      if ((f.properties as PointProps).world !== this.world) continue;
      const c = (f.geometry as GeoJSON.Point).coordinates;
      bounds.extend(gameToLngLat(c[0], c[1], this.world));
      has = true;
    }
    // 配置了 center（游戏坐标）则用作初始镜头中心，否则回退数据范围中心。
    const cfgCenter = tile?.center;
    const center =
      cfgCenter && cfgCenter.length === 2
        ? gameToLngLat(cfgCenter[0], cfgCenter[1], this.world)
        : has
          ? bounds.getCenter()
          : null;
    const padding = { top: 60, bottom: 60, right: 60, left: 60 + this.leftInset };
    if (tile?.zoom !== undefined) {
      // 配置了初始缩放：用配置的 zoom 级别定位到指定/数据中心（不自动框选整片范围）。
      if (center) this.map.jumpTo({ center, zoom: tile.zoom, padding });
      else this.map.setZoom(tile.zoom);
    } else if (has) {
      // 未配置 zoom：按数据范围自动框选缩放，maxZoom 兜底避免压成一个点。
      this.map.fitBounds(bounds, { padding, maxZoom: tile?.maxZoom ?? 18, duration: 0 });
      // 若额外配置了 center，框选后把镜头平移到该中心（缩放沿用框选结果）。
      if (cfgCenter && center) this.map.setCenter(center);
    }
  }

  /**
   * 平滑缩放并移动到给定路线（各段节点 id）的范围，使整条线路完整入镜。
   * 用于点击路线卡片 / 乘车历史时的镜头联动。
   */
  fitToNodes(legs: string[][]) {
    if (!this.fc) return;
    const byId = new Map<string, GeoJSON.Point>();
    for (const feat of this.fc.features) {
      const p = feat.properties as PointProps;
      if (feat.geometry?.type === 'Point' && p.id) byId.set(p.id, feat.geometry as GeoJSON.Point);
    }
    const bounds = new maplibregl.LngLatBounds();
    let has = false;
    for (const leg of legs) {
      for (const id of leg) {
        const geom = byId.get(id);
        if (!geom) continue;
        bounds.extend(gameToLngLat(geom.coordinates[0], geom.coordinates[1], this.world));
        has = true;
      }
    }
    if (!has) return;
    const tile = getConfig().worldTiles[this.world];
    this.map.fitBounds(bounds, {
      padding: { top: 80, bottom: 80, right: 80, left: 80 + this.leftInset },
      maxZoom: tile?.maxZoom ?? 18,
      duration: 600,
    });
  }

  /** 平滑缩放并移动到某条线路的完整范围（用于点击线路 / 线路面板 / 车站面板线路名）。 */
  fitToLine(lineId: string) {
    if (!this.fc) return;
    const bounds = new maplibregl.LngLatBounds();
    let has = false;
    for (const f of this.fc.features) {
      if (f.geometry?.type !== 'LineString') continue;
      const l = f.properties as LineStringProps;
      if (l.lineId !== lineId || l.world !== this.world) continue;
      for (const c of (f.geometry as GeoJSON.LineString).coordinates) {
        bounds.extend(gameToLngLat(c[0], c[1], this.world));
        has = true;
      }
    }
    if (!has) return;
    const tile = getConfig().worldTiles[this.world];
    this.map.fitBounds(bounds, {
      padding: { top: 80, bottom: 80, right: 80, left: 80 + this.leftInset },
      maxZoom: tile?.maxZoom ?? 18,
      duration: 600,
    });
  }

  /**
   * 平滑移动镜头，把给定游戏坐标居中显示（用于点击实时列车卡片跳转到列车位置）。
   * 保持当前缩放，仅平移；左侧留出侧边栏遮挡宽度，使列车落在可见区域中央。
   */
  centerOnGame(x: number, z: number) {
    const center = gameToLngLat(x, z, this.world);
    this.map.easeTo({
      center,
      padding: { top: 0, bottom: 0, right: 0, left: this.leftInset },
      duration: 600,
    });
  }

  /** 把缩放夹取到当前世界的 min/max 范围（直通约束下手动补上这层限制）。 */
  private clampZoom(zoom: number): number {
    // 构造期回调可能早于 map 赋值；用配置兜底。
    const tile = getConfig().worldTiles[this.world];
    const min = this.map?.getMinZoom?.() ?? tile?.minZoom ?? 0;
    const max = this.map?.getMaxZoom?.() ?? tile?.maxZoom ?? 20;
    return Math.min(max, Math.max(min, zoom));
  }

  private applyWorldZoomLimits() {
    const tile = getConfig().worldTiles[this.world];
    this.map.setMinZoom(tile?.minZoom ?? 0);
    this.map.setMaxZoom(tile?.maxZoom ?? 20);
  }
}

/**
 * 把颜色 hex 按 alpha 混向底图背景色，返回不透明 #rrggbb。
 * out = color*alpha + bg*(1-alpha)。用不透明淡化色替代半透明，重叠处不再叠加变实。
 */
function mixTowardBg(hex: string | undefined, alpha: number): string {
  const rgb = parseHex(hex);
  if (!rgb) return '#888888';
  const a = Math.min(1, Math.max(0, alpha));
  const mix = (c: number, bg: number) => Math.round(c * a + bg * (1 - a));
  const r = mix(rgb[0], BG_COLOR[0]);
  const g = mix(rgb[1], BG_COLOR[1]);
  const b = mix(rgb[2], BG_COLOR[2]);
  return `#${[r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('')}`;
}

/** 解析 #rgb / #rrggbb 为 [r,g,b]；非法返回 null。 */
function parseHex(hex: string | undefined): [number, number, number] | null {
  if (!hex) return null;
  let h = hex.replace('#', '').trim();
  if (h.length === 3) h = h.split('').map((c) => c + c).join('');
  if (h.length !== 6 || /[^0-9a-fA-F]/.test(h)) return null;
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}
