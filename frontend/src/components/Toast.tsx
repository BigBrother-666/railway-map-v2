import { useEffect } from 'react';
import { useStore } from '../store/useStore';

/** 顶部居中的临时提示（登录成功 / 失败等）。数秒后自动消失，也可点击关闭。 */
export function Toast() {
  const toast = useStore((s) => s.toast);
  const dismissToast = useStore((s) => s.dismissToast);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(dismissToast, 4000);
    return () => window.clearTimeout(timer);
  }, [toast, dismissToast]);

  if (!toast) return null;

  return (
    <div className={`toast toast-${toast.kind}`} role="status" onClick={dismissToast}>
      <span className="toast-icon">{toast.kind === 'success' ? '✓' : '!'}</span>
      <span className="toast-message">{toast.message}</span>
    </div>
  );
}
