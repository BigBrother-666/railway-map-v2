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
  /** 入向面门控：到达起点道岔的允许到达面集合（与插件 GeoLink.enterFacesFrom 同源）。空表示不门控。 */
  enterFrom?: string[];
  /** 沿本段到达终点节点的到达面 key（与插件 GeoLink.enterFaceTo 同源）。 */
  enterTo?: string;
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
        enterFrom: l.enterFrom,
        enterTo: l.enterTo,
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

  /** 站名级缩合距离矩阵的缓存（惰性构建，随图实例失效）。 */
  private stationDistCache: Map<string, Map<string, number>> | null = null;

  /**
   * 站名级「直达可达」缩合距离矩阵：起点站名 → 终点站名 → 一趟快速车的最短距离（km）。复刻插件
   * GeoRouteGraph.stationDirectDistances。把物理节点（含大量道岔/多站台）缩合为站名节点，只保留
   * 「站名 A 能否一趟直达站名 B、最短多少」，供 findTransferJourneys 快速枚举全部换乘站。
   * <p>
   * 口径为下界估计：对每个站名各站台跑普通 Dijkstra（边权 = length，忽略 enterFace / 折返 / 正线绕行约束），
   * 只用于筛选候选，最终每段仍由 findByStation 权威实体化施加全部约束，故下界乐观性不影响结果正确性。
   */
  stationDirectDistances(): Map<string, Map<string, number>> {
    if (this.stationDistCache) return this.stationDistCache;
    const matrix = new Map<string, Map<string, number>>();
    for (const [startName, platforms] of this.stationIndex) {
      const row = new Map<string, number>();
      for (const platformId of platforms) this.accumulateShortestToStations(platformId, row);
      row.delete(startName); // 起点到自身不算直达候选
      matrix.set(startName, row);
    }
    this.stationDistCache = matrix;
    return matrix;
  }

  /**
   * 从单一起点节点做普通 Dijkstra（边权 = 段长，米），把到达各站名的最短距离（km）并入 out（取更小值）。
   */
  private accumulateShortestToStations(startNodeId: string, out: Map<string, number>): void {
    const dist = new Map<string, number>();
    dist.set(startNodeId, 0);
    // 简易二叉堆用数组 + 线性 pop 足够（节点数中等）；与后端 PriorityQueue 语义一致（过期条目跳过）。
    const queue: { id: string; d: number }[] = [{ id: startNodeId, d: 0 }];
    while (queue.length > 0) {
      let mi = 0;
      for (let i = 1; i < queue.length; i++) if (queue[i].d < queue[mi].d) mi = i;
      const cur = queue.splice(mi, 1)[0];
      const known = dist.get(cur.id);
      if (known !== undefined && cur.d > known) continue;
      const node = this.nodes.get(cur.id);
      if (node && node.type === 'station' && node.name && cur.d > 0) {
        const km = cur.d / 1000;
        const prev = out.get(node.name);
        if (prev === undefined || km < prev) out.set(node.name, km);
      }
      for (const link of this.links(cur.id)) {
        const nd = cur.d + link.distance;
        const old = dist.get(link.to);
        if (old === undefined || nd < old) {
          dist.set(link.to, nd);
          queue.push({ id: link.to, d: nd });
        }
      }
    }
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
