import { describe, expect, it } from 'vitest';
import { resolveRouteEdges } from './routeEdges';

type E = { id: string; lineId: string };

/** 由「区间 → 线路列表」构造 edgesOf；边 id = from-to-lineId（同插件）。 */
function edges(spec: Record<string, string[]>) {
  return (from: string, to: string): E[] =>
    (spec[`${from}-${to}`] ?? []).map((lineId) => ({ id: `${from}-${to}-${lineId}`, lineId }));
}

const lines = (picked: (E | null)[]) => picked.map((e) => e?.lineId ?? null);

describe('resolveRouteEdges 共线段选边', () => {
  // A-B 仅 L1；B-C、C-D 为 L1/L2 共线；D-E 仅 L2
  const shared = edges({ 'A-B': ['L1'], 'B-C': ['L2', 'L1'], 'C-D': ['L2', 'L1'], 'D-E': ['L2'] });

  it('有逐段提示时按提示选边，不受候选顺序影响', () => {
    expect(lines(resolveRouteEdges(['A', 'B', 'C', 'D'], shared, { lineHints: ['L1', 'L1', 'L1'] }))).toEqual([
      'L1',
      'L1',
      'L1',
    ]);
  });

  it('无提示时共线段归到沿路线延续下去的线路', () => {
    expect(lines(resolveRouteEdges(['A', 'B', 'C', 'D'], shared))).toEqual(['L1', 'L1', 'L1']);
    expect(lines(resolveRouteEdges(['B', 'C', 'D', 'E'], shared))).toEqual(['L2', 'L2', 'L2']);
  });

  it('连续长度平手时沿用上一段线路', () => {
    // A-B-C-D：B-C 处 L1 / L2 都还能覆盖 2 段，平手沿用 A-B 的 L1（候选顺序 L2 在前）
    expect(lines(resolveRouteEdges(['A', 'B', 'C', 'D'], shared))).toEqual(['L1', 'L1', 'L1']);
  });

  it('换线路线在共线区间整体归到后续线路，不在区间中途换色', () => {
    // A-B-C-D-E：B-C 处 L2 能覆盖 3 段 > L1 的 2 段，整个共线区间用 L2
    expect(lines(resolveRouteEdges(['A', 'B', 'C', 'D', 'E'], shared))).toEqual(['L1', 'L2', 'L2', 'L2']);
  });

  it('提示未命中时回退推断，允许线路收窄候选', () => {
    expect(lines(resolveRouteEdges(['B', 'C', 'D'], shared, { lineHints: ['X', 'X'] }))).toEqual(['L2', 'L2']);
    expect(lines(resolveRouteEdges(['B', 'C', 'D', 'E'], shared, { allowedLines: ['L1'] }))).toEqual([
      'L1',
      'L1',
      'L2',
    ]);
  });

  it('提示长度对不上节点序列时整体忽略', () => {
    expect(lines(resolveRouteEdges(['A', 'B', 'C', 'D'], shared, { lineHints: ['L2', 'L2'] }))).toEqual([
      'L1',
      'L1',
      'L1',
    ]);
  });

  it('缺段返回 null', () => {
    expect(lines(resolveRouteEdges(['A', 'X', 'B'], shared))).toEqual([null, null]);
  });
});
