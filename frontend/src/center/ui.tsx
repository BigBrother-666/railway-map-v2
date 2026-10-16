/** 个人中心通用 UI：加载 / 空 / 错误 / 无权限状态、卡片、确认弹窗、数据加载 hook。 */
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { CenterError } from './api';

/** 异步数据加载：返回 data / error / loading 与 reload。 */
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<CenterError | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const seq = useRef(0);

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const run = useCallback(fn, deps);
  const reload = useCallback(async () => {
    const my = ++seq.current;
    setLoading(true);
    setError(undefined);
    try {
      const v = await run();
      if (my === seq.current) setData(v);
    } catch (e) {
      if (my === seq.current) setError(toCenterError(e));
    } finally {
      if (my === seq.current) setLoading(false);
    }
  }, [run]);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { data, error, loading, reload, setData };
}

export function toCenterError(e: unknown): CenterError {
  if (e instanceof CenterError) return e;
  return new CenterError(0, 'unknown', e instanceof Error ? e.message : String(e));
}

export function Loading({ rows = 3 }: { rows?: number }) {
  return (
    <div className="c-skeleton" aria-busy="true" aria-label="加载中">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="c-skeleton-row" style={{ width: `${90 - i * 15}%` }} />
      ))}
    </div>
  );
}

export function Empty({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="c-state">
      <div className="c-state-icon" aria-hidden>
        ○
      </div>
      <div className="c-state-title">{title}</div>
      {hint && <div className="c-state-hint">{hint}</div>}
      {action}
    </div>
  );
}

/** 错误状态：按状态码区分无权限 / 不存在 / 插件离线 / 其他，并提供重试。 */
export function ErrorState({ error, onRetry }: { error: CenterError; onRetry?: () => void }) {
  if (error.status === 403) {
    return <Empty title="没有权限" hint="你没有访问该页面的权限，如有需要请联系管理员。" />;
  }
  if (error.status === 404) {
    return <Empty title="内容不存在" hint="它可能已被删除，或你不在该铁路系统中。" />;
  }
  if (error.status === 401) {
    return <Empty title="登录已过期" hint="请返回地图页重新登录。" action={<a className="c-btn primary" href="/">返回地图</a>} />;
  }
  return (
    <div className="c-state error" role="alert">
      <div className="c-state-icon" aria-hidden>
        !
      </div>
      <div className="c-state-title">加载失败</div>
      <div className="c-state-hint">{error.message}</div>
      {onRetry && (
        <button className="c-btn" onClick={onRetry}>
          重试
        </button>
      )}
    </div>
  );
}

export function Card({
  title,
  extra,
  children,
  className = '',
}: {
  title?: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`c-card ${className}`}>
      {(title || extra) && (
        <header className="c-card-head">
          {title && <h2 className="c-card-title">{title}</h2>}
          {extra && <div className="c-card-extra">{extra}</div>}
        </header>
      )}
      {children}
    </section>
  );
}

export function PageHeader({ title, sub, actions }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="c-page-head">
      <div>
        <h1 className="c-page-title">{title}</h1>
        {sub && <div className="c-page-sub">{sub}</div>}
      </div>
      {actions && <div className="c-page-actions">{actions}</div>}
    </div>
  );
}

/** 插件离线横幅：写操作需要游戏服务器在线。 */
export function PluginOfflineBanner({ online }: { online: boolean }) {
  if (online) return null;
  return (
    <div className="c-banner warn" role="status">
      游戏服务器当前未连接，修改操作暂不可用；展示的是最近一次同步的数据。
    </div>
  );
}

/** 模态弹窗（Esc / 点击遮罩关闭）。 */
export function Modal({
  title,
  onClose,
  children,
  footer,
  wide,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div className="c-modal-mask" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`c-modal ${wide ? 'wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
        <header className="c-modal-head">
          <h3>{title}</h3>
          <button className="c-icon-btn" onClick={onClose} aria-label="关闭">
            ✕
          </button>
        </header>
        <div className="c-modal-body">{children}</div>
        {footer && <footer className="c-modal-foot">{footer}</footer>}
      </div>
    </div>
  );
}

/** 危险操作确认：可要求输入确认文本（如系统 ID）。 */
export function ConfirmModal({
  title,
  message,
  confirmText = '确认',
  requireInput,
  onConfirm,
  onClose,
}: {
  title: string;
  message: ReactNode;
  confirmText?: string;
  requireInput?: string;
  onConfirm: () => Promise<void> | void;
  onClose: () => void;
}) {
  const [input, setInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const ok = !requireInput || input === requireInput;
  const submit = async () => {
    setBusy(true);
    setErr('');
    try {
      await onConfirm();
      onClose();
    } catch (e) {
      setErr(toCenterError(e).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <button className="c-btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="c-btn danger" onClick={submit} disabled={!ok || busy}>
            {busy ? '处理中…' : confirmText}
          </button>
        </>
      }
    >
      <div className="c-confirm-msg">{message}</div>
      {requireInput && (
        <label className="c-field">
          <span className="c-label">
            请输入 <code>{requireInput}</code> 以确认
          </span>
          <input className="c-input" value={input} onChange={(e) => setInput(e.target.value)} autoFocus />
        </label>
      )}
      {err && <div className="c-form-error">{err}</div>}
    </Modal>
  );
}

/** 轻量提示条（右上角，自动消失）。 */
export function useNotice() {
  const [notice, setNotice] = useState<{ kind: 'success' | 'error' | 'warn'; text: string } | null>(null);
  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(null), notice.kind === 'error' ? 6000 : 3500);
    return () => clearTimeout(t);
  }, [notice]);
  const node = notice && (
    <div className={`c-notice ${notice.kind}`} role="status" onClick={() => setNotice(null)}>
      {notice.text}
    </div>
  );
  return { notify: (kind: 'success' | 'error' | 'warn', text: string) => setNotice({ kind, text }), node };
}

/** 写操作成功后的提示文案（附插件返回的警告）。 */
export function successText(base: string, warnings?: string[] | null): string {
  return warnings && warnings.length > 0 ? `${base}（${warnings.join('；')}）` : base;
}

export function formatTime(ms: number): string {
  if (!ms) return '-';
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function formatNumber(n: number, digits = 2): string {
  return n.toLocaleString('zh-CN', { maximumFractionDigits: digits });
}
