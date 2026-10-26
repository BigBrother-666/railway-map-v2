import { useId, useMemo, useRef } from 'react';
import { getConfig } from '../config';
import { useStore } from '../store/useStore';
import type { FeatureCollection, LineStringProps, Train } from '../types';
import type { RouteGraph } from '../routing/graph';
import { resolveRouteEdges } from '../routing/routeEdges';
import {
  DIAGRAM_PAD,
  LABEL_EDGE_MARGIN,
  StationDots,
  StationLabel,
  TrainIcon,
  clamp,
  edgeKey,
  polylineLength,
  projectPointToAxisSegment,
  useAvailableDiagramHeight,
  type AxisSegment,
  type Point2,
} from './LineRouteDiagram';

const DIAGRAM_WIDTH = 260;
const STATION_LABEL_GAP = 13;
/** 中途站标签（单行 11px）占用高度，相邻标签间距小于该值时省略后者的站名（保留刻度点）。 */
const MID_LABEL_HEIGHT = 15;
/** 始发 / 终到站标签（中文 + 英文两行）半高。 */
const END_LABEL_HALF_HEIGHT = 13;

interface ExpressSegment extends AxisSegment {
  color: string | null;
}

/** 路线上某种颜色占据的比例区间 [from, to]（0~1）。 */
export interface ColorSpan {
  color: string;
  from: number;
  to: number;
}

export interface GradientStop {
  offset: number;
  color: string;
}

export interface ExpressAxis {
  world: string;
  total: number;
  segments: ExpressSegment[];
  spans: ColorSpan[];
  /** 中途车站（不含始发 / 终到）及其位置（0~1）。 */
  midStations: { name: string; progress: number }[];
}

/** 快速车路线图：一条竖线，两端为始发 / 终到站，颜色按经过线路的长度比例渐变，列车圆点沿线移动。 */
export function ExpressRouteDiagram({ train }: { train: Train }) {
  const geojson = useStore((s) => s.geojson);
  const graph = useStore((s) => s.graph);
  const focusTrain = useStore((s) => s.focusTrain);
  const stationAliases = useStore((s) => s.stationAliases);
  const cfg = getConfig().routeDiagram;
  const trainIcons = getConfig().trainIcons;
  const cardRef = useRef<HTMLDivElement>(null);
  const availableHeight = useAvailableDiagramHeight(cardRef);
  const gradientId = `express-gradient-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`;

  // 依赖路线内容而非整个 train 对象，避免位置刷新时反复重建路线轴。
  const routeKey = train.routeNodeIds && train.routeNodeIds.length > 1 ? train.routeNodeIds.join(',') : '';
  const lineKey = train.routeLineIds?.join(',') ?? '';
  const axis = useMemo(
    () =>
      routeKey && geojson && graph
        ? buildExpressAxis(routeKey.split(','), geojson, graph, lineKey ? lineKey.split(',') : undefined)
        : null,
    [routeKey, lineKey, geojson, graph],
  );
  if (!axis || !graph) return null;

  const route = train.routeNodeIds ?? [];
  const startStation = graph.firstStationName(route);
  const endStation = graph.firstStationName(route, true);
  const enOf = (name: string) => stationAliases[name] || undefined;

  const minHeight = Math.max(80, Number.isFinite(cfg.expressMinHeightPx) ? cfg.expressMinHeightPx : 240);
  const height = Math.max(minHeight, availableHeight ?? minHeight);
  const top = DIAGRAM_PAD;
  const trackLength = height - DIAGRAM_PAD * 2;
  const xTrack = DIAGRAM_WIDTH / 2;
  const yOf = (progress: number) => top + clamp(progress, 0, 1) * trackLength;
  const trackWidth = clamp(cfg.trackWidthPx, 1, 20);
  const trainIconScale = clamp(cfg.trainIconScale, 0.25, 2);
  const stops = buildGradientStops(axis.spans, clamp(cfg.expressGradientBlendRatio, 0, 1));
  const progress = locateTrainOnExpressAxis(train, axis, cfg.projectionThresholdBlocks);
  const labelX = xTrack - STATION_LABEL_GAP;
  const labelMaxWidth = labelX - LABEL_EDGE_MARGIN;

  return (
    <div className="panel-section route-diagram-section">
      <div className="label">路线图</div>
      <div ref={cardRef} className="route-diagram-card straight">
        <div className="route-diagram route-diagram-straight" style={{ width: DIAGRAM_WIDTH, height }}>
          <svg className="route-diagram-path-svg" viewBox={`0 0 ${DIAGRAM_WIDTH} ${height}`} aria-hidden="true">
            <defs>
              {/* 竖线包围盒宽度为 0，必须用 userSpaceOnUse，否则 objectBoundingBox 渐变不生效 */}
              <linearGradient id={gradientId} gradientUnits="userSpaceOnUse" x1={xTrack} y1={top} x2={xTrack} y2={top + trackLength}>
                {stops.map((stop, i) => (
                  <stop key={i} offset={stop.offset} stopColor={stop.color} />
                ))}
              </linearGradient>
            </defs>
            <line
              x1={xTrack}
              y1={top}
              x2={xTrack}
              y2={top + trackLength}
              stroke={stops.length > 0 ? `url(#${gradientId})` : '#8a90a0'}
              strokeWidth={trackWidth}
              strokeLinecap="round"
            />
            {axis.midStations.map((station, i) => (
              <circle
                key={`${station.name}-${i}`}
                className="route-diagram-tick-svg"
                cx={xTrack}
                cy={yOf(station.progress)}
                r={Math.max(2, trackWidth * 0.4)}
              />
            ))}
            {pickMidLabels(axis.midStations.map((s) => yOf(s.progress)), yOf(0), yOf(1)).map((i) => {
              const station = axis.midStations[i];
              return (
                <StationLabel
                  key={`${station.name}-${i}`}
                  name={station.name}
                  x={labelX}
                  y={yOf(station.progress)}
                  anchor="end"
                  maxWidth={labelMaxWidth}
                  muted
                />
              );
            })}
            <StationDots
              points={[
                { point: { x: xTrack, z: yOf(0) }, key: 'start' },
                { point: { x: xTrack, z: yOf(1) }, key: 'end' },
              ]}
            />
            {startStation && (
              <StationLabel name={startStation} nameEn={enOf(startStation)} x={labelX} y={yOf(0)} anchor="end" maxWidth={labelMaxWidth} />
            )}
            {endStation && (
              <StationLabel name={endStation} nameEn={enOf(endStation)} x={labelX} y={yOf(1)} anchor="end" maxWidth={labelMaxWidth} />
            )}
          </svg>
          {progress != null && (
            <TrainIcon
              marker={{ train, progress, distance: 0, lane: 0 }}
              src={trainIcons.express}
              scale={trainIconScale}
              onClick={focusTrain}
              style={{ left: xTrack, top: yOf(progress) }}
            />
          )}
        </div>
        {progress == null && <div className="route-diagram-empty">列车当前不在路线上</div>}
      </div>
    </div>
  );
}

/**
 * 按快速车 routeNodeIds 逐段构建路线轴：段长取 geojson length（缺省按几何长度），颜色取轨道段 color（加载时已按线路配置覆盖，含联络线）。
 * geojson 缺失某段时以两端节点直线兜底，颜色沿用相邻段。
 * 共线段（同一 from→to 有多条线路）优先按 lineHints（逐段 lineId）选边，无提示时按连续性推断（resolveRouteEdges）。
 */
export function buildExpressAxis(
  nodeIds: string[],
  fc: FeatureCollection,
  graph: RouteGraph,
  lineHints?: readonly string[],
): ExpressAxis | null {
  type EdgeFeature = { lineId: string; props: LineStringProps; coords: Point2[] };
  // 正向区间优先；仅反向存在时用反向几何兜底
  const forward = new Map<string, EdgeFeature[]>();
  const reverse = new Map<string, EdgeFeature[]>();
  const push = (m: Map<string, EdgeFeature[]>, key: string, f: EdgeFeature) => {
    const arr = m.get(key) ?? [];
    if (!arr.some((e) => e.lineId === f.lineId)) arr.push(f);
    m.set(key, arr);
  };
  for (const feature of fc.features) {
    if (feature.geometry?.type !== 'LineString') continue;
    const props = feature.properties as LineStringProps;
    if (!props?.from || !props?.to) continue;
    const coords = ((feature.geometry as GeoJSON.LineString).coordinates as number[][])
      .map((c) => ({ x: c[0], z: c[1] }))
      .filter((c) => Number.isFinite(c.x) && Number.isFinite(c.z));
    if (coords.length < 2) continue;
    push(forward, edgeKey(props.from, props.to), { lineId: props.lineId, props, coords });
    push(reverse, edgeKey(props.to, props.from), { lineId: props.lineId, props, coords: [...coords].reverse() });
  }
  const picked = resolveRouteEdges(nodeIds, (from, to) => {
    const key = edgeKey(from, to);
    return forward.get(key) ?? reverse.get(key) ?? [];
  }, { lineHints });

  const segments: ExpressSegment[] = [];
  const stationDistances: { name: string; distance: number }[] = [];
  let world: string | null = null;
  let total = 0;

  const pushName = (name: string, distance: number) => {
    // 同一车站的多个站台节点只记一次
    if (stationDistances[stationDistances.length - 1]?.name === name) return;
    stationDistances.push({ name, distance });
  };
  const pushStation = (nodeId: string) => {
    const node = graph.nodes.get(nodeId);
    if (node?.type === 'station' && node.name) pushName(node.name, total);
  };

  pushStation(nodeIds[0]);
  for (let i = 0; i < nodeIds.length - 1; i++) {
    const from = nodeIds[i];
    const to = nodeIds[i + 1];
    const feature = picked[i];
    let coords: Point2[];
    let color: string | null = null;
    let length: number;
    // 正线（track=main）跨站直通，不经过车站节点：由进站道岔沿停靠线找出所跨车站，记在本段中点
    let bypassStation: string | null = null;
    if (feature) {
      bypassStation = graph.mainlineBypassStationName(feature.props.from, feature.props.to, feature.props.lineId);
      coords = feature.coords;
      color = feature.props.color || null;
      world ??= feature.props.world;
      length = feature.props.length > 0 ? feature.props.length : polylineLength(coords);
    } else {
      const a = graph.nodes.get(from);
      const b = graph.nodes.get(to);
      if (!a || !b) continue;
      coords = [{ x: a.x, z: a.z }, { x: b.x, z: b.z }];
      length = polylineLength(coords);
    }
    if (!(length > 0)) continue;
    segments.push({ from, to, start: total, length, coords, color });
    if (bypassStation) pushName(bypassStation, total + length / 2);
    total += length;
    pushStation(to);
  }

  if (!world || total <= 0 || segments.length === 0) return null;

  // 中途站刻度：去掉首尾车站（已在两端显示）
  const midStations = stationDistances
    .slice(1, -1)
    .map((s) => ({ name: s.name, progress: s.distance / total }))
    .filter((s) => s.progress > 0 && s.progress < 1);
  return { world, total, segments, spans: buildColorSpans(segments, total), midStations };
}

/**
 * 选出可显示站名的中途站下标：与上一个已显示标签（含始发站）及终到站标签不重叠的才显示，
 * 站点密集时省略部分站名，刻度点仍全部保留。
 */
export function pickMidLabels(ys: number[], startY: number, endY: number): number[] {
  const picked: number[] = [];
  let lastBottom = startY + END_LABEL_HALF_HEIGHT;
  const endTop = endY - END_LABEL_HALF_HEIGHT;
  ys.forEach((y, i) => {
    const half = MID_LABEL_HEIGHT / 2;
    if (y - half >= lastBottom && y + half <= endTop) {
      picked.push(i);
      lastBottom = y + half;
    }
  });
  return picked;
}

/** 把逐段颜色按长度比例合并为连续色区间；无颜色的段并入前一区间（开头的并入后一区间）。 */
export function buildColorSpans(segments: { start: number; length: number; color: string | null }[], total: number): ColorSpan[] {
  const spans: ColorSpan[] = [];
  let pendingFrom: number | null = null; // 开头无颜色段的起点
  for (const segment of segments) {
    const from = segment.start / total;
    const to = (segment.start + segment.length) / total;
    const last = spans[spans.length - 1];
    if (!segment.color) {
      if (last) last.to = to;
      else pendingFrom ??= from;
      continue;
    }
    const color = segment.color.toLowerCase();
    if (last && last.color === color) {
      last.to = to;
    } else {
      spans.push({ color, from: pendingFrom ?? from, to });
      pendingFrom = null;
    }
  }
  if (spans.length > 0) {
    spans[0].from = 0;
    spans[spans.length - 1].to = 1;
  }
  return spans;
}

/**
 * 生成竖线渐变 stop：区间内纯色，相邻两色交界处留一段过渡带线性插值。
 * 过渡带长度 = blendRatio（占总长比例），且不超过相邻两色各自长度的一半，避免短区间被吞没。
 */
export function buildGradientStops(spans: ColorSpan[], blendRatio: number): GradientStop[] {
  if (spans.length === 0) return [];
  const stops: GradientStop[] = [{ offset: 0, color: spans[0].color }];
  for (let i = 0; i < spans.length - 1; i++) {
    const a = spans[i];
    const b = spans[i + 1];
    const width = Math.max(0, Math.min(blendRatio, (a.to - a.from) / 2, (b.to - b.from) / 2));
    stops.push({ offset: a.to - width / 2, color: a.color });
    stops.push({ offset: a.to + width / 2, color: b.color });
  }
  stops.push({ offset: 1, color: spans[spans.length - 1].color });
  return stops;
}

/** 把列车车头投影到路线上，返回 0~1 进度；不在同一世界或偏离超过阈值时返回 null（隐藏圆点）。 */
export function locateTrainOnExpressAxis(train: Train, axis: ExpressAxis, threshold: number): number | null {
  if (train.world !== axis.world || !train.head) return null;
  const point = { x: train.head.x, z: train.head.z };
  let best: { distance: number; along: number } | null = null;
  for (const segment of axis.segments) {
    const projection = projectPointToAxisSegment(point, segment);
    if (!best || projection.distance < best.distance) {
      best = { distance: projection.distance, along: segment.start + projection.along };
    }
  }
  if (!best || best.distance > threshold) return null;
  return clamp(best.along / axis.total, 0, 1);
}
