import { describe, it, expect } from 'vitest';
import { rank, rankWithMinDirect, type Candidate } from './ranker';

describe('TicketRanker.rank', () => {
  it('距离前 N ∪ 票价前 M 合并去重后按加权排序', () => {
    // c0 最短最贵，c1 中等，c2 最长最便宜
    const cands: Candidate[] = [
      { index: 0, distance: 10, price: 30 },
      { index: 1, distance: 20, price: 20 },
      { index: 2, distance: 30, price: 10 },
    ];
    // 距离前 1 = c0，票价前 1 = c2；合并 {c0,c2}，权重 0.5/0.5 归一化后并列，保持加入顺序 c0,c2
    const order = rank(cands, 1, 1, 0.5, 0.5);
    expect(order).toEqual([0, 2]);
  });

  it('maxByDistance<=0 表示不限制，纳入全部', () => {
    const cands: Candidate[] = [
      { index: 0, distance: 10, price: 30 },
      { index: 1, distance: 20, price: 20 },
      { index: 2, distance: 30, price: 10 },
    ];
    const order = rank(cands, 0, 0, 1, 0); // 只看距离
    expect(order).toEqual([0, 1, 2]);
  });

  it('空候选返回空', () => {
    expect(rank([], 5, 5, 0.5, 0.5)).toEqual([]);
  });
});

describe('TicketRanker.rankWithMinDirect', () => {
  it('结果全是联程票时补最优直达到最前', () => {
    // index<2 为直达，index>=2 为联程票。联程票距离/票价都更优，会挤掉直达
    const cands: Candidate[] = [
      { index: 0, distance: 100, price: 100 }, // 直达（差）
      { index: 1, distance: 90, price: 90 }, // 直达（较好）
      { index: 2, distance: 10, price: 10 }, // 联程票
      { index: 3, distance: 12, price: 12 }, // 联程票
    ];
    const order = rankWithMinDirect(cands, 2, 1, 1, 0.5, 0.5, 1);
    // 结果首位应是最优直达 index 1（兜底补入），其后是排序结果
    expect(order[0]).toBe(1);
    expect(order).toContain(2);
  });

  it('结果已有直达时不兜底', () => {
    const cands: Candidate[] = [
      { index: 0, distance: 10, price: 10 }, // 直达且最优
      { index: 1, distance: 50, price: 50 }, // 联程票
    ];
    const order = rankWithMinDirect(cands, 1, 5, 5, 0.5, 0.5, 1);
    expect(order[0]).toBe(0);
    // 未额外补入重复的直达
    expect(order.filter((i) => i === 0).length).toBe(1);
  });

  it('minDirect<=0 时不兜底', () => {
    const cands: Candidate[] = [
      { index: 0, distance: 100, price: 100 },
      { index: 1, distance: 10, price: 10 },
    ];
    const order = rankWithMinDirect(cands, 1, 1, 1, 1, 0, 0);
    // 距离前 1 = index1（联程票），无兜底，直达不入选
    expect(order).toEqual([1]);
  });
});
