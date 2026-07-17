import { useEffect } from 'react';
import { useStore } from './store/useStore';
import { useRealtime } from './api/realtime';
import { MapView } from './components/MapView';
import { TopBar } from './components/TopBar';
import { LeftSidebar } from './components/LeftSidebar';
import { RightSidebar } from './components/RightSidebar';
import { Toast } from './components/Toast';
import { consumeLoginResult } from './login';

export function App() {
  const init = useStore((s) => s.init);
  const loading = useStore((s) => s.loading);
  const loadError = useStore((s) => s.loadError);
  const online = useStore((s) => s.meta?.online ?? false);
  const showToast = useStore((s) => s.showToast);
  const refreshPlayer = useStore((s) => s.refreshPlayer);

  useEffect(() => {
    init();
  }, [init]);

  // OAuth 回调后地址带 ?login=success|error；读取后弹提示、拉取登录态并清理 URL。
  useEffect(() => {
    const result = consumeLoginResult();
    if (!result) return;
    showToast(result.kind, result.message);
    if (result.kind === 'success') refreshPlayer();
  }, [showToast, refreshPlayer]);

  // 实时列车（仅游戏在线时连）
  useRealtime(online);

  return (
    <div className="app">
      <TopBar />
      <div className="app-body">
        <MapView />
        <LeftSidebar />
        <RightSidebar />
        {loading && <div className="overlay">加载中…</div>}
        {loadError && <div className="overlay error">加载失败：{loadError}</div>}
      </div>
      <Toast />
    </div>
  );
}
