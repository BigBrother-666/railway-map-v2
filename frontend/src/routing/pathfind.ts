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

/** KSP_SAFETY_CAP：未限制条数时每站台 K-最短路的安全上限（复刻插件 GeoRouteEngine）。 */
const KSP_SAFETY_CAP = 16;

/**
 * 按起点站名 + 终点站名求候选路线（距离升序、两级去重）。复刻插件 GeoRouteEngine.findByStation：
 * 枚举起点各站台各求 K 条 → 一级按 departDirectionSequence 去重 → 二级按 stationSequence 去重，
 * 择优规则 isBetterRoute（转线次数少者优先，相同则距离短者）。
 */
export function findByStation(
  graph: RouteGraph,
  startStation: string,
  endStation: string,
  maxResults = 10,
  isReverse: ReversePredicate = () => false,
): RoutePath[] {
  const kPerPlatform = maxResults > 0 ? maxResults : KSP_SAFETY_CAP;
  const all: RoutePath[] = [];
  for (const startId of graph.stationNodes(startStation)) {
    all.push(...kShortest(graph, startId, endStation, kPerPlatform, isReverse));
  }

  // 一级去重：departDirectionSequence 相同视为重复路线，保留 isBetterRoute 更优者
  const deduped = new Map<string, RoutePath>();
  for (const p of all) {
    const key = departDirectionKey(p);
    const old = deduped.get(key);
    if (!old || isBetterRoute(p, old)) deduped.set(key, p);
  }

  // 二级去重：stationSequence（经过车站序列）相同也视为同一路线
  const byStations = new Map<string, RoutePath>();
  for (const p of deduped.values()) {
    const key = p.stations.join('>');
    const old = byStations.get(key);
    if (!old || isBetterRoute(p, old)) byStations.set(key, p);
  }

  const ret = [...byStations.values()].sort((a, b) => a.distance - b.distance);
  return maxResults > 0 ? ret.slice(0, maxResults) : ret;
}

function departDirectionKey(path: RoutePath): string {
  return (path.departDirectionSequence ?? []).map((dir) => dir ?? '').join('>');
}

/** 择优规则（复刻 isBetterRoute）：转线次数少者优先；相同则距离短者优先。candidate 应取代 current 时返回 true。 */
function isBetterRoute(candidate: RoutePath, current: RoutePath): boolean {
  const candTransfers = lineTransferCount(candidate.lineIdSequence);
  const curTransfers = lineTransferCount(current.lineIdSequence);
  if (candTransfers !== curTransfers) return candTransfers < curTransfers;
  return candidate.distance < current.distance;
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
      // 入向面门控：与插件 enterFaceAllows 一致，拒绝「从错误到达面接反向牌出边」的非法接续
      if (!enterFaceAllows(cur.link, link)) continue;
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

/**
 * 入向面门控（复刻插件 GeoRouteEngine.enterFaceAllows）：沿 inLink 到达当前节点后，是否允许接着走 outLink。
 * 仅当 outLink 声明了允许到达面集合（enterFrom 非空）、inLink 也带到达面（enterTo 非空）、且该到达面不在
 * 集合内时才拒绝。任一信息缺失（起点首段无入边、旧 geojson 无门控字段）都放行，保证向后兼容与起点正常展开。
 */
function enterFaceAllows(inLink: GraphLink | null, outLink: GraphLink): boolean {
  if (!inLink) return true;
  const allowed = outLink.enterFrom;
  if (!allowed || allowed.length === 0) return true;
  const arrivedFace = inLink.enterTo;
  if (arrivedFace == null) return true;
  return allowed.includes(arrivedFace);
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

/** 一次换乘行程的原始寻路结果（票价在上层计算）。 */
export interface RawJourney {
  legs: RoutePath[];
  transferStations: string[];
  totalDistance: number;
}

/**
 * 启发式寻找「一次换乘」的行程方案（两段直达）：起点站 → 换乘站 → 终点站。复刻插件
 * GeoRouteEngine.findTransferJourneys。用于两站没有便宜直达、但「中途某站下车换乘另一条线路」更近的场景。
 *
 * @param maxResults     最多返回方案数（<=0 不限制）
 * @param maxCandidates  最多考察的候选换乘站数（<=0 不限制），优先直达路径上的经停站
 * @param minImprovement 最低改善比例 [0,1)：换乘总距离须 < 最短直达 ×(1 - 此值)；两站无直达时门槛不生效
 */
export function findTransferJourneys(
  graph: RouteGraph,
  startStation: string,
  endStation: string,
  maxResults: number,
  maxCandidates: number,
  minImprovement: number,
  isReverse: ReversePredicate = () => false,
): RawJourney[] {
  if (!startStation || !endStation || startStation === endStation) return [];

  // 直达最短距离 → 阈值。无直达则 +∞，任何换乘方案都接受
  const directPaths = findByStation(graph, startStation, endStation, 0, isReverse);
  let bestDirect = Number.POSITIVE_INFINITY;
  for (const p of directPaths) bestDirect = Math.min(bestDirect, p.distance);
  let threshold = bestDirect;
  if (Number.isFinite(bestDirect) && minImprovement > 0) {
    threshold = bestDirect * (1 - minImprovement);
  }

  // 候选换乘站集合（启发式，保序）：直达路径经停站 ∪ 终点线路经停站，去掉起终点
  const candidates = new Set<string>();
  for (const p of directPaths) for (const s of p.stations) candidates.add(s);
  for (const s of stationsOnEndLines(graph, endStation)) candidates.add(s);
  candidates.delete(startStation);
  candidates.delete(endStation);
  let candidateList = [...candidates];
  if (maxCandidates > 0 && candidateList.length > maxCandidates) {
    candidateList = candidateList.slice(0, maxCandidates);
  }

  // 逐候选站求两段最短路，按换乘站去重（留总距离最短者）
  const byTransfer = new Map<string, RawJourney>();
  for (const mid of candidateList) {
    const leg1 = findByStation(graph, startStation, mid, 1, isReverse);
    if (leg1.length === 0) continue;
    const leg2 = findByStation(graph, mid, endStation, 1, isReverse);
    if (leg2.length === 0) continue;
    const total = leg1[0].distance + leg2[0].distance;
    if (total >= threshold) continue;
    const old = byTransfer.get(mid);
    if (!old || total < old.totalDistance) {
      byTransfer.set(mid, { legs: [leg1[0], leg2[0]], transferStations: [mid], totalDistance: total });
    }
  }

  const ret = [...byTransfer.values()].sort((a, b) => a.totalDistance - b.totalDistance);
  return maxResults > 0 ? ret.slice(0, maxResults) : ret;
}

/**
 * 收集终点站所属线路经停的所有车站名（换乘站候选来源之一）。复刻插件 stationsOnEndLines：
 * 终点站各站台的 lineIds 即服务该终点站的线路；再扫全图 station 节点，凡 lineIds 与之有交集者都是候选。
 */
function stationsOnEndLines(graph: RouteGraph, endStation: string): string[] {
  const endLineIds = new Set<string>();
  for (const id of graph.stationNodes(endStation)) {
    graph.nodes.get(id)?.lineIds.forEach((l) => endLineIds.add(l));
  }
  const result = new Set<string>();
  if (endLineIds.size === 0) return [];
  for (const node of graph.nodes.values()) {
    if (node.type !== 'station' || !node.name) continue;
    for (const lineId of node.lineIds) {
      if (endLineIds.has(lineId)) {
        result.add(node.name);
        break;
      }
    }
  }
  return [...result];
}
