import { describe, expect, it } from 'vitest';
import { buildColorSpans, buildExpressAxis, buildGradientStops, locateTrainOnExpressAxis, pickMidLabels } from './ExpressRouteDiagram';
import { RouteGraph } from '../routing/graph';
import type { FeatureCollection, Train } from '../types';

const seg = (start: number, length: number, color: string | null) => ({ start, length, color });

describe('buildColorSpans', () => {
  it('相邻同色段合并，按长度比例划分区间', () => {
    const spans = buildColorSpans([seg(0, 30, '#F00'), seg(30, 20, '#f00'), seg(50, 50, '#00f')], 100);
    expect(spans).toEqual([
      { color: '#f00', from: 0, to: 0.5 },
      { color: '#00f', from: 0.5, to: 1 },
    ]);
  });

  it('无颜色段并入前一区间，开头的并入后一区间', () => {
    const spans = buildColorSpans([seg(0, 10, null), seg(10, 40, '#f00'), seg(50, 10, null), seg(60, 40, '#0f0')], 100);
    expect(spans).toEqual([
      { color: '#f00', from: 0, to: 0.6 },
      { color: '#0f0', from: 0.6, to: 1 },
    ]);
  });
});

describe('buildGradientStops', () => {
  it('交界两侧各留半个过渡带', () => {
    const stops = buildGradientStops(
      [
        { color: '#f00', from: 0, to: 0.5 },
        { color: '#00f', from: 0.5, to: 1 },
      ],
      0.1,
    );
    expect(stops.map((s) => s.color)).toEqual(['#f00', '#f00', '#00f', '#00f']);
    expect(stops[1].offset).toBeCloseTo(0.45);
    expect(stops[2].offset).toBeCloseTo(0.55);
  });

  it('过渡带不超过相邻短区间长度的一半', () => {
    const stops = buildGradientStops(
      [
        { color: '#f00', from: 0, to: 0.9 },
        { color: '#00f', from: 0.9, to: 0.94 },
        { color: '#0f0', from: 0.94, to: 1 },
      ],
      0.2,
    );
    // 中间区间长 0.04 → 两侧过渡带宽均为 0.02
    expect(stops[1].offset).toBeCloseTo(0.89);
    expect(stops[2].offset).toBeCloseTo(0.91);
    expect(stops[3].offset).toBeCloseTo(0.93);
    expect(stops[4].offset).toBeCloseTo(0.95);
  });
});

const point = (id: string, x: number, type: 'station' | 'switch', name?: string) => ({
  type: 'Feature' as const,
  geometry: { type: 'Point' as const, coordinates: [x, 0] },
  properties: { id, type, world: 'w', name },
});
const line = (from: string, to: string, x1: number, x2: number, color: string, lineId: string) => ({
  type: 'Feature' as const,
  geometry: { type: 'LineString' as const, coordinates: [[x1, 0], [x2, 0]] },
  properties: { id: `${from}-${to}`, from, to, lineId, world: 'w', color, length: Math.abs(x2 - x1) },
});

describe('buildExpressAxis', () => {
  const fc = {
    type: 'FeatureCollection',
    features: [
      point('a', 0, 'station', 'A'),
      point('s', 50, 'switch'),
      point('b', 100, 'station', 'B'),
      point('c', 200, 'station', 'C'),
      line('a', 's', 0, 50, '#ff0000', 'L1'),
      line('s', 'b', 50, 100, '#ff0000', 'L1'),
      // 反向存储的联络线段，也要按 geojson 颜色参与
      line('c', 'b', 200, 100, '#888888', 'contact-1'),
    ],
  } as unknown as FeatureCollection;
  const graph = RouteGraph.fromFeatureCollection(fc);

  it('按路线逐段累计长度、颜色与中途站刻度', () => {
    const axis = buildExpressAxis(['a', 's', 'b', 'c'], fc, graph)!;
    expect(axis.total).toBe(200);
    expect(axis.spans).toEqual([
      { color: '#ff0000', from: 0, to: 0.5 },
      { color: '#888888', from: 0.5, to: 1 },
    ]);
    expect(axis.midStations).toEqual([{ name: 'B', progress: 0.5 }]);
  });

  it('列车投影到路线得到进度，偏离超过阈值时隐藏', () => {
    const axis = buildExpressAxis(['a', 's', 'b', 'c'], fc, graph)!;
    const train = { world: 'w', head: { x: 150, y: 0, z: 3, yaw: 0 } } as unknown as Train;
    expect(locateTrainOnExpressAxis(train, axis, 25)).toBeCloseTo(0.75);
    expect(locateTrainOnExpressAxis({ ...train, head: { x: 150, y: 0, z: 40, yaw: 0 } }, axis, 25)).toBeNull();
    expect(locateTrainOnExpressAxis({ ...train, world: 'other' }, axis, 25)).toBeNull();
  });
});

describe('pickMidLabels', () => {
  it('与上一个标签或终点标签重叠的中途站省略站名', () => {
    // 起点 y=0（底边 13），终点 y=200（顶边 187）；标签高 15
    expect(pickMidLabels([10, 30, 40, 60, 190], 0, 200)).toEqual([1, 3]);
  });
});

describe('buildExpressAxis 正线判定', () => {
  it('经过正线（track=main）时，由进站道岔沿停靠线找出所跨车站作为中途站', () => {
    const lineT = (from: string, to: string, x1: number, x2: number, track?: 'main' | 'siding') => ({
      ...line(from, to, x1, x2, '#ff0000', 'L1'),
      properties: { ...line(from, to, x1, x2, '#ff0000', 'L1').properties, track },
    });
    // A → in ⇒(正线) out → C；停靠线 in → x → P（车站 P 被正线跨过）
    const fc = {
      type: 'FeatureCollection',
      features: [
        point('a', 0, 'station', 'A'),
        point('in', 50, 'switch'),
        point('x', 60, 'switch'),
        point('p', 100, 'station', 'P'),
        point('out', 150, 'switch'),
        point('c', 200, 'station', 'C'),
        lineT('a', 'in', 0, 50),
        lineT('in', 'x', 50, 60, 'siding'),
        lineT('x', 'p', 60, 100, 'siding'),
        lineT('in', 'out', 50, 150, 'main'),
        lineT('out', 'c', 150, 200),
      ],
    } as unknown as FeatureCollection;
    const graph = RouteGraph.fromFeatureCollection(fc);
    const axis = buildExpressAxis(['a', 'in', 'out', 'c'], fc, graph)!;
    expect(axis.midStations).toEqual([{ name: 'P', progress: 0.5 }]);
  });
});

describe('buildExpressAxis 共线段', () => {
  // a→s→b 为 L1 / L2 共线（L2 要素在前），b→c 仅 L1：共线段应取 L1 颜色
  const sharedLine = (from: string, to: string, x1: number, x2: number, color: string, lineId: string) => {
    const f = line(from, to, x1, x2, color, lineId);
    return { ...f, properties: { ...f.properties, id: `${from}-${to}-${lineId}` } };
  };
  const fc = {
    type: 'FeatureCollection',
    features: [
      point('a', 0, 'station', 'A'),
      point('s', 50, 'switch'),
      point('b', 100, 'station', 'B'),
      point('c', 200, 'station', 'C'),
      sharedLine('a', 's', 0, 50, '#0000ff', 'L2'),
      sharedLine('a', 's', 0, 50, '#ff0000', 'L1'),
      sharedLine('s', 'b', 50, 100, '#0000ff', 'L2'),
      sharedLine('s', 'b', 50, 100, '#ff0000', 'L1'),
      sharedLine('b', 'c', 100, 200, '#ff0000', 'L1'),
    ],
  } as unknown as FeatureCollection;

  it('共线段按路线延续的线路取色', () => {
    const axis = buildExpressAxis(['a', 's', 'b', 'c'], fc, RouteGraph.fromFeatureCollection(fc))!;
    expect(axis.spans).toEqual([{ color: '#ff0000', from: 0, to: 1 }]);
  });

  it('逐段 lineId 优先于连续性推断', () => {
    const axis = buildExpressAxis(['a', 's', 'b', 'c'], fc, RouteGraph.fromFeatureCollection(fc), ['L2', 'L2', 'L1'])!;
    expect(axis.spans).toEqual([
      { color: '#0000ff', from: 0, to: 0.5 },
      { color: '#ff0000', from: 0.5, to: 1 },
    ]);
  });
});
