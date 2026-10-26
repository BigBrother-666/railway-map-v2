/**
 * 路线逐段选边：共线段（同一物理轨道上有多条线路）在 geojson 里是多条 from/to 相同、lineId 不同的
 * LineString（插件边 id = from+to+lineId）。按节点序列取边时必须在这些候选里挑出路线实际所属的线路，
 * 否则高亮 / 路线图会用错线路颜色。与后端 internal/geo/route_geometry.go 的 pickRouteEdges 同一规则。
 */

export interface RouteEdgeOptions {
  /** 逐段线路提示（如寻路结果的 lineIdSequence）；长度须为 nodeIds.length-1，否则忽略；命中候选时直接采用。 */
  lineHints?: readonly string[];
  /** 允许的线路集合（如乘车历史经过的线路）；候选中有命中时只在命中者里选。 */
  allowedLines?: readonly string[];
}

/**
 * 为节点序列的每一段选一条边。
 *
 * 规则：有提示且命中候选 → 用提示；否则在候选里选「从本段起能沿路线连续覆盖最多段」的线路，
 * 平手时沿用上一段的线路——共线区间归到真正延续下去的那条线路，且不会在区间中途换色。
 *
 * @param nodeIds 路线节点 id 序列
 * @param edgesOf 返回 from→to 的全部候选边（每条边带 lineId）
 * @param opts    提示 / 允许线路
 * @return 每段选中的边（无候选为 null），长度 = nodeIds.length-1
 */
export function resolveRouteEdges<T extends { lineId: string }>(
  nodeIds: readonly string[],
  edgesOf: (from: string, to: string) => readonly T[],
  opts: RouteEdgeOptions = {},
): (T | null)[] {
  const n = Math.max(0, nodeIds.length - 1);
  const allowed = opts.allowedLines?.length ? new Set(opts.allowedLines) : null;
  // 长度对不上节点序列的提示无法逐段对齐，整体忽略
  const hints = opts.lineHints?.length === n ? opts.lineHints : undefined;
  const cands: (readonly T[])[] = [];
  for (let i = 0; i < n; i++) {
    let c = edgesOf(nodeIds[i], nodeIds[i + 1]);
    if (allowed && c.length > 1) {
      const filtered = c.filter((e) => allowed.has(e.lineId));
      if (filtered.length) c = filtered;
    }
    cands.push(c);
  }
  // runs[i]：lineId → 从第 i 段起该线路连续出现在候选中的段数（倒序递推）
  const runs: Map<string, number>[] = new Array(n);
  for (let i = n - 1; i >= 0; i--) {
    const m = new Map<string, number>();
    for (const e of cands[i]) m.set(e.lineId, 1 + (runs[i + 1]?.get(e.lineId) ?? 0));
    runs[i] = m;
  }

  const out: (T | null)[] = [];
  let prev: string | null = null;
  for (let i = 0; i < n; i++) {
    const c = cands[i];
    if (c.length === 0) {
      out.push(null);
      continue;
    }
    const hint = hints?.[i];
    let pick = hint ? c.find((e) => e.lineId === hint) : undefined;
    if (!pick) {
      let bestRun = -1;
      for (const e of c) {
        const run = runs[i].get(e.lineId) ?? 0;
        if (run > bestRun || (run === bestRun && e.lineId === prev)) {
          pick = e;
          bestRun = run;
        }
      }
    }
    out.push(pick!);
    prev = pick!.lineId;
  }
  return out;
}
