/**
 * 由 geojson 反向构建寻路图，并提供 Dijkstra / Yen K-最短路（本地寻路，见 FRONTEND_PROMPT.md §4）。
 * 与后端 GeoRouteEngine 同源：边权 = length，逐段 lineId 不去重；建图只需节点 + 边，
 * 不保留每段几何顶点（渲染与寻路分离，§5）。
 */
import type { FeatureCollection, LineStringProps, PointProps } from '../types';

/** 图中的一条有向边。 */
export interface GraphLink {
  from: string;
  to: string;
  lineId: string;
  distance: number; // 米
  systemId?: string;
  departDir?: string;
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
        departDir: l.departDir,
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

  allStationNames(): string[] {
    return [...this.stationIndex.keys()];
  }

  platformNameOfMainlineSwitch(nodeId: string): string | null {
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
}
