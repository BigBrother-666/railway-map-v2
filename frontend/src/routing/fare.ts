/**
 * 票价估算：按各段所属铁路系统的 pricePerKm × 段公里数累加（未登录时不计成员免票，仅供展示，
 * 最终以购票返回为准，见 §4.6 / §4.7）。
 */
import type { FareDetail, RailwaySystem, RoutePath } from '../types';

export function estimateFare(path: RoutePath, systems: Map<string, RailwaySystem>, globalPricePerKm: number): number {
  const details = fareDetails(path, systems, globalPricePerKm);
  const total = details.reduce((sum, d) => sum + d.price, 0);
  return Math.round(total * 100) / 100;
}

export function fareDetails(path: RoutePath, systems: Map<string, RailwaySystem>, globalPricePerKm: number): FareDetail[] {
  const bySystem = new Map<string, FareDetail>();
  for (const seg of path.segments) {
    const systemId = seg.systemId || '__contact__';
    const sys = systems.get(seg.systemId);
    const rate = sys?.pricePerKm ?? globalPricePerKm;
    const existing = bySystem.get(systemId);
    if (existing) {
      existing.distance += seg.distance;
      existing.price += seg.distance * rate;
    } else {
      bySystem.set(systemId, {
        systemId,
        systemName: sys?.name ?? (seg.systemId ? seg.systemId : '联络线'),
        distance: seg.distance,
        price: seg.distance * rate,
        rate,
      });
    }
  }
  return [...bySystem.values()].map((d) => ({
    ...d,
    distance: Math.round(d.distance * 100) / 100,
    price: Math.round(d.price * 100) / 100,
  }));
}
