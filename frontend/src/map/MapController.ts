/**
 * MapLibre 地图控制器：管理线路 / 车站 / 列车 / 高亮路线四类图层。
 * 按当前世界过滤数据，按 lineId 控制可见性，游戏坐标渲染前转经纬度。
 * 渲染与寻路分离——这里用完整几何渲染，寻路图在 routing/ 内单独构建。
 */
import maplibregl from 'maplibre-gl';
import type { FeatureCollection, LineStringProps, PointProps, Train } from '../types';
import { gameToLngLat, gameLineToLngLat } from './coords';
import { getConfig } from '../config';

const SRC_LINES = 'lines';
const SRC_STATIONS = 'stations';
const SRC_TRAINS = 'trains';
const SRC_ENDPOINTS = 'route-endpoints';
const SRC_WORLD_TILES = 'world-tiles';
const LAYER_WORLD_TILES = 'world-tiles-layer';

export class MapController {
  private map: maplibregl.Map;
  private fc: FeatureCollection | null = null;
  private world = '';
  private hidden: Set<string> = new Set();
  private highlightEdges: Set<string> = new Set();
  private onStationClick?: (name: string) => void;
  private onTrainClick?: (id: string) => void;

  constructor(container: HTMLElement) {
    this.map = new maplibregl.Map({
      container,
      fadeDuration: 80,
      refreshExpiredTiles: false,
      renderWorldCopies: false,
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

  setHandlers(onStationClick: (name: string) => void, onTrainClick: (id: string) => void) {
    this.onStationClick = onStationClick;
    this.onTrainClick = onTrainClick;
  }

  destroy() {
    this.map.remove();
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

  /** 当前世界 + 未隐藏的可见性过滤（base 与 highlight 共用的基础部分）。 */
  private visibilityFilter(): maplibregl.FilterSpecification {
    return [
      'all',
      ['==', ['get', 'world'], this.world],
      ['!', ['in', ['get', 'lineId'], ['literal', [...this.hidden]]]],
    ];
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
  highlightRoute(legs: string[][] | null, transferStations: string[] = []) {
    if (!this.fc || !this.map.getLayer('lines-highlight')) return;
    const validLegs = (legs ?? []).filter((leg) => leg.length >= 2);
    if (validLegs.length === 0) {
      // 清除高亮：highlight 边集合清空（filter 变空集，不渲染），base 恢复正常透明度
      this.highlightEdges = new Set();
      this.applyLineFilters();
      this.map.setPaintProperty('lines-layer', 'line-opacity', getConfig().mapStyle.lineOpacity);
      this.setRouteEndpoints(null);
      return;
    }
    // 收集各段各区间的 edge id（geojson LineString 的 id 属性）
    const edgeIds = new Set<string>();
    const edgeByEndpoints = this.edgeIdIndex();
    for (const leg of validLegs) {
      for (let i = 0; i < leg.length - 1; i++) {
        const id = edgeByEndpoints.get(`${leg[i]}__${leg[i + 1]}`);
        if (id) edgeIds.add(id);
      }
    }
    this.highlightEdges = edgeIds;
    this.applyLineFilters();
    // 其它线路淡化，突出高亮
    this.map.setPaintProperty('lines-layer', 'line-opacity', getConfig().mapStyle.dimOpacity);
    // 端点：整程起点（首段首节点）、终点（末段末节点），换乘站单独打点
    const first = validLegs[0];
    const last = validLegs[validLegs.length - 1];
    this.setRouteEndpoints(first[0], last[last.length - 1], transferStations);
  }

  private setRouteEndpoints(startId: string | null, endId?: string, transferStations: string[] = []) {
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
    // 换乘站：按站名取任一站台节点坐标打点（联程票专用）
    for (const name of transferStations) {
      const f = this.fc.features.find((feat) => {
        const p = feat.properties as PointProps;
        return feat.geometry?.type === 'Point' && p.type === 'station' && p.name === name;
      });
      if (!f || f.geometry?.type !== 'Point') continue;
      const c = (f.geometry as GeoJSON.Point).coordinates;
      features.push({
        type: 'Feature',
        properties: { role: 'transfer' },
        geometry: { type: 'Point', coordinates: gameToLngLat(c[0], c[1], this.world) },
      });
    }
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
    this.map.addSource(SRC_LINES, { type: 'geojson', data: empty });
    this.map.addSource(SRC_STATIONS, { type: 'geojson', data: empty });
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
    // 车站圆点（固定像素半径）
    this.map.addLayer({
      id: 'stations-layer',
      type: 'circle',
      source: SRC_STATIONS,
      paint: {
        'circle-radius': getConfig().mapStyle.stationRadius,
        'circle-color': '#ffffff',
        'circle-stroke-color': '#222222',
        'circle-stroke-width': getConfig().mapStyle.stationStrokeWidth,
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
      },
      paint: { 'text-color': '#eaeaea', 'text-halo-color': '#000000', 'text-halo-width': 1.2 },
    });
    this.map.addLayer({
      id: 'route-endpoints-layer',
      type: 'circle',
      source: SRC_ENDPOINTS,
      paint: {
        'circle-radius': getConfig().mapStyle.stationRadius + 2,
        'circle-color': [
          'case',
          ['==', ['get', 'role'], 'start'],
          '#2fbf71',
          ['==', ['get', 'role'], 'transfer'],
          '#f5a623',
          '#ef4444',
        ],
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
    for (const layer of ['stations-layer', 'trains-layer']) {
      this.map.on('mouseenter', layer, () => (this.map.getCanvas().style.cursor = 'pointer'));
      this.map.on('mouseleave', layer, () => (this.map.getCanvas().style.cursor = ''));
    }
    this.map.on('zoomend', () => this.updateStationSource());
  }

  private refreshWorldData() {
    if (!this.fc) return;
    this.updateWorldTiles();
    const lineFeats = this.fc.features.filter(
      (f) => f.geometry?.type === 'LineString' && (f.properties as LineStringProps).world === this.world,
    );
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

  private updateStationSource() {
    if (!this.fc || !this.map.getSource(SRC_STATIONS)) return;
    const visibleStationIds = this.computeVisibleStationIds();
    const stationFeats = this.fc.features.filter((f) => {
      if (f.geometry?.type !== 'Point') return false;
      const p = f.properties as PointProps;
      return p.type === 'station' && p.world === this.world && visibleStationIds.has(p.id);
    });
    (this.map.getSource(SRC_STATIONS) as maplibregl.GeoJSONSource).setData(
      this.toLngLatFC(this.mergeNearbyStations(stationFeats)),
    );
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
    if (has) {
      // 用数据自身范围框选缩放；上限用世界 maxZoom，避免被「默认 zoom」压成一个点。
      this.map.fitBounds(bounds, { padding: 60, maxZoom: tile?.maxZoom ?? 18, duration: 0 });
    } else if (tile?.zoom !== undefined) {
      // 无数据可框时，才回退到配置的默认缩放级别。
      this.map.setZoom(tile.zoom);
    }
  }

  private applyWorldZoomLimits() {
    const tile = getConfig().worldTiles[this.world];
    this.map.setMinZoom(tile?.minZoom ?? 0);
    this.map.setMaxZoom(tile?.maxZoom ?? 20);
  }
}
