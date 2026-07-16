import type { RailwaySystem, RoutePath, FrontendConfig } from '../types';
import { RouteGraph } from './graph';
import { findByStation, findTransferJourneys } from './pathfind';
import { estimateFare, fareDetails, mergeFareDetails } from './fare';
import { rankWithMinDirect, type Candidate } from './ranker';

/** 寻路所需的可序列化输入（可在主线程或 Web Worker 中复用）。 */
export interface ComputeInput {
  startStation: string;
  endStation: string;
  systems: RailwaySystem[];
  /** 折返站集合成员，key = `${lineId}|${stationName}`。 */
  reverseKeys: string[];
  cfg: FrontendConfig;
}

/**
 * 纯寻路：给定图与起终点，返回排序后的候选路线（直达 + 联程票）。
 * 无副作用、不依赖运行时全局配置，故可安全在 Web Worker 中执行。
 */
export function computeCandidates(graph: RouteGraph, input: ComputeInput): RoutePath[] {
  const { startStation, endStation, systems, reverseKeys, cfg } = input;
  const systemMap = new Map(systems.map((s) => [s.id, s]));
  const reverseSet = new Set(reverseKeys);
  const globalRate = cfg.defaultPricePerKm;
  const isReverse = (lineId: string, station: string) => reverseSet.has(`${lineId}|${station}`);
  const withFare = (p: RoutePath): RoutePath => {
    const details = fareDetails(p, systemMap, globalRate);
    return { ...p, fareDetails: details, estimatedFare: estimateFare({ ...p, fareDetails: details }, systemMap, globalRate) };
  };

  // 直达候选池：覆盖「距离前 N ∪ 票价前 M」，两者之和作上限；任一不限(<=0)时退回不限条数(0)。
  const directPool =
    cfg.maxDistanceResults <= 0 || cfg.maxPriceResults <= 0 ? 0 : cfg.maxDistanceResults + cfg.maxPriceResults;
  const directCandidates: RoutePath[] = findByStation(graph, startStation, endStation, directPool, isReverse).map(
    (p) => ({ ...withFare(p), kind: 'direct' as const }),
  );

  // 联程票候选：限方案数（换乘站用站名级缩合矩阵枚举全部，不再截断候选）
  const throughCandidates: RoutePath[] = findTransferJourneys(
    graph,
    startStation,
    endStation,
    cfg.maxTransferResults,
    cfg.transferMinImprovement,
    isReverse,
  ).map((j) => {
    const legs = j.legs.map(withFare);
    const legFareDetails = legs.map((l) => l.fareDetails ?? []);
    const merged = mergeFareDetails(legFareDetails);
    const totalFare = Math.round(legs.reduce((sum, l) => sum + l.estimatedFare, 0) * 100) / 100;
    const rep = legs[0];
    return {
      ...rep,
      kind: 'through' as const,
      distance: j.totalDistance,
      estimatedFare: totalFare,
      fareDetails: merged,
      journey: { legs, transferStations: j.transferStations, totalDistance: j.totalDistance, totalFare, fareDetails: merged },
    };
  });

  // 汇总排序候选：直达在前、联程票在后（下标空间连续）
  const all = [...directCandidates, ...throughCandidates];
  const rankInput: Candidate[] = all.map((p, i) => ({ index: i, distance: p.distance, price: p.estimatedFare }));
  const order = rankWithMinDirect(
    rankInput,
    directCandidates.length,
    cfg.maxDistanceResults,
    cfg.maxPriceResults,
    cfg.searchWeightDistance,
    cfg.searchWeightPrice,
    cfg.minDirectResults,
  );
  return order.map((idx) => all[idx]);
}
