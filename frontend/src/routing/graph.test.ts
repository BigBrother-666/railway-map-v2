import { describe, expect, it } from 'vitest';
import { RouteGraph } from './graph';
import type { FeatureCollection } from '../types';

/** 构造 A→C→B 的图（模拟 geojson 更新后在 A、B 之间插入了节点 C）。 */
function graphWithInsertedNode(): RouteGraph {
  const point = (id: string, x: number) => ({
    type: 'Feature' as const,
    properties: { id, type: 'station' as const, world: 'w', name: id },
    geometry: { type: 'Point' as const, coordinates: [x, 0, 64] },
  });
  const line = (from: string, to: string) => ({
    type: 'Feature' as const,
    properties: { id: `${from}-${to}`, from, to, lineId: 'L1', world: 'w', color: '#f00', length: 10 },
    geometry: { type: 'LineString' as const, coordinates: [[0, 0], [1, 0]] },
  });
  const fc = {
    type: 'FeatureCollection',
    features: [point('A', 0), point('C', 5), point('B', 10), line('A', 'C'), line('C', 'B')],
  } as FeatureCollection;
  return RouteGraph.fromFeatureCollection(fc);
}

describe('RouteGraph 历史路线修补', () => {
  it('hasAllEdges 能识别缺失的相邻边', () => {
    const g = graphWithInsertedNode();
    expect(g.hasAllEdges(['A', 'C', 'B'])).toBe(true);
    expect(g.hasAllEdges(['A', 'B'])).toBe(false);
  });

  it('repairPath 用最短路补齐中间新增的节点', () => {
    const g = graphWithInsertedNode();
    expect(g.repairPath(['A', 'B'])).toEqual(['A', 'C', 'B']);
  });

  it('repairPath 遇到已删除节点时保持原样', () => {
    const g = graphWithInsertedNode();
    expect(g.repairPath(['A', 'X', 'B'])).toEqual(['A', 'X', 'B']);
  });
});
