import { describe, expect, it } from 'vitest';
import { chooseLayout } from './LineRouteDiagram';

// chooseLayout 只读取 stations.length 与 intervalCount
const axisOf = (stationCount: number, ring = false) =>
  ({
    stations: Array.from({ length: stationCount }, (_, i) => ({ name: `S${i}`, distance: i, progress: 0 })),
    intervalCount: ring ? stationCount : stationCount - 1,
  }) as unknown as Parameters<typeof chooseLayout>[1];

describe('chooseLayout', () => {
  it('非环线最小间距放得下时直线展示，间距按高度放大但不超过最大值', () => {
    // 5 站 4 区间：4*32+36=164 <= 400 → 直线，(400-36)/4=91 → 封顶 60
    expect(chooseLayout(false, axisOf(5), 400, 32, 60)).toEqual({ folded: false, stationGap: 60 });
    // (200-36)/4 = 41
    expect(chooseLayout(false, axisOf(5), 200, 32, 60)).toEqual({ folded: false, stationGap: 41 });
  });

  it('非环线最小间距仍超出可视高度时折叠，折叠间距按高度求最优', () => {
    // 21 站 20 区间：20*32+36=676 > 500 → 折叠；左侧 11 站 10 区间：(500-36-26)/10=43.8
    const r = chooseLayout(false, axisOf(21), 500, 32, 60);
    expect(r.folded).toBe(true);
    expect(r.stationGap).toBeCloseTo(43.8);
  });

  it('折叠后高度仍不足时间距不低于最小值，宽松时不超过最大值', () => {
    expect(chooseLayout(false, axisOf(60), 300, 32, 60)).toEqual({ folded: true, stationGap: 32 });
    expect(chooseLayout(false, axisOf(12), 380, 32, 60).stationGap).toBeLessThanOrEqual(60);
  });

  it('环线从不折叠', () => {
    expect(chooseLayout(true, axisOf(30, true), 300, 32, 60).folded).toBe(false);
  });

  it('未测量时按直线 + 最小间距', () => {
    expect(chooseLayout(false, axisOf(30), null, 32, 60)).toEqual({ folded: false, stationGap: 32 });
  });
});
