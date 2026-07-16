import { useEffect, useRef } from 'react';
import 'maplibre-gl/dist/maplibre-gl.css';
import { MapController } from '../map/MapController';
import { useStore } from '../store/useStore';

/** 左侧侧边栏宽度（与 styles.css 的 .sidebar 保持一致），用于框选时留出遮挡内边距。 */
const LEFT_SIDEBAR_WIDTH = 320;

/** 地图视图：把 MapController 与全局状态连起来。 */
export function MapView() {
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
  const selectedTrainId = useStore((s) => s.selectedTrainId);
  const trainFocusMode = useStore((s) => s.trainFocusMode);
  const clickStation = useStore((s) => s.clickStation);
  const selectTrain = useStore((s) => s.selectTrain);

  // 初始化地图（一次）
  useEffect(() => {
    if (!containerRef.current) return;
    const ctrl = new MapController(containerRef.current);
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

  // 高亮选中路线（联程票高亮各段并集）
  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current) return;
    const route = selectedRouteIndex != null ? candidates[selectedRouteIndex] : null;
    if (!route) {
      ctrlRef.current.highlightRoute(null);
      return;
    }
    const legs =
      route.kind === 'through' && route.journey ? route.journey.legs.map((l) => l.nodeIds) : [route.nodeIds];
    ctrlRef.current.highlightRoute(legs);
    // 镜头联动：缩放并移动到选中线路（路线卡片 / 乘车历史通用）；
    // 左侧留出侧边栏宽度，避免线路落在侧边栏下方被遮挡。
    ctrlRef.current.setLeftInset(sidebar !== 'idle' ? LEFT_SIDEBAR_WIDTH : 0);
    // 列车列表点击（center 模式）：镜头由专门的居中 effect 处理，这里不框选整条路线。
    if (!(sidebar === 'train' && trainFocusMode === 'center')) {
      ctrlRef.current.fitToNodes(legs);
    }
  }, [candidates, selectedRouteIndex, sidebar, trainFocusMode]);

  // 列车列表点击后居中到列车位置（切换世界后再定位，故依赖 currentWorld）。
  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current) return;
    if (sidebar !== 'train' || trainFocusMode !== 'center' || !selectedTrainId) return;
    const train = useStore.getState().trains.get(selectedTrainId);
    if (!train || train.world !== currentWorld) return;
    ctrlRef.current.setLeftInset(LEFT_SIDEBAR_WIDTH);
    ctrlRef.current.centerOnGame(train.head.x, train.head.z);
  }, [selectedTrainId, trainFocusMode, currentWorld, sidebar]);

  // 列车
  useEffect(() => {
    if (readyRef.current && ctrlRef.current) ctrlRef.current.setTrains([...trains.values()]);
  }, [trains]);

  // 防 ESLint 未用告警（handlers 已在 onReady 内绑定到 getState）
  void clickStation;
  void selectTrain;

  return <div ref={containerRef} className="map-container" />;
}
