import { describe, it, expect } from 'vitest';
import { RouteGraph } from './graph';
import { findByStation, findTransferJourneys } from './pathfind';
import type { FeatureCollection, Feature } from '../types';

/** 构造一个 Point feature（type=station/switch）。坐标为 [x, z, y]（与 geojson 一致）。 */
function point(id: string, type: 'station' | 'switch', name: string | null, x: number, z: number, y = 64): Feature {
  return {
    type: 'Feature',
    geometry: { type: 'Point', coordinates: [x, z, y] },
    properties: { id, type, world: 'w', ...(name ? { name } : {}) },
  } as Feature;
}

/** 构造一个 LineString feature（一段有向边）。 */
function line(
  id: string,
  from: string,
  to: string,
  lineId: string,
  length: number,
  extra: { departDir?: string; enterFrom?: string[]; enterTo?: string; railwaySystemId?: string } = {},
): Feature {
  return {
    type: 'Feature',
    geometry: { type: 'LineString', coordinates: [[0, 0, 64], [1, 0, 64]] },
    properties: { id, from, to, lineId, world: 'w', color: '#fff', length, ...extra },
  } as Feature;
}

function fc(features: Feature[]): FeatureCollection {
  return { type: 'FeatureCollection', features } as FeatureCollection;
}

describe('findTransferJourneys', () => {
  // 复刻插件 TransferJourneyTest：L1 从 A 绕远到 E，L2 从 B 短直达 E。B 的 L1/L2 站台是独立节点无连边。
  function scenario(): RouteGraph {
    return RouteGraph.fromFeatureCollection(
      fc([
        point('nA', 'station', 'A', 0, 0),
        point('nB1', 'station', 'B', 10, 0),
        point('nE1', 'station', 'E', 100, 0),
        point('nB2', 'station', 'B', 10, 20),
        point('nE2', 'station', 'E', 100, 20),
        line('e.L1.nA__nB1', 'nA', 'nB1', 'L1', 10, { departDir: 'e' }),
        line('e.L1.nB1__nE1', 'nB1', 'nE1', 'L1', 90, { departDir: 'e' }),
        line('e.L2.nB2__nE2', 'nB2', 'nE2', 'L2', 10, { departDir: 's' }),
      ]),
    );
  }

  it('找到经 B 的换乘方案，总距离 20km 优于直达 100km', () => {
    const g = scenario();
    const direct = findByStation(g, 'A', 'E', 0);
    expect(direct.length).toBeGreaterThan(0);
    expect(direct[0].distance).toBeCloseTo(100 / 1000, 9);

    const plans = findTransferJourneys(g, 'A', 'E', 0, 30, 0);
    expect(plans.length).toBeGreaterThan(0);
    const best = plans[0];
    expect(best.transferStations[0]).toBe('B');
    expect(best.legs.length).toBe(2);
    expect(best.totalDistance).toBeCloseTo(20 / 1000, 9);
    expect(best.totalDistance).toBeLessThan(direct[0].distance);
  });

  it('起终点相同返回空', () => {
    expect(findTransferJourneys(scenario(), 'A', 'A', 0, 30, 0)).toEqual([]);
  });

  it('minImprovement 过滤边际收益的换乘', () => {
    const g = scenario();
    // 阈值 = 100 * (1-0.9) = 10km；换乘总距 20km 不满足，被过滤
    expect(findTransferJourneys(g, 'A', 'E', 3, 30, 0.9)).toEqual([]);
    // 0.2 时阈值 80km，20km 满足
    expect(findTransferJourneys(g, 'A', 'E', 3, 30, 0.2).length).toBeGreaterThan(0);
  });
});

describe('findByStation 入向面门控', () => {
  // 直行道岔 S：从 nA 到达面 "1_0"，只有到达面属于 {1_0} 的出边才能续接直行段到 D。
  // 反向牌出边 enterFrom={9_9} 不含 1_0，应被门控拒绝，防止走出物理非法路线。
  it('拒绝到达面不在允许集合内的续接', () => {
    const g = RouteGraph.fromFeatureCollection(
      fc([
        point('nA', 'station', 'A', 0, 0),
        point('nS', 'switch', null, 10, 0),
        point('nD', 'station', 'D', 20, 0),
        point('nX', 'station', 'X', 20, 10),
        // A->S 到达 S 的到达面为 1_0
        line('e.L1.nA__nS', 'nA', 'nS', 'L1', 10, { departDir: 'e', enterTo: '1_0' }),
        // S->D 合法：允许到达面含 1_0
        line('e.L1.nS__nD', 'nS', 'nD', 'L1', 10, { departDir: 'e', enterFrom: ['1_0'] }),
        // S->X 非法：只给到达面 9_9 的车（反向），从 1_0 来的车不能走
        line('e.L1.nS__nX', 'nS', 'nX', 'L1', 5, { departDir: 's', enterFrom: ['9_9'] }),
      ]),
    );
    const toD = findByStation(g, 'A', 'D', 0);
    expect(toD.length).toBeGreaterThan(0);
    const toX = findByStation(g, 'A', 'X', 0);
    // X 只能经非法出边到达 → 门控后无路线
    expect(toX.length).toBe(0);
  });

  it('缺失门控字段时放行（向后兼容）', () => {
    const g = RouteGraph.fromFeatureCollection(
      fc([
        point('nA', 'station', 'A', 0, 0),
        point('nS', 'switch', null, 10, 0),
        point('nX', 'station', 'X', 20, 10),
        line('e.L1.nA__nS', 'nA', 'nS', 'L1', 10, { departDir: 'e' }),
        line('e.L1.nS__nX', 'nS', 'nX', 'L1', 5, { departDir: 's', enterFrom: ['9_9'] }),
      ]),
    );
    // inLink.enterTo 缺失 → 放行
    expect(findByStation(g, 'A', 'X', 0).length).toBeGreaterThan(0);
  });
});
