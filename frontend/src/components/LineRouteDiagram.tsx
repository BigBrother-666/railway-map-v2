import { useLayoutEffect, useRef, useState, type CSSProperties, type RefObject } from 'react';
import { getConfig } from '../config';
import { useStore } from '../store/useStore';
import type { FeatureCollection, Line, LineStringProps, Train } from '../types';
import type { RouteGraph } from '../routing/graph';

type Point2 = { x: number; z: number };
const TRAIN_LANE_OFFSET_PX = 14;
const DIAGRAM_PAD = 18;
const FOLD_RADIUS_MAX = 26;
const RING_RADIUS_MAX = 46;
const LABEL_EDGE_MARGIN = 2;
const LABEL_FONT_FAMILY = "system-ui, -apple-system, 'Segoe UI', Roboto, 'PingFang SC', 'Microsoft YaHei', sans-serif";

interface AxisSegment {
  from: string;
  to: string;
  start: number;
  length: number;
  coords: Point2[];
}

interface StationMark {
  name: string;
  distance: number;
  progress: number;
}

interface LineAxis {
  world: string;
  total: number;
  ring: boolean;
  intervalCount: number;
  segments: AxisSegment[];
  segmentByEdge: Map<string, AxisSegment>;
  stations: StationMark[];
}

interface TrainMarker {
  train: Train;
  progress: number;
  distance: number;
  lane: number;
}

export function LineRouteDiagram({ line }: { line: Line }) {
  const geojson = useStore((s) => s.geojson);
  const graph = useStore((s) => s.graph);
  const trains = useStore((s) => s.trains);
  const focusTrain = useStore((s) => s.focusTrain);
  const stationAliases = useStore((s) => s.stationAliases);
  const cfg = getConfig().routeDiagram;
  const trainIcons = getConfig().trainIcons;
  const cardRef = useRef<HTMLDivElement>(null);
  const availableHeight = useAvailableDiagramHeight(cardRef);

  if (!geojson || !graph) return null;
  const axis = buildLineAxis(line, geojson, graph);
  if (!axis) return null;

  const markers = assignTrainLanes(
    [...trains.values()]
      .map((train) => locateTrainOnAxis(train, line.id, axis, cfg.projectionThresholdBlocks))
      .filter((m): m is TrainMarker => m != null),
    cfg.trainClusterProgress,
  );

  const trainIconScale = clamp(cfg.trainIconScale, 0.25, 2);
  const { folded, stationGap } = chooseLayout(line.ring, axis, availableHeight, cfg.minStationGapPx, cfg.maxStationGapPx);
  const enOf = (name: string) => stationAliases[name] || undefined;

  return (
    <div className="panel-section route-diagram-section">
      <div className="label">线路图</div>
      <div ref={cardRef} className={`route-diagram-card ${line.ring ? 'ring' : folded ? 'folded' : 'straight'}`}>
        {line.ring ? (
          <RingDiagram
            line={line}
            axis={axis}
            markers={markers}
            stationGap={stationGap}
            enOf={enOf}
            trainIconScale={trainIconScale}
            icons={trainIcons}
            onTrainClick={focusTrain}
          />
        ) : folded ? (
          <FoldedDiagram
            line={line}
            axis={axis}
            markers={markers}
            stationGap={stationGap}
            enOf={enOf}
            trainIconScale={trainIconScale}
            icons={trainIcons}
            onTrainClick={focusTrain}
          />
        ) : (
          <StraightDiagram
            line={line}
            axis={axis}
            markers={markers}
            stationGap={stationGap}
            enOf={enOf}
            trainIconScale={trainIconScale}
            icons={trainIcons}
            onTrainClick={focusTrain}
          />
        )}
        {markers.length === 0 && <div className="route-diagram-empty">当前线路无运行中列车</div>}
      </div>
    </div>
  );
}

function StraightDiagram({
  line,
  axis,
  markers,
  stationGap,
  trainIconScale,
  icons,
  onTrainClick,
  enOf,
}: DiagramProps) {
  const topPad = DIAGRAM_PAD;
  const straightWidth = 260;
  const xTrack = straightWidth / 2;
  const trackLength = Math.max(1, axis.intervalCount) * stationGap;
  const height = trackLength + topPad * 2;
  const yOf = (progress: number) => topPad + clamp(progress, 0, 1) * trackLength;

  return (
    <div className="route-diagram route-diagram-straight" style={{ width: straightWidth, height }}>
      <svg className="route-diagram-path-svg" viewBox={`0 0 ${straightWidth} ${height}`} aria-hidden="true">
        <line
          x1={xTrack}
          y1={topPad}
          x2={xTrack}
          y2={topPad + trackLength}
          stroke={line.color}
          strokeWidth="7"
          strokeLinecap="round"
        />
        <StationDots points={axis.stations.map((station) => ({ point: { x: xTrack, z: yOf(station.progress) }, key: station.name }))} />
        {axis.stations.map((station, i) => {
          const y = yOf(station.progress);
          return (
            <StationLabel
              key={`${station.name}-${i}`}
              name={station.name}
              nameEn={enOf(station.name)}
              x={xTrack - 13}
              y={y}
              anchor="end"
              maxWidth={xTrack - 13 - LABEL_EDGE_MARGIN}
            />
          );
        })}
      </svg>
      {markers.map((marker) => (
        <TrainIcon
          key={marker.train.trainId}
          marker={marker}
          src={marker.train.express ? icons.express : icons.normal}
          scale={trainIconScale}
          onClick={onTrainClick}
          style={{ left: xTrack, top: yOf(marker.progress) }}
        />
      ))}
    </div>
  );
}

function FoldedDiagram({
  line,
  axis,
  markers,
  stationGap,
  trainIconScale,
  icons,
  onTrainClick,
  enOf,
}: DiagramProps) {
  const stationCount = axis.stations.length;
  const intervals = Math.max(1, axis.intervalCount);
  const { leftIntervals, rightIntervals } = foldSplit(stationCount);
  const foldedWidth = 260;
  const topPad = DIAGRAM_PAD;
  const bottomPad = DIAGRAM_PAD;
  const xLeft = 100;
  const xRight = 160;
  const top = topPad;
  const radius = foldRadius(stationGap);
  const sideTop = top + radius;
  const leftBottom = sideTop + leftIntervals * stationGap;
  const rightBottom = sideTop + rightIntervals * stationGap;
  const height = Math.max(leftBottom, rightBottom) + bottomPad;
  const pointOf = (progress: number) =>
    foldedPoint(progress, intervals, leftIntervals, xLeft, xRight, top, sideTop, leftBottom, stationGap, radius);
  const path = `M ${xLeft} ${leftBottom} L ${xLeft} ${sideTop} Q ${xLeft} ${top} ${xLeft + radius} ${top} L ${xRight - radius} ${top} Q ${xRight} ${top} ${xRight} ${sideTop} L ${xRight} ${rightBottom}`;

  return (
    <div className="route-diagram route-diagram-folded" style={{ width: foldedWidth, height }}>
      <svg className="route-diagram-path-svg" viewBox={`0 0 ${foldedWidth} ${height}`} aria-hidden="true">
        <path d={path} fill="none" stroke={line.color} strokeWidth="7" strokeLinecap="round" strokeLinejoin="round" />
        <StationDots
          points={axis.stations.map((station) => {
            const p = pointOf(station.progress);
            return { point: p, key: station.name };
          })}
        />
        {axis.stations.map((station, i) => {
          const p = pointOf(station.progress);
          const onRight = p.x > (xLeft + xRight) / 2;
          return (
            <StationLabel
              key={`${station.name}-${i}`}
              name={station.name}
              nameEn={enOf(station.name)}
              x={p.x + (onRight ? 13 : -13)}
              y={p.z}
              anchor={onRight ? 'start' : 'end'}
              maxWidth={onRight ? foldedWidth - p.x - 13 - LABEL_EDGE_MARGIN : p.x - 13 - LABEL_EDGE_MARGIN}
            />
          );
        })}
      </svg>
      {markers.map((marker) => {
        const p = pointOf(marker.progress);
        return (
          <TrainIcon
            key={marker.train.trainId}
            marker={marker}
            src={marker.train.express ? icons.express : icons.normal}
            scale={trainIconScale}
            onClick={onTrainClick}
            style={{ left: p.x + marker.lane * TRAIN_LANE_OFFSET_PX, top: p.z }}
          />
        );
      })}
    </div>
  );
}

function RingDiagram({
  line,
  axis,
  markers,
  stationGap,
  trainIconScale,
  icons,
  onTrainClick,
  enOf,
}: DiagramProps) {
  const outerWidth = 276;
  const pad = DIAGRAM_PAD;
  const xLeft = 94;
  const xRight = outerWidth - 94;
  const split = ringSplit(axis.stations.length);
  const rows = Math.max(split.leftCount, split.rightCount);
  const r = Math.min(RING_RADIUS_MAX, (xRight - xLeft) / 2);
  const sideHeight = Math.max(1, (Math.max(2, rows) - 1) * stationGap);
  const trackHeight = sideHeight + r * 2;
  const outerHeight = trackHeight + pad * 2;
  const sideTop = pad + r;
  const sideBottom = pad + trackHeight - r;
  const pointOf = (progress: number) =>
    ringSidePoint(progress, axis.intervalCount, split.leftCount, xLeft, xRight, pad, pad + trackHeight, r, sideTop, sideBottom, stationGap);

  return (
    <div className="route-diagram route-diagram-ring" style={{ width: outerWidth, height: outerHeight }}>
      <svg className="route-diagram-path-svg" viewBox={`0 0 ${outerWidth} ${outerHeight}`} aria-hidden="true">
        <rect
          x={xLeft}
          y={pad}
          width={xRight - xLeft}
          height={trackHeight}
          rx={r}
          ry={r}
          fill="none"
          stroke={line.color}
          strokeWidth="7"
        />
        <StationDots
          points={axis.stations.map((station) => {
            const p = pointOf(station.progress);
            return { point: p, key: station.name };
          })}
        />
        {axis.stations.map((station, i) => {
          const p = pointOf(station.progress);
          const onRight = p.x > outerWidth / 2;
          return (
            <StationLabel
              key={`${station.name}-${i}`}
              name={station.name}
              nameEn={enOf(station.name)}
              x={p.x + (onRight ? 13 : -13)}
              y={p.z}
              anchor={onRight ? 'start' : 'end'}
              maxWidth={onRight ? outerWidth - p.x - 13 - LABEL_EDGE_MARGIN : p.x - 13 - LABEL_EDGE_MARGIN}
              ring
            />
          );
        })}
      </svg>
      {markers.map((marker) => {
        const p = pointOf(marker.progress);
        const side = p.x > outerWidth / 2 ? 1 : -1;
        return (
          <TrainIcon
            key={marker.train.trainId}
            marker={marker}
            src={marker.train.express ? icons.express : icons.normal}
            scale={trainIconScale}
            onClick={onTrainClick}
            style={{ left: p.x + marker.lane * TRAIN_LANE_OFFSET_PX * side, top: p.z }}
          />
        );
      })}
    </div>
  );
}

interface DiagramProps {
  line: Line;
  axis: LineAxis;
  markers: TrainMarker[];
  stationGap: number;
  trainIconScale: number;
  icons: { express: string; normal: string };
  onTrainClick: (id: string) => void;
  enOf: (name: string) => string | undefined;
}

/** 站名标签：中文名在上（超出 maxWidth 时自动换行）、英文名（如有）在下（仅一行，超出以"…"截断），整体以 y 垂直居中。 */
function StationLabel({
  name,
  nameEn,
  x,
  y,
  anchor,
  maxWidth,
  ring = false,
}: {
  name: string;
  nameEn?: string;
  x: number;
  y: number;
  anchor: 'start' | 'end';
  maxWidth: number;
  ring?: boolean;
}) {
  const className = `route-diagram-station-text${ring ? ' ring' : ''}`;
  const nameSize = ring ? 11 : 12;
  const lines = [
    ...wrapText(name, maxWidth, measurerFor(`600 ${nameSize}px ${LABEL_FONT_FAMILY}`, nameSize)).map((text) => ({
      text,
      en: false,
      height: nameSize + 2,
    })),
    ...(nameEn
      ? [{ text: truncateText(nameEn, maxWidth, measurerFor(`500 9.5px ${LABEL_FONT_FAMILY}`, 9.5)), en: true, height: 12 }]
      : []),
  ];
  const total = lines.reduce((sum, line) => sum + line.height, 0);
  let cursor = y - total / 2;

  return (
    <text className={className} textAnchor={anchor} dominantBaseline="middle">
      <title>{nameEn ? `${name}\n${nameEn}` : name}</title>
      {lines.map((line, i) => {
        const lineY = cursor + line.height / 2;
        cursor += line.height;
        return (
          <tspan
            key={i}
            className={line.en ? 'route-diagram-station-text-en' : undefined}
            lang={line.en ? 'en' : undefined}
            x={x}
            y={lineY}
          >
            {line.text}
          </tspan>
        );
      })}
    </text>
  );
}

type TextMeasurer = (text: string) => number;
const measurerCache = new Map<string, TextMeasurer>();
let measureCanvas: CanvasRenderingContext2D | null | undefined;

/** 优先用 canvas 精确测量文本宽度；无 canvas 环境时按字符估算（CJK 1em，其余约 0.6em）。 */
function measurerFor(font: string, fontSize: number): TextMeasurer {
  const cached = measurerCache.get(font);
  if (cached) return cached;
  if (measureCanvas === undefined) {
    measureCanvas = typeof document !== 'undefined' ? document.createElement('canvas').getContext('2d') : null;
  }
  const ctx = measureCanvas;
  const widths = new Map<string, number>();
  const measure: TextMeasurer = (text) => {
    let w = widths.get(text);
    if (w == null) {
      if (ctx) {
        ctx.font = font;
        w = ctx.measureText(text).width;
      } else {
        w = [...text].reduce((sum, ch) => sum + (isCjk(ch) ? 1 : 0.6) * fontSize, 0);
      }
      widths.set(text, w);
    }
    return w;
  };
  measurerCache.set(font, measure);
  return measure;
}

const CJK_RANGE = '\u2e80-\u9fff\uac00-\ud7af\uf900-\ufaff\uff00-\uffef';
const CJK_CHAR = new RegExp(`[${CJK_RANGE}]`);
// 断行单元：单个 CJK 字符 | 空白 | 非 CJK 单词（可带尾随连字符）| 单独连字符
const WRAP_TOKEN = new RegExp(`[${CJK_RANGE}]|\\s+|[^\\s${CJK_RANGE}-]+-?|-`, 'g');

function isCjk(ch: string): boolean {
  return CJK_CHAR.test(ch);
}

/** 单行截断：超出 maxWidth 时保留尽可能多的前缀并以"…"结尾（去掉截断处的尾随空白）。 */
export function truncateText(text: string, maxWidth: number, measure: TextMeasurer): string {
  if (!text || !(maxWidth > 0) || measure(text) <= maxWidth) return text;
  const chars = [...text];
  const fits = (n: number) => measure(chars.slice(0, n).join('').trimEnd() + '…') <= maxWidth;
  // 二分查找能放下的最长前缀
  let lo = 0;
  let hi = chars.length - 1;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (fits(mid)) lo = mid;
    else hi = mid - 1;
  }
  return chars.slice(0, lo).join('').trimEnd() + '…';
}

/**
 * 贪心换行：CJK 字符可在任意字间断开，其余文字按空格/连字符断词，
 * 单词本身超宽时再按字符拆分。每行至少保留一个字符。
 */
export function wrapText(text: string, maxWidth: number, measure: TextMeasurer): string[] {
  if (!text) return [];
  if (!(maxWidth > 0) || measure(text) <= maxWidth) return [text];

  const tokens = text.match(WRAP_TOKEN) ?? [text];
  const lines: string[] = [];
  let current = '';
  const push = () => {
    const trimmed = current.trim();
    if (trimmed) lines.push(trimmed);
    current = '';
  };

  for (const token of tokens) {
    if (/^\s+$/.test(token)) {
      if (current) current += token;
      continue;
    }
    if (measure(current + token) <= maxWidth) {
      current += token;
      continue;
    }
    if (current.trim()) push();
    if (measure(token) <= maxWidth) {
      current = token;
      continue;
    }
    // 超长单词按字符拆分
    for (const ch of token) {
      if (current && measure(current + ch) > maxWidth) push();
      current += ch;
    }
  }
  push();
  return lines.length > 0 ? lines : [text];
}

/**
 * 按可用高度选择布局与站间距：
 * - 环线：站间距按可用高度适配，限制在 [min, max]；
 * - 非环线：最小站间距直线展示仍放不下（会出现滚动条）则折叠为 n 形，
 *   折叠后的站间距同样按可用高度求最优值，限制在 [min, max]。
 * availableHeight 为 null（尚未测量）时按直线 + 最小间距渲染一帧。
 */
export function chooseLayout(
  ring: boolean,
  axis: LineAxis,
  availableHeight: number | null,
  minGapPx: number,
  maxGapPx: number,
): { folded: boolean; stationGap: number } {
  const minGap = clamp(Number.isFinite(minGapPx) ? minGapPx : 30, 20, 120);
  const maxGap = Math.max(minGap, Number.isFinite(maxGapPx) ? Math.min(maxGapPx, 200) : minGap);
  const fit = (space: number, intervals: number) =>
    availableHeight == null ? minGap : clamp(space / Math.max(1, intervals), minGap, maxGap);
  const avail = availableHeight ?? 0;

  if (ring) {
    const split = ringSplit(axis.stations.length);
    const rows = Math.max(2, split.leftCount, split.rightCount);
    return { folded: false, stationGap: fit(avail - DIAGRAM_PAD * 2 - RING_RADIUS_MAX * 2, rows - 1) };
  }

  const intervals = Math.max(1, axis.intervalCount);
  const straightMinHeight = intervals * minGap + DIAGRAM_PAD * 2;
  const canFold = axis.stations.length >= 3;
  if (availableHeight == null || straightMinHeight <= availableHeight || !canFold) {
    return { folded: false, stationGap: fit(avail - DIAGRAM_PAD * 2, intervals) };
  }

  const { leftIntervals, rightIntervals } = foldSplit(axis.stations.length);
  const sideIntervals = Math.max(1, leftIntervals, rightIntervals);
  // 站间距不小于 minGap(>=20) 时折叠圆角恒为 FOLD_RADIUS_MAX
  return { folded: true, stationGap: fit(avail - DIAGRAM_PAD * 2 - FOLD_RADIUS_MAX, sideIntervals) };
}

function foldSplit(stationCount: number) {
  const leftCount = Math.ceil(stationCount / 2);
  return {
    leftCount,
    leftIntervals: Math.max(0, leftCount - 1),
    rightIntervals: Math.max(0, stationCount - leftCount - 1),
  };
}

function foldRadius(stationGap: number): number {
  return Math.min(FOLD_RADIUS_MAX, stationGap * 1.6);
}

/**
 * 测量线路图在不出现滚动条的前提下可占用的高度（px）：
 * 从图形顶部（按滚动容器未滚动时计算）到滚动容器可视区域底部，扣除卡片内图形下方的内容与容器底部内边距。
 * 结果只取决于图形上方内容与容器尺寸，与图形自身高度无关，不会形成测量循环。
 */
function useAvailableDiagramHeight(cardRef: RefObject<HTMLDivElement>): number | null {
  const [height, setHeight] = useState<number | null>(null);
  const observed = useRef<{ el: Element | null; ro: ResizeObserver | null }>({ el: null, ro: null });
  const measureRef = useRef<() => void>(() => {});

  measureRef.current = () => {
    const card = cardRef.current;
    const diagram = card?.firstElementChild as HTMLElement | null;
    if (!card || !diagram) return;
    const scroller = findScrollContainer(card);
    if (!scroller) return;
    const next = Math.floor(measureAvailable(card, diagram, scroller));
    setHeight((prev) => (prev != null && Math.abs(prev - next) <= 1 ? prev : next));

    if (observed.current.el !== scroller) {
      observed.current.ro?.disconnect();
      const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => measureRef.current()) : null;
      ro?.observe(scroller);
      if (scroller.parentElement) ro?.observe(scroller.parentElement);
      observed.current = { el: scroller, ro };
    }
  };

  useLayoutEffect(() => measureRef.current());

  useLayoutEffect(() => {
    const onResize = () => measureRef.current();
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      observed.current.ro?.disconnect();
      observed.current = { el: null, ro: null };
    };
  }, []);

  return height;
}

function findScrollContainer(el: HTMLElement): HTMLElement | null {
  for (let cur = el.parentElement; cur; cur = cur.parentElement) {
    const overflowY = getComputedStyle(cur).overflowY;
    if (overflowY === 'auto' || overflowY === 'scroll') return cur;
  }
  return null;
}

function measureAvailable(card: HTMLElement, diagram: HTMLElement, scroller: HTMLElement): number {
  const px = (v: string) => parseFloat(v) || 0;
  const scrollerStyle = getComputedStyle(scroller);
  const viewBottom =
    scroller.getBoundingClientRect().bottom - px(scrollerStyle.borderBottomWidth) - px(scrollerStyle.paddingBottom);
  const diagramRect = diagram.getBoundingClientRect();
  const diagramTop = diagramRect.top + scroller.scrollTop;
  const section = card.parentElement;
  const sectionMargin = section ? px(getComputedStyle(section).marginBottom) : 0;
  const trailing = card.getBoundingClientRect().bottom - diagramRect.bottom + sectionMargin;
  return viewBottom - diagramTop - trailing;
}

function StationDots({ points }: { points: { point: Point2; key: string }[] }) {
  return (
    <>
      {points.map(({ point, key }, index) => (
        <circle
          key={`${key}-${index}`}
          className="route-diagram-dot-svg"
          cx={point.x}
          cy={point.z}
          r="8"
        />
      ))}
    </>
  );
}

function TrainIcon({
  marker,
  src,
  scale,
  onClick,
  style,
}: {
  marker: TrainMarker;
  src: string;
  scale: number;
  onClick: (id: string) => void;
  style: CSSProperties;
}) {
  const label = marker.train.trainName && marker.train.trainName !== 'N/A'
    ? marker.train.trainName
    : marker.train.trainId;
  return (
    <img
      className="route-diagram-train"
      src={src}
      alt=""
      title={label}
      role="button"
      tabIndex={0}
      onClick={() => onClick(marker.train.trainId)}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          onClick(marker.train.trainId);
        }
      }}
      style={{
        ...style,
        transform: `translate(-50%, -50%) rotate(${Number(marker.train.head.yaw ?? 0)}deg) scale(${scale})`,
      }}
    />
  );
}

function buildLineAxis(line: Line, fc: FeatureCollection, graph: RouteGraph): LineAxis | null {
  const featureIndex = buildFeatureIndex(line.id, fc, graph);
  if (featureIndex.features.length === 0) return null;

  const world = featureIndex.features[0].props.world;
  const rawStations = normalizeStationSequence(line);
  if (rawStations.length >= 2) {
    const ordered = buildStationOrderedAxis(line, graph, featureIndex, rawStations, world);
    if (ordered) return ordered;
  }

  return buildFeatureFallbackAxis(line, featureIndex, world);
}

function buildFeatureIndex(lineId: string, fc: FeatureCollection, graph: RouteGraph) {
  const byEdge = new Map<string, { props: LineStringProps; coords: Point2[] }>();
  const features: { props: LineStringProps; coords: Point2[] }[] = [];

  for (const feature of fc.features) {
    if (feature.geometry?.type !== 'LineString') continue;
    const props = feature.properties as LineStringProps;
    if (props.lineId !== lineId) continue;
    if (graph.isMainlineBypassSegment(props.from, props.to, props.lineId)) continue;
    const coords = ((feature.geometry as GeoJSON.LineString).coordinates as number[][])
      .map((coord) => ({ x: coord[0], z: coord[1] }))
      .filter((coord) => Number.isFinite(coord.x) && Number.isFinite(coord.z));
    if (coords.length < 2) continue;
    const item = { props, coords };
    features.push(item);
    byEdge.set(edgeKey(props.from, props.to), item);
  }

  return { byEdge, features };
}

function buildStationOrderedAxis(
  line: Line,
  graph: RouteGraph,
  featureIndex: ReturnType<typeof buildFeatureIndex>,
  stationNames: string[],
  world: string,
): LineAxis | null {
  const segments: AxisSegment[] = [];
  const segmentByEdge = new Map<string, AxisSegment>();
  const stationDistances: { name: string; distance: number }[] = [{ name: stationNames[0], distance: 0 }];
  let total = 0;

  for (let i = 0; i < stationNames.length - 1; i++) {
    const path = findLinePathBetweenStations(graph, line.id, stationNames[i], stationNames[i + 1]);
    if (!path || path.length < 2) return null;

    for (let j = 0; j < path.length - 1; j++) {
      const segment = buildAxisSegment(path[j], path[j + 1], total, graph, featureIndex);
      if (!segment) return null;
      segments.push(segment);
      segmentByEdge.set(edgeKey(segment.from, segment.to), segment);
      segmentByEdge.set(edgeKey(segment.to, segment.from), segment);
      total += segment.length;
    }
    stationDistances.push({ name: stationNames[i + 1], distance: total });
  }

  if (total <= 0 || segments.length === 0) return null;
  const visibleStations = stationDistances.filter(
    (station, index) => !(line.ring && index === stationDistances.length - 1 && station.name === stationDistances[0].name),
  );
  const intervalCount = Math.max(1, line.ring ? visibleStations.length : visibleStations.length - 1);
  const stations = visibleStations.map((station, index) => ({
    name: station.name,
    distance: station.distance,
    progress: index / intervalCount,
  }));

  return { world, total, ring: line.ring, intervalCount, segments, segmentByEdge, stations };
}

function buildFeatureFallbackAxis(
  line: Line,
  featureIndex: ReturnType<typeof buildFeatureIndex>,
  world: string,
): LineAxis | null {
  const segments: AxisSegment[] = [];
  const segmentByEdge = new Map<string, AxisSegment>();
  let total = 0;

  for (const feature of featureIndex.features) {
    const length = feature.props.length > 0 ? feature.props.length : polylineLength(feature.coords);
    const segment: AxisSegment = {
      from: feature.props.from,
      to: feature.props.to,
      start: total,
      length,
      coords: feature.coords,
    };
    segments.push(segment);
    segmentByEdge.set(edgeKey(segment.from, segment.to), segment);
    segmentByEdge.set(edgeKey(segment.to, segment.from), segment);
    total += length;
  }

  if (total <= 0 || segments.length === 0) return null;
  const stationNames = normalizeStationSequence(line).filter(
    (name, index, arr) => !(line.ring && index === arr.length - 1 && name === arr[0]),
  );
  const intervalCount = Math.max(1, line.ring ? stationNames.length : stationNames.length - 1);
  const stations = stationNames.map((name, index) => ({
    name,
    distance: (index / intervalCount) * total,
    progress: index / intervalCount,
  }));

  return { world, total, ring: line.ring, intervalCount, segments, segmentByEdge, stations };
}

function buildAxisSegment(
  from: string,
  to: string,
  start: number,
  graph: RouteGraph,
  featureIndex: ReturnType<typeof buildFeatureIndex>,
): AxisSegment | null {
  const direct = featureIndex.byEdge.get(edgeKey(from, to));
  const reverse = featureIndex.byEdge.get(edgeKey(to, from));
  const feature = direct ?? reverse;

  if (feature) {
    const coords = direct ? feature.coords : [...feature.coords].reverse();
    return {
      from,
      to,
      start,
      length: feature.props.length > 0 ? feature.props.length : polylineLength(coords),
      coords,
    };
  }

  const fromNode = graph.nodes.get(from);
  const toNode = graph.nodes.get(to);
  if (!fromNode || !toNode) return null;
  const coords = [{ x: fromNode.x, z: fromNode.z }, { x: toNode.x, z: toNode.z }];
  const link = graph.links(from).find((candidate) => candidate.to === to);
  return {
    from,
    to,
    start,
    length: link?.distance ?? polylineLength(coords),
    coords,
  };
}

function findLinePathBetweenStations(
  graph: RouteGraph,
  lineId: string,
  startName: string,
  endName: string,
): string[] | null {
  const starts = graph.stationNodes(startName).filter((id) => graph.nodes.get(id)?.lineIds.has(lineId));
  const targets = new Set(graph.stationNodes(endName).filter((id) => graph.nodes.get(id)?.lineIds.has(lineId)));
  if (starts.length === 0 || targets.size === 0) return null;

  const dist = new Map<string, number>();
  const prev = new Map<string, string | null>();
  const queue: { id: string; distance: number }[] = [];

  for (const start of starts) {
    dist.set(start, 0);
    prev.set(start, null);
    queue.push({ id: start, distance: 0 });
  }

  while (queue.length > 0) {
    let best = 0;
    for (let i = 1; i < queue.length; i++) if (queue[i].distance < queue[best].distance) best = i;
    const current = queue.splice(best, 1)[0];
    if (current.distance > (dist.get(current.id) ?? Infinity)) continue;
    if (targets.has(current.id)) return reconstructPath(current.id, prev);

    for (const link of graph.links(current.id)) {
      if (link.lineId !== lineId) continue;
      if (graph.isMainlineBypassSegment(link.from, link.to, link.lineId)) continue;
      const nextDistance = current.distance + link.distance;
      if (nextDistance < (dist.get(link.to) ?? Infinity)) {
        dist.set(link.to, nextDistance);
        prev.set(link.to, current.id);
        queue.push({ id: link.to, distance: nextDistance });
      }
    }
  }

  return null;
}

function reconstructPath(end: string, prev: Map<string, string | null>): string[] {
  const path: string[] = [];
  let current: string | null | undefined = end;
  while (current) {
    path.push(current);
    current = prev.get(current);
  }
  return path.reverse();
}

function locateTrainOnAxis(
  train: Train,
  lineId: string,
  axis: LineAxis,
  threshold: number,
): TrainMarker | null {
  if (train.world !== axis.world || !train.head) return null;

  const routeSegments = segmentsFromRoute(train.routeNodeIds, axis);
  const hasRouteMatch = routeSegments.length > 0;
  const hasLineMatch = train.lineId === lineId;
  if (!hasRouteMatch && !hasLineMatch) return null;

  const point = { x: train.head.x, z: train.head.z };
  const candidates = hasRouteMatch ? routeSegments : axis.segments;
  const projection = bestProjection(point, candidates);
  if (!projection || projection.distance > threshold) return null;

  return {
    train,
    progress: distanceToStationProgress(axis, projection.segment.start + projection.along),
    distance: projection.distance,
    lane: 0,
  };
}

function distanceToStationProgress(axis: LineAxis, distance: number): number {
  const stations = axis.stations;
  if (stations.length < 2 || axis.total <= 0) return clamp(distance / Math.max(1, axis.total), 0, 1);

  const d = clamp(distance, 0, axis.total);
  if (!axis.ring && d <= stations[0].distance) return 0;

  for (let i = 0; i < stations.length - 1; i++) {
    const a = stations[i].distance;
    const b = stations[i + 1].distance;
    if (d >= a && d <= b) {
      const local = b > a ? (d - a) / (b - a) : 0;
      return clamp((i + local) / axis.intervalCount, 0, 1);
    }
  }

  if (axis.ring) {
    const last = stations[stations.length - 1];
    if (d >= last.distance) {
      const local = axis.total > last.distance ? (d - last.distance) / (axis.total - last.distance) : 0;
      return clamp((stations.length - 1 + local) / axis.intervalCount, 0, 1);
    }
  }

  return 1;
}

function segmentsFromRoute(routeNodeIds: string[] | undefined, axis: LineAxis): AxisSegment[] {
  if (!routeNodeIds || routeNodeIds.length < 2) return [];
  const segments: AxisSegment[] = [];
  const seen = new Set<AxisSegment>();
  for (let i = 0; i < routeNodeIds.length - 1; i++) {
    const segment = axis.segmentByEdge.get(edgeKey(routeNodeIds[i], routeNodeIds[i + 1]));
    if (segment && !seen.has(segment)) {
      seen.add(segment);
      segments.push(segment);
    }
  }
  return segments;
}

function bestProjection(point: Point2, segments: AxisSegment[]) {
  let best: { segment: AxisSegment; along: number; distance: number } | null = null;
  for (const segment of segments) {
    const projection = projectPointToAxisSegment(point, segment);
    if (!best || projection.distance < best.distance) best = { segment, ...projection };
  }
  return best;
}

function projectPointToAxisSegment(point: Point2, segment: AxisSegment) {
  const geomLength = polylineLength(segment.coords);
  let best = { along: 0, distance: Infinity };
  let walked = 0;

  for (let i = 0; i < segment.coords.length - 1; i++) {
    const a = segment.coords[i];
    const b = segment.coords[i + 1];
    const dx = b.x - a.x;
    const dz = b.z - a.z;
    const partLengthSq = dx * dx + dz * dz;
    if (partLengthSq <= 0) continue;
    const t = clamp(((point.x - a.x) * dx + (point.z - a.z) * dz) / partLengthSq, 0, 1);
    const px = a.x + dx * t;
    const pz = a.z + dz * t;
    const distance = Math.hypot(point.x - px, point.z - pz);
    const partLength = Math.sqrt(partLengthSq);
    const alongGeom = walked + partLength * t;
    const along = geomLength > 0 ? (alongGeom / geomLength) * segment.length : 0;
    if (distance < best.distance) best = { along, distance };
    walked += partLength;
  }

  return best;
}

function assignTrainLanes(markers: TrainMarker[], clusterProgress: number): TrainMarker[] {
  const sorted = [...markers].sort((a, b) => a.progress - b.progress);
  const result: TrainMarker[] = [];
  let cluster: TrainMarker[] = [];
  const threshold = Math.max(0.005, clusterProgress);
  const flush = () => {
    const lanes = [0, -1, 1, -2, 2];
    cluster.forEach((marker, index) => result.push({ ...marker, lane: lanes[index % lanes.length] }));
    cluster = [];
  };

  for (const marker of sorted) {
    if (cluster.length === 0 || Math.abs(marker.progress - cluster[cluster.length - 1].progress) <= threshold) {
      cluster.push(marker);
    } else {
      flush();
      cluster.push(marker);
    }
  }
  flush();
  return result;
}

function normalizeStationSequence(line: Line): string[] {
  const stations = line.stations.filter(Boolean);
  if (line.ring && stations.length > 1 && stations[0] !== stations[stations.length - 1]) {
    return [...stations, stations[0]];
  }
  return stations;
}

function foldedPoint(
  progress: number,
  intervals: number,
  leftIntervals: number,
  xLeft: number,
  xRight: number,
  top: number,
  sideTop: number,
  leftBottom: number,
  stationGap: number,
  radius: number,
): Point2 {
  const step = clamp(progress, 0, 1) * intervals;
  if (step <= leftIntervals) {
    return { x: xLeft, z: leftBottom - step * stationGap };
  }
  if (step <= leftIntervals + 1) {
    const local = step - leftIntervals;
    return roundedRectHorizontalPoint(1 - local, xLeft, xRight, top, sideTop, radius, false);
  }
  return { x: xRight, z: sideTop + (step - leftIntervals - 1) * stationGap };
}

function ringSplit(stationCount: number): { leftCount: number; rightCount: number } {
  const leftCount = Math.ceil(stationCount / 2);
  return { leftCount, rightCount: Math.max(0, stationCount - leftCount) };
}

function ringSidePoint(
  progress: number,
  intervalCount: number,
  leftCount: number,
  xLeft: number,
  xRight: number,
  top: number,
  bottom: number,
  radius: number,
  sideTop: number,
  sideBottom: number,
  stationGap: number,
): Point2 {
  const intervals = Math.max(1, intervalCount);
  const rightCount = Math.max(0, intervals - leftCount);
  const leftIntervals = Math.max(0, leftCount - 1);
  const rightIntervals = Math.max(0, rightCount - 1);
  const step = clamp(progress, 0, 1) * intervals;
  if (step <= leftIntervals) {
    return { x: xLeft, z: sideTop + step * stationGap };
  }
  if (step <= leftIntervals + 1) {
    const local = step - leftIntervals;
    return roundedRectHorizontalPoint(local, xLeft, xRight, bottom, sideBottom, radius, true);
  }
  if (step <= leftIntervals + 1 + rightIntervals) {
    return { x: xRight, z: sideBottom - (step - leftIntervals - 1) * stationGap };
  }
  const local = step - leftIntervals - 1 - rightIntervals;
  return roundedRectHorizontalPoint(local, xLeft, xRight, top, sideTop, radius, false);
}

function roundedRectHorizontalPoint(
  local: number,
  xLeft: number,
  xRight: number,
  edgeY: number,
  sideY: number,
  radius: number,
  bottom: boolean,
): Point2 {
  const r = Math.max(0, radius);
  const horizontal = Math.max(0, xRight - xLeft - r * 2);
  const arc = (Math.PI * r) / 2;
  const total = arc * 2 + horizontal;
  let d = clamp(local, 0, 1) * total;

  if (d <= arc) {
    const t = arc > 0 ? d / arc : 1;
    const start = bottom ? Math.PI : 0;
    const end = bottom ? Math.PI / 2 : -Math.PI / 2;
    return arcPoint(bottom ? xLeft + r : xRight - r, sideY, r, start, end, t);
  }
  d -= arc;
  if (d <= horizontal) {
    const x = bottom ? xLeft + r + d : xRight - r - d;
    return { x, z: edgeY };
  }
  d -= horizontal;
  const t = arc > 0 ? d / arc : 1;
  const start = bottom ? Math.PI / 2 : -Math.PI / 2;
  const end = bottom ? 0 : -Math.PI;
  return arcPoint(bottom ? xRight - r : xLeft + r, sideY, r, start, end, t);
}

function arcPoint(cx: number, cy: number, radius: number, start: number, end: number, t: number): Point2 {
  const angle = start + (end - start) * t;
  return { x: cx + Math.cos(angle) * radius, z: cy + Math.sin(angle) * radius };
}

function polylineLength(coords: Point2[]): number {
  let total = 0;
  for (let i = 0; i < coords.length - 1; i++) {
    total += Math.hypot(coords[i + 1].x - coords[i].x, coords[i + 1].z - coords[i].z);
  }
  return total;
}

function edgeKey(from: string, to: string): string {
  return `${from}__${to}`;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}
