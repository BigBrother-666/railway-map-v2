/**
 * 本地寻路：从起点站名到终点站名求按距离升序的 K 条无环候选路线。
 * 复刻后端 GeoRouteEngine 的语义：枚举起点各站台 → 每站台求 K 条简单路径 → 汇总去重排序。
 */
import type { RoutePath, RouteSegment } from '../types';
import type { GraphLink, RouteGraph } from './graph';

interface Entry {
  nodeId: string;
  dist: number; // 累计米
  link: GraphLink | null; // 到达本节点所走的边
  prev: Entry | null;
}

/** (lineId, stationName) → 是否折返站。默认恒 false。 */
export type ReversePredicate = (lineId: string, stationName: string) => boolean;

/** 按起点站名 + 终点站名求候选路线（距离升序、按逐段出向去重）。 */
export function findByStation(
  graph: RouteGraph,
  startStation: string,
  endStation: string,
  maxResults = 10,
  isReverse: ReversePredicate = () => false,
): RoutePath[] {
  const all: RoutePath[] = [];
  for (const startId of graph.stationNodes(startStation)) {
    all.push(...kShortest(graph, startId, endStation, maxResults, isReverse));
  }
  const deduped = new Map<string, RoutePath>();
  for (const p of all) {
    const key = departDirectionKey(p);
    const old = deduped.get(key);
    if (!old || lineTransferCount(old.lineIdSequence) > lineTransferCount(p.lineIdSequence)) {
      deduped.set(key, p);
    }
  }
  const ret = [...deduped.values()].sort((a, b) => a.distance - b.distance);
  return maxResults > 0 ? ret.slice(0, maxResults) : ret;
}

function departDirectionKey(path: RoutePath): string {
  return (path.departDirectionSequence ?? []).map((dir) => dir ?? '').join('>');
}

function lineTransferCount(lineIdSequence: string[]): number {
  let count = 0;
  for (let i = 0; i < lineIdSequence.length - 1; i++) {
    if (lineIdSequence[i] !== lineIdSequence[i + 1]) count++;
  }
  return count;
}

/**
 * 从单一起点节点求 K 条无环最短路线，终点为任一名为 endStation 的 station 节点。
 * 优先队列按累计距离扩展，跳过已在当前路径前缀中的节点保证无环（允许回到起点闭合环线）。
 */
function kShortest(
  graph: RouteGraph,
  startId: string,
  endStation: string,
  k: number,
  isReverse: ReversePredicate,
): RoutePath[] {
  const results: RoutePath[] = [];
  if (!graph.nodes.has(startId) || k < 1) return results;

  const pq: Entry[] = [{ nodeId: startId, dist: 0, link: null, prev: null }];
  const MAX_POPS = 200_000;
  let pops = 0;

  while (pq.length > 0 && results.length < k && pops < MAX_POPS) {
    // 取最小 dist（线性扫描；候选规模小，足够）
    let mi = 0;
    for (let i = 1; i < pq.length; i++) if (pq[i].dist < pq[mi].dist) mi = i;
    const cur = pq.splice(mi, 1)[0];
    pops++;

    const curNode = graph.nodes.get(cur.nodeId);
    if (curNode?.type === 'station' && curNode.name === endStation && cur.link) {
      results.push(buildPath(graph, cur));
      continue;
    }
    for (const link of graph.links(cur.nodeId)) {
      const nextId = link.to;
      const nextNode = graph.nodes.get(nextId);
      if (!nextNode) continue;
      if (inPath(cur, nextId)) {
        const closesLoop = nextId === startId && nextNode.type === 'station' && nextNode.name === endStation;
        if (!closesLoop) continue;
      }
      // 与插件 GeoRouteEngine 一致：中途站的处理
      if (nextNode.type === 'station' && nextNode.name !== endStation) {
        // 存在正线绕行 → 放弃穿越该 station（避免直达车误进停靠线）
        if (hasMainlineBypass(graph, cur.nodeId, nextId)) continue;
        // 折返站 → 直达车不穿越（与插件 isReverseStation 跳过一致）
        if (nextNode.name && isReverse(link.lineId, nextNode.name)) continue;
      }
      pq.push({ nodeId: nextId, dist: cur.dist + link.distance, link, prev: cur });
    }
  }
  return results;
}

/**
 * 结构判定某处是否存在「正线绕行」——进站道岔 nodeId 与停靠线车站 stationId 连接了同一个出站道岔。
 * 复刻插件 GeoRouteEngine.hasMainlineBypass：用坐标比较出站道岔，兼容两线共线但节点 id 不同的情况。
 */
function hasMainlineBypass(graph: RouteGraph, nodeId: string, stationId: string): boolean {
  const stationLinks = graph.links(stationId);
  if (stationLinks.length === 0) return false;
  const stationOut = graph.nodes.get(stationLinks[0].to);
  if (!stationOut) return false;
  for (const link of graph.links(nodeId)) {
    const to = graph.nodes.get(link.to);
    if (to && coordEquals(to, stationOut)) return true;
  }
  return false;
}

function coordEquals(a: { x: number; y: number; z: number }, b: { x: number; y: number; z: number }): boolean {
  return a.x === b.x && a.y === b.y && a.z === b.z;
}

function inPath(entry: Entry, nodeId: string): boolean {
  for (let e: Entry | null = entry; e; e = e.prev) {
    if (e.nodeId === nodeId) return true;
  }
  return false;
}

/** 从回溯链构建 RoutePath（与后端结构一致，距离换算为 km）。 */
function buildPath(graph: RouteGraph, end: Entry): RoutePath {
  const nodeIds: string[] = [];
  const lineIdSequence: string[] = [];
  const departDirectionSequence: string[] = [];
  const segMeters: { lineId: string; meters: number; systemId: string }[] = [];

  for (let e: Entry | null = end; e && e.link; e = e.prev) {
    nodeIds.push(e.nodeId);
    lineIdSequence.push(e.link.lineId);
    departDirectionSequence.push(e.link.departDir ?? '');
    segMeters.push({
      lineId: e.link.lineId,
      meters: e.link.distance,
      systemId: e.link.systemId ?? '',
    });
  }
  // 加起点节点
  let startEntry: Entry = end;
  while (startEntry.prev) startEntry = startEntry.prev;
  nodeIds.push(startEntry.nodeId);

  nodeIds.reverse();
  lineIdSequence.reverse();
  departDirectionSequence.reverse();
  segMeters.reverse();

  const stations: string[] = [];
  const stationSteps: { stationName: string; departLineId?: string }[] = [];
  for (let i = 0; i < nodeIds.length; i++) {
    const id = nodeIds[i];
    const n = graph.nodes.get(id);
    if (n?.type === 'station' && n.name) {
      const lineId = i < lineIdSequence.length ? lineIdSequence[i] : lineIdSequence[i - 1];
      pushStationStep(stationSteps, n.name, lineId);
    } else if (n) {
      const stationName = graph.platformNameOfMainlineSwitch(id);
      const lineId = i < lineIdSequence.length ? lineIdSequence[i] : undefined;
      if (stationName && lineId) pushStationStep(stationSteps, stationName, lineId);
    }
  }
  stations.push(...stationSteps.map((s) => s.stationName));

  const segments: RouteSegment[] = segMeters.map((s) => ({
    lineId: s.lineId,
    distance: s.meters / 1000,
    systemId: s.systemId,
  }));
  const totalMeters = segMeters.reduce((acc, s) => acc + s.meters, 0);

  return {
    stations,
    stationSteps,
    nodeIds,
    lineIdSequence,
    departDirectionSequence,
    distance: totalMeters / 1000,
    segments,
    estimatedFare: 0, // 票价估算在上层按系统 pricePerKm 计算
  };
}

function pushStationStep(
  steps: { stationName: string; departLineId?: string }[],
  stationName: string,
  departLineId?: string,
) {
  if (steps.length === 0 || steps[steps.length - 1].stationName !== stationName) {
    steps.push({ stationName, departLineId });
  }
}
