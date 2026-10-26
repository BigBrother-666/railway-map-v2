import { useEffect, useRef } from 'react';
import 'maplibre-gl/dist/maplibre-gl.css';
import { MapController } from '../map/MapController';
import { useStore } from '../store/useStore';
import type { ResolvedTheme } from '../theme';
import { getConfig } from '../config';

/** 左侧侧边栏宽度（与 styles.css 的 .sidebar 保持一致），用于扣除地图遮挡区域。 */
const LEFT_SIDEBAR_WIDTH = 320;
/** 手机端断点与底部抽屉高度占比（与 styles.css 的 @media (max-width: 640px) .sidebar-left 保持一致）。 */
const MOBILE_QUERY = '(max-width: 640px)';
const MOBILE_DRAWER_RATIO = 0.55;

/** 地图视图：把 MapController 与全局状态连起来。 */
export function MapView({ theme }: { theme: ResolvedTheme }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const ctrlRef = useRef<MapController | null>(null);
  const readyRef = useRef(false);

  const geojson = useStore((s) => s.geojson);
  const currentWorld = useStore((s) => s.currentWorld);
  const hiddenLines = useStore((s) => s.hiddenLines);
  const candidates = useStore((s) => s.candidates);
  const selectedRouteIndex = useStore((s) => s.selectedRouteIndex);
  const trains = useStore((s) => s.trains);
  const sidebar = useStore((s) => s.sidebar);
  const sidebarCollapsed = useStore((s) => s.sidebarCollapsed);
  const highlightLineId = useStore((s) => s.highlightLineId);
  const selectedTrainId = useStore((s) => s.selectedTrainId);
  const trainFocusMode = useStore((s) => s.trainFocusMode);
  const trainFocusSeq = useStore((s) => s.trainFocusSeq);
  const trackingTrainId = useStore((s) => s.trackingTrainId);
  const lineFocusSeq = useStore((s) => s.lineFocusSeq);
  const stationFocus = useStore((s) => s.stationFocus);
  const clickStation = useStore((s) => s.clickStation);
  const selectTrain = useStore((s) => s.selectTrain);

  // 初始化地图（一次）
  useEffect(() => {
    if (!containerRef.current) return;
    const ctrl = new MapController(containerRef.current, theme);
    ctrlRef.current = ctrl;
    ctrl.setHandlers(
      (name) => useStore.getState().clickStation(name),
      (id) => useStore.getState().selectTrain(id),
      (lineId) => useStore.getState().clickLine(lineId),
    );
    ctrl.onReady(() => {
      readyRef.current = true;
      const st = useStore.getState();
      if (st.geojson) ctrl.setData(st.geojson, st.currentWorld);
      ctrl.setHiddenLines(st.hiddenLines);
    });
    return () => {
      ctrl.destroy();
      ctrlRef.current = null;
      readyRef.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    ctrlRef.current?.setTheme(theme);
  }, [theme]);

  // 侧边栏遮挡区域：桌面端左侧固定宽度，手机端底部抽屉；关闭或折叠时为 0。
  // 放在镜头相关 effect 之前，保证同一次更新中框选 / 居中使用的是新遮挡区域；窗口尺寸变化（含横竖屏）时重算。
  const sidebarShown = sidebar !== 'idle' && !sidebarCollapsed;
  useEffect(() => {
    const ctrl = ctrlRef.current;
    const el = containerRef.current;
    if (!ctrl || !el) return;
    const apply = () => {
      if (!sidebarShown) ctrl.setInset(0, 0);
      else if (window.matchMedia(MOBILE_QUERY).matches) ctrl.setInset(0, Math.round(el.clientHeight * MOBILE_DRAWER_RATIO));
      else ctrl.setInset(LEFT_SIDEBAR_WIDTH, 0);
    };
    apply();
    window.addEventListener('resize', apply);
    return () => window.removeEventListener('resize', apply);
  }, [sidebarShown]);

  // 数据就绪
  useEffect(() => {
    if (readyRef.current && ctrlRef.current && geojson) {
      ctrlRef.current.setData(geojson, currentWorld);
      ctrlRef.current.setHiddenLines(hiddenLines);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [geojson]);

  // 世界切换
  useEffect(() => {
    if (readyRef.current && ctrlRef.current) {
      ctrlRef.current.setWorld(currentWorld);
      ctrlRef.current.setHiddenLines(hiddenLines);
      // 列车按世界过滤渲染，切世界后需重刷（否则仍显示旧世界列车）。
      ctrlRef.current.setTrains([...useStore.getState().trains.values()]);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentWorld]);

  // 图层可见性
  useEffect(() => {
    if (readyRef.current && ctrlRef.current) ctrlRef.current.setHiddenLines(hiddenLines);
  }, [hiddenLines]);

  // 高亮线路（点击地图线路 / 线路面板 / 车站面板线路名）：整条线路高亮，优先于路线候选高亮。
  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current) return;
    if (!highlightLineId) return; // 无线路高亮时交由下面的路线高亮 effect 处理
    ctrlRef.current.highlightLine(highlightLineId);
    ctrlRef.current.fitToLine(highlightLineId);
  }, [highlightLineId, sidebar, lineFocusSeq]);

  // 高亮选中路线（联程票高亮各段并集）。线路高亮生效时跳过，避免互相覆盖。
  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current) return;
    if (highlightLineId) return; // 线路高亮优先
    const route = selectedRouteIndex != null ? candidates[selectedRouteIndex] : null;
    if (!route) {
      ctrlRef.current.highlightRoute(null);
      return;
    }
    // 乘车历史：当前 geojson 已无法还原路线时，改用结算时的坐标快照渲染
    if (route.historyGeometry) {
      ctrlRef.current.highlightHistoryGeometry(route.historyGeometry, route.nodeIds, route.expressRoute ?? false);
      ctrlRef.current.fitToGeometry(route.historyGeometry);
      return;
    }
    const legPaths = route.kind === 'through' && route.journey ? route.journey.legs : [route];
    const legs = legPaths.map((l) => l.nodeIds);
    // 逐段 lineId 用于共线段选边（快速车 / 乘车历史无逐段信息，为空数组，由控制器按连续性推断）
    ctrlRef.current.highlightRoute(legs, route.expressRoute ?? false, {
      lineHints: legPaths.map((l) => l.lineIdSequence),
      allowedLines: route.allowedLineIds,
    });
    // 镜头联动：缩放并移动到选中线路（路线卡片 / 乘车历史通用）；
    // 侧边栏遮挡区域已作为地图常驻 padding 扣除，线路不会落在侧边栏下方。
    // 列车列表点击（center 模式）：镜头由专门的居中 effect 处理，这里不框选整条路线。
    if (!(sidebar === 'train' && trainFocusMode === 'center')) {
      ctrlRef.current.fitToNodes(legs);
    }
  }, [candidates, selectedRouteIndex, sidebar, trainFocusMode, highlightLineId]);

  // 列车列表点击后居中到列车位置（切换世界后再定位，故依赖 currentWorld）。
  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current) return;
    if (sidebar !== 'train' || trainFocusMode !== 'center' || !selectedTrainId) return;
    const train = useStore.getState().trains.get(selectedTrainId);
    if (!train || train.world !== currentWorld) return;
    ctrlRef.current.centerOnGame(train.head.x, train.head.z, getConfig().mapStyle.trainFocusZoom);
  }, [selectedTrainId, trainFocusMode, trainFocusSeq, currentWorld, sidebar]);

  // 搜索选中车站：框选全部同名站台（放在世界切换 effect 之后，跨世界时先切世界再定位）。
  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current || !stationFocus) return;
    ctrlRef.current.fitToStation(stationFocus.name);
  }, [stationFocus]);

  // 列车
  useEffect(() => {
    if (readyRef.current && ctrlRef.current) ctrlRef.current.setTrains([...trains.values()]);
  }, [trains]);

  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current) return;
    ctrlRef.current.setTrackingTrainId(sidebar === 'train' ? trackingTrainId : null);
  }, [trackingTrainId, sidebar, currentWorld]);

  // 防 ESLint 未用告警（handlers 已在 onReady 内绑定到 getState）
  void clickStation;
  void selectTrain;

  return <div ref={containerRef} className="map-container" />;
}
