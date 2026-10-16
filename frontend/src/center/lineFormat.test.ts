import { describe, expect, it } from 'vitest';
import { formatNotices, formatStations, parseNotices, parseStations, validateNotices } from './lineFormat';

describe('bossbar-stations 互转', () => {
  it('解析折返站与转线目标并原样还原', () => {
    const raw = ['S1-A', 'S1-B:RV', 'S1-C', 'line2:S2-B'];
    const p = parseStations(raw, 'line1', ['line1', 'line2']);
    expect(p.stations).toEqual([
      { name: 'S1-A', reverse: false },
      { name: 'S1-B', reverse: true },
      { name: 'S1-C', reverse: false },
    ]);
    expect(p.nextLineId).toBe('line2');
    expect(p.nextEntryStation).toBe('S2-B');
    expect(formatStations(p)).toEqual(raw);
  });

  it('末项不是已知线路时当作普通站名', () => {
    const p = parseStations(['A', 'B:C'], 'l', ['l']);
    expect(p.nextLineId).toBe('');
    expect(p.stations[1]).toEqual({ name: 'B:C', reverse: false });
  });

  it('末项为其它铁路系统的线路时也识别为转线', () => {
    const p = parseStations(['A', 'B', 'otherSysLine'], 'l', ['l', 'otherSysLine']);
    expect(p.nextLineId).toBe('otherSysLine');
    expect(p.nextEntryStation).toBe('');
    expect(p.stations.map((s) => s.name)).toEqual(['A', 'B']);
  });

  it('自身线路 id 不视为转线', () => {
    expect(parseStations(['A', 'l'], 'l', ['l']).nextLineId).toBe('');
  });
});

describe('notice 互转', () => {
  it('解析 sound / announce 并还原', () => {
    const raw = 'sound:noteblock.xxxx,0.56,0.5||announce:列车即将到达 {curr_station}站';
    const rows = parseNotices(raw);
    expect(rows).toEqual([
      { kind: 'sound', name: 'noteblock.xxxx', pitch: '0.56', volume: '0.5' },
      { kind: 'announce', text: '列车即将到达 {curr_station}站' },
    ]);
    expect(formatNotices(rows)).toBe(raw);
  });

  it('校验数字与空文本', () => {
    expect(validateNotices([{ kind: 'sound', name: 'a', pitch: 'x', volume: '1' }])).not.toBeNull();
    expect(validateNotices([{ kind: 'announce', text: ' ' }])).not.toBeNull();
    expect(validateNotices([{ kind: 'sound', name: 'a', pitch: '1', volume: '0.5' }])).toBeNull();
  });
});
