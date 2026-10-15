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
  /** 正线 / 停靠线标记（geojson track）。 */
  track?: 'main' | 'siding';
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
        track: l.track,
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

  /**
   * 判断节点序列的每对相邻节点在当前图中是否都有直连边。
   * 乘车历史用：geojson 更新（中间增删节点 / 删线路）后旧序列可能对不上。
   * @param nodeIds 路线节点序列
   * @returns 全部相邻节点对都有边时为 true
   */
  hasAllEdges(nodeIds: string[]): boolean {
    for (let i = 0; i < nodeIds.length - 1; i++) {
      if (!this.links(nodeIds[i]).some((l) => l.to === nodeIds[i + 1])) return false;
    }
    return true;
  }

  /**
   * 修补节点序列：相邻节点间没有直连边时，用当前图上的最短路（按距离）补齐中间节点；
   * 找不到路径（节点已被删除等）则保持原样。用于无坐标快照的旧乘车历史的兜底显示。
   * @param nodeIds 路线节点序列
   * @returns 修补后的节点序列
   */
  repairPath(nodeIds: string[]): string[] {
    if (nodeIds.length === 0) return [];
    const out = [nodeIds[0]];
    for (let i = 0; i < nodeIds.length - 1; i++) {
      const from = nodeIds[i];
      const to = nodeIds[i + 1];
      if (this.links(from).some((l) => l.to === to)) {
        out.push(to);
        continue;
      }
      const path = this.shortestPath(from, to);
      // path 含首尾；首节点已在 out 中
      if (path) out.push(...path.slice(1));
      else out.push(to);
    }
    return out;
  }

  /**
   * 两节点间按距离的最短路径（Dijkstra，线性取最小，节点规模下足够快）。
   * @returns 含首尾的节点序列；不可达或节点不存在时为 null
   */
  private shortestPath(from: string, to: string): string[] | null {
    if (!this.nodes.has(from) || !this.nodes.has(to)) return null;
    const dist = new Map<string, number>([[from, 0]]);
    const prev = new Map<string, string>();
    const open = new Set<string>([from]);
    const done = new Set<string>();
    while (open.size > 0) {
      let cur = '';
      let best = Infinity;
      for (const id of open) {
        const d = dist.get(id) ?? Infinity;
        if (d < best) {
          best = d;
          cur = id;
        }
      }
      open.delete(cur);
      if (cur === to) break;
      done.add(cur);
      for (const link of this.links(cur)) {
        if (done.has(link.to)) continue;
        const nd = best + (link.distance > 0 ? link.distance : 1);
        if (nd < (dist.get(link.to) ?? Infinity)) {
          dist.set(link.to, nd);
          prev.set(link.to, cur);
          open.add(link.to);
        }
      }
    }
    if (!dist.has(to)) return null;
    const path = [to];
    while (path[0] !== from) {
      const p = prev.get(path[0]);
      if (!p) return null;
      path.unshift(p);
    }
    return path;
  }

  allStationNames(): string[] {
    return [...this.stationIndex.keys()];
  }

  /**
   * 是否为正线段（geojson track=main，进站道岔跨站直通到出站道岔）。
   * 高亮 / 线路图只画停靠线，正线段跳过。
   */
  isMainlineBypassSegment(from: string | undefined, to: string | undefined, lineId: string | undefined): boolean {
    if (!from || !to || !lineId) return false;
    return this.links(from).some((l) => l.to === to && l.lineId === lineId && l.track === 'main');
  }

  /**
   * 正线段跨过的车站名（复刻后端 Graph.sidingStationFrom）：从进站道岔沿本线停靠线（track=siding）出边 BFS，
   * 返回遇到的第一个车站名；不是正线段或找不到时返回 null。
   */
  mainlineBypassStationName(from: string | undefined, to: string | undefined, lineId: string | undefined): string | null {
    if (!from || !lineId || !this.isMainlineBypassSegment(from, to, lineId)) return null;
    const queue = [from];
    const seen = new Set(queue);
    while (queue.length > 0) {
      const cur = queue.shift()!;
      for (const l of this.links(cur)) {
        if (l.track !== 'siding' || l.lineId !== lineId) continue;
        const node = this.nodes.get(l.to);
        if (!node) continue;
        if (node.type === 'station') {
          if (node.name) return node.name;
        } else if (!seen.has(node.id)) {
          seen.add(node.id);
          queue.push(node.id);
        }
      }
    }
    return null;
  }
}
