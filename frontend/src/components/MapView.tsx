import { useEffect, useRef } from 'react';
import 'maplibre-gl/dist/maplibre-gl.css';
import { MapController } from '../map/MapController';
import { useStore } from '../store/useStore';

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
    if (route.kind === 'through' && route.journey) {
      ctrlRef.current.highlightRoute(route.journey.legs.map((l) => l.nodeIds));
    } else {
      ctrlRef.current.highlightRoute([route.nodeIds]);
    }
  }, [candidates, selectedRouteIndex]);

  // 列车
  useEffect(() => {
    if (readyRef.current && ctrlRef.current) ctrlRef.current.setTrains([...trains.values()]);
  }, [trains]);

  // 侧栏开合导致地图区收缩/扩张：等 CSS 过渡（.2s）结束后重算画布，保证可完整拖动
  useEffect(() => {
    if (!readyRef.current || !ctrlRef.current) return;
    const raf = requestAnimationFrame(() => ctrlRef.current?.resize());
    const timer = setTimeout(() => ctrlRef.current?.resize(), 220);
    return () => {
      cancelAnimationFrame(raf);
      clearTimeout(timer);
    };
  }, [sidebar]);

  // 防 ESLint 未用告警（handlers 已在 onReady 内绑定到 getState）
  void clickStation;
  void selectTrain;

  return <div ref={containerRef} className="map-container" />;
}
