/**
 * 购票搜索结果的「距离 / 票价混合排序」纯逻辑。复刻插件 TicketRanker：
 * 1. 取「距离最近」前 N 与「票价最低」前 M，合并去重作为展示集；
 * 2. 展示集内距离、票价各归一化到 [0,1]（按集合内 min/max 线性缩放）；
 * 3. weight = wDistance*归一化距离 + wPrice*归一化票价，weight 越小越靠前。
 */

/** 一个候选的排序输入：原始下标 + 总距离 + 总票价。 */
export interface Candidate {
  index: number;
  distance: number;
  price: number;
}

/** 对候选做「距离前 N ∪ 票价前 M → 归一化加权排序」，返回排序后的原始下标序列（已去重）。 */
export function rank(
  candidates: Candidate[],
  maxByDistance: number,
  maxByPrice: number,
  wDistance: number,
  wPrice: number,
): number[] {
  if (!candidates || candidates.length === 0) return [];

  const byDistance = [...candidates].sort((a, b) => a.distance - b.distance);
  const byPrice = [...candidates].sort((a, b) => a.price - b.price);

  // 合并去重（按原始下标），保持「先距离后票价」的加入顺序
  const selected = new Set<number>();
  addTop(selected, byDistance, maxByDistance);
  addTop(selected, byPrice, maxByPrice);

  return sortByWeight([...selected], candidates, wDistance, wPrice);
}

/**
 * 在 rank 基础上加「直达票兜底」：若排序结果里没有任何直达票（全是联程票）且 minDirect>0，
 * 则把最优的 minDirect 条直达票（按同一权重公式排序）补到结果最前。
 * 直达 / 联程票通过下标区分：index < directCount 为直达（与调用方拼装候选时的下标布局一致）。
 */
export function rankWithMinDirect(
  candidates: Candidate[],
  directCount: number,
  maxByDistance: number,
  maxByPrice: number,
  wDistance: number,
  wPrice: number,
  minDirect: number,
): number[] {
  const order = rank(candidates, maxByDistance, maxByPrice, wDistance, wPrice);
  if (minDirect <= 0 || directCount <= 0) return order;
  // 结果里已有直达票则无需兜底
  if (order.some((idx) => idx < directCount)) return order;
  // 取最优的 minDirect 条直达票（按同一权重排序），补到最前
  const directIndices: number[] = [];
  for (let i = 0; i < directCount; i++) directIndices.push(i);
  const bestDirect = sortByWeight(directIndices, candidates, wDistance, wPrice);
  const take = Math.min(minDirect, bestDirect.length);
  return [...bestDirect.slice(0, take), ...order];
}

/** 把给定下标集合按归一化加权公式排序（范围取自该集合内 min/max，span 为 0 时归一化取 0 防除零）。 */
function sortByWeight(indices: number[], candidates: Candidate[], wDistance: number, wPrice: number): number[] {
  let minDist = Number.POSITIVE_INFINITY;
  let maxDist = Number.NEGATIVE_INFINITY;
  let minPrice = Number.POSITIVE_INFINITY;
  let maxPrice = Number.NEGATIVE_INFINITY;
  for (const idx of indices) {
    const c = candidates[idx];
    minDist = Math.min(minDist, c.distance);
    maxDist = Math.max(maxDist, c.distance);
    minPrice = Math.min(minPrice, c.price);
    maxPrice = Math.max(maxPrice, c.price);
  }
  const distSpan = maxDist - minDist;
  const priceSpan = maxPrice - minPrice;
  const weightOf = (idx: number): number => {
    const c = candidates[idx];
    const normDist = distSpan > 0 ? (c.distance - minDist) / distSpan : 0;
    const normPrice = priceSpan > 0 ? (c.price - minPrice) / priceSpan : 0;
    return wDistance * normDist + wPrice * normPrice;
  };
  return [...indices].sort((a, b) => weightOf(a) - weightOf(b));
}

/** 把已排序列表的前 limit 条的原始下标加入 target（limit<=0 表示全部）。 */
function addTop(target: Set<number>, sorted: Candidate[], limit: number): void {
  const count = limit > 0 ? Math.min(limit, sorted.length) : sorted.length;
  for (let i = 0; i < count; i++) target.add(sorted[i].index);
}
