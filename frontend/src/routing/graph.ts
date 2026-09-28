/**
 * 由 geojson 反向构建的节点/边图。寻路计算已移至后端 internal/routing（POST /api/v1/route/query），
 * 这份图只服务非寻路场景：车站面板（所属线路/系统）、线路详情（正线绕行判断以计算真实长度）、
 * 乘车历史（缺失距离时的兜底估算）。建图只需节点 + 边，不保留每段几何顶点（渲染与此分离）。
 */
import type { FeatureCollection, LineStringProps, PointProps } from '../types';

/** 图中的一条有向边。 */
export interface GraphLink {
  from: string;
  to: string;
  lineId: string;
  distance: number; // 米
  systemId?: string;
}

/** 图中的一个节点。 */
export interface GraphNode {
  id: string;
  type: 'station' | 'switch';
  name?: string;
  lineIds: Set<string>;
  systemIds: Set<string>;
  x: number; // 游戏 x（经度方向）
  z: number; // 游戏 z（纬度方向）
  y: number;
}

/** 寻路图：节点表 + 出边邻接表 + 车站名索引。 */
export class RouteGraph {
  readonly nodes = new Map<string, GraphNode>();
  readonly adjacency = new Map<string, GraphLink[]>();
  /** 车站名 → 该名下所有 station 节点 id（一个车站可能多站台）。 */
  readonly stationIndex = new Map<string, string[]>();

  static fromFeatureCollection(fc: FeatureCollection): RouteGraph {
    const g = new RouteGraph();
    // 先加节点
    for (const f of fc.features) {
      if (f.geometry?.type !== 'Point') continue;
      const p = f.properties as PointProps;
      if (!p?.id) continue;
      const coords = (f.geometry as GeoJSON.Point).coordinates;
      g.addNode({
        id: p.id,
        type: p.type,
        name: p.name,
        lineIds: new Set(p.lineIds ?? []),
        systemIds: new Set(p.railwaySystemIds ?? []),
        x: coords[0],
        z: coords[1],
        y: coords[2] ?? 0,
      });
    }
    // 再加边（节点已就位，便于把 lineId 累积到两端）
    for (const f of fc.features) {
      if (f.geometry?.type !== 'LineString') continue;
      const l = f.properties as LineStringProps;
      if (!l?.from || !l?.to) continue;
      g.addLink({
        from: l.from,
        to: l.to,
        lineId: l.lineId,
        distance: l.length,
        systemId: l.railwaySystemId,
      });
    }
    return g;
  }

  private addNode(n: GraphNode) {
    const existing = this.nodes.get(n.id);
    if (existing) {
      n.lineIds.forEach((id) => existing.lineIds.add(id));
      n.systemIds.forEach((id) => existing.systemIds.add(id));
      return;
    }
    this.nodes.set(n.id, n);
    if (n.type === 'station' && n.name) {
      const arr = this.stationIndex.get(n.name) ?? [];
      arr.push(n.id);
      this.stationIndex.set(n.name, arr);
    }
  }

  private addLink(l: GraphLink) {
    const arr = this.adjacency.get(l.from) ?? [];
    arr.push(l);
    this.adjacency.set(l.from, arr);
    this.nodes.get(l.from)?.lineIds.add(l.lineId);
    this.nodes.get(l.to)?.lineIds.add(l.lineId);
  }

  links(nodeId: string): GraphLink[] {
    return this.adjacency.get(nodeId) ?? [];
  }

  stationNodes(name: string): string[] {
    return this.stationIndex.get(name) ?? [];
  }

  /**
   * 从节点序列的一端向内遍历，返回第一个车站节点的站名。
   * 用于从路线 nodeIds 推断始发 / 终到车站——首尾节点可能是道岔，需向内找到最近的车站。
   * @param nodeIds 路线节点序列
   * @param fromEnd false 从头（始发）向后找；true 从尾（终到）向前找
   */
  firstStationName(nodeIds: string[], fromEnd = false): string | null {
    const n = nodeIds.length;
    for (let k = 0; k < n; k++) {
      const id = nodeIds[fromEnd ? n - 1 - k : k];
      const node = this.nodes.get(id);
      if (node?.type === 'station' && node.name) return node.name;
    }
    return null;
  }

  allStationNames(): string[] {
    return [...this.stationIndex.keys()];
  }

  private isEnterSwitcher(nodeId: string, lineId: string | undefined): boolean {
    const node = this.nodes.get(nodeId);
    if (!node || node.type === 'station' || !lineId) return false;
    const links = this.links(nodeId);
    if (links.length === 0) return false;
    return links.every((link) => link.lineId === lineId);
  }

  platformNameOfMainlineSwitch(nodeId: string, lineId: string | undefined): string | null {
    if (!this.isEnterSwitcher(nodeId, lineId)) return null;
    const links = this.links(nodeId);
    let toSwitch = false;
    let platformName: string | null = null;
    for (const link of links) {
      const to = this.nodes.get(link.to);
      if (!to) continue;
      if (to.type === 'station') {
        platformName = to.name ?? null;
      } else {
        toSwitch = true;
      }
    }
    return toSwitch ? platformName : null;
  }

  isMainlineBypassSegment(from: string | undefined, to: string | undefined, lineId: string | undefined): boolean {
    if (!from || !to || !lineId) return false;
    const toNode = this.nodes.get(to);
    return toNode?.type !== 'station' && this.platformNameOfMainlineSwitch(from, lineId) != null;
  }
}
