import { lazy, StrictMode, Suspense } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { isCenterPath } from './center/router';
import './styles.css';

// 个人中心按需加载，不拖慢地图首屏
const CenterApp = lazy(() => import('./center/CenterApp').then((m) => ({ default: m.CenterApp })));

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {isCenterPath(location.pathname) ? (
      <Suspense fallback={null}>
        <CenterApp />
      </Suspense>
    ) : (
      <App />
    )}
  </StrictMode>,
);
