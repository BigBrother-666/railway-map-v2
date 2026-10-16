/** 铁路系统详情：系统信息 / 成员 / Logo 编辑、删除（仅创建者或管理员），以及线路列表与线路编辑。 */
import { useState } from 'react';
import { getConfig } from '../../config';
import { centerApi, CenterError } from '../api';
import type { CenterContext } from '../CenterApp';
import { navigate } from '../router';
import type { PlayerRow, PrivateLine, SystemDetail } from '../types';
import {
  Card,
  ConfirmModal,
  Empty,
  ErrorState,
  formatNumber,
  Loading,
  PageHeader,
  PluginOfflineBanner,
  successText,
  toCenterError,
  useAsync,
  useNotice,
} from '../ui';
import { LineEditor } from './LineEditor';

export function SystemPage({ id, ctx }: { id: string; ctx: CenterContext }) {
  const sys = useAsync(() => centerApi.system(id), [id]);
  const lines = useAsync(() => centerApi.systemLines(id), [id]);
  const { notify, node: notice } = useNotice();
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [editLine, setEditLine] = useState<PrivateLine | null>(null);
  const [deleteLine, setDeleteLine] = useState<PrivateLine | null>(null);
  const online = ctx.profile.pluginOnline;

  if (sys.loading && !sys.data) return <Loading rows={6} />;
  if (sys.error || !sys.data) return sys.error ? <ErrorState error={sys.error} onRetry={sys.reload} /> : null;
  const s = sys.data;
  const currency = getConfig().currencyName;
  const editableSystems = ctx.profile.systems;

  // 插件写入后会推送新快照，稍等再刷新以拿到最新数据
  const refreshSoon = () =>
    setTimeout(() => {
      void sys.reload();
      void lines.reload();
      void ctx.refreshProfile();
    }, 600);

  return (
    <>
      {notice}
      <PageHeader
        title={s.name}
        sub={<span className="c-mono">{s.id}</span>}
        actions={
          <>
            {s.canEdit && (
              <button className="c-btn" disabled={!online} onClick={() => setEditing(true)}>
                编辑系统
              </button>
            )}
            {s.canDelete && (
              <button className="c-btn danger" disabled={!online} onClick={() => setDeleting(true)}>
                删除系统
              </button>
            )}
          </>
        }
      />
      <PluginOfflineBanner online={online} />

      <div className="c-grid-2">
        <Card title="基本信息">
          <dl className="c-dl">
            <dt>每公里票价</dt>
            <dd>
              {s.pricePerKm != null
                ? `${s.pricePerKm} ${currency}/km`
                : `未单独设置，使用默认 ${s.effectivePricePerKm ?? getConfig().defaultPricePerKm} ${currency}/km`}
            </dd>
            <dt>创建者</dt>
            <dd>{s.creatorName || <span className="c-mono">{s.creator}</span>}</dd>
            <dt>累计营收</dt>
            <dd>
              {formatNumber(s.income)} {currency}
              <span className="c-muted">（已发放 {formatNumber(s.withdrawn)}）</span>
            </dd>
            <dt>线路数</dt>
            <dd>{s.lineCount}</dd>
          </dl>
        </Card>
        <Card title={`成员（${s.members.length}）`}>
          <ul className="c-member-list">
            {s.members.map((m) => (
              <li key={m}>
                <span>{s.memberNames[m] || '未知玩家'}</span>
                {m === s.creator && <span className="c-tag">创建者</span>}
                <span className="c-muted c-mono c-ellipsis">{m}</span>
              </li>
            ))}
          </ul>
        </Card>
      </div>

      <LogoCard sys={s} online={online} notify={notify} onDone={refreshSoon} />

      <Card title={`线路（${lines.data?.length ?? 0}）`}>
        {lines.loading && !lines.data ? (
          <Loading />
        ) : lines.error ? (
          <ErrorState error={lines.error} onRetry={lines.reload} />
        ) : !lines.data || lines.data.length === 0 ? (
          <Empty title="还没有线路" hint="线路需要在游戏内通过 /ticketconfig editRoute 创建，网页端不能新建。" />
        ) : (
          <div className="c-table-wrap">
            <table className="c-table">
              <thead>
                <tr>
                  <th>线路</th>
                  <th>ID</th>
                  <th>车站数</th>
                  <th className="c-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {lines.data.map((l) => (
                  <tr key={l.id}>
                    <td data-label="线路">
                      <span className="c-swatch" style={{ background: l.color }} />
                      {l.name}
                    </td>
                    <td data-label="ID" className="c-mono">
                      {l.id}
                    </td>
                    <td data-label="车站数">{l.bossbarStations.length}</td>
                    <td className="c-right c-actions">
                      <button className="c-btn small" disabled={!online || !s.canEdit} onClick={() => setEditLine(l)}>
                        编辑
                      </button>
                      <button
                        className="c-btn small danger-ghost"
                        disabled={!online || !s.canEdit}
                        onClick={() => setDeleteLine(l)}
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {editing && (
        <SystemEditModal
          sys={s}
          onClose={() => setEditing(false)}
          onSaved={(warnings) => {
            notify('success', successText('已保存并同步到游戏服务器', warnings));
            refreshSoon();
          }}
        />
      )}
      {deleting && (
        <ConfirmModal
          title="删除铁路系统"
          confirmText="永久删除"
          requireInput={s.id}
          message={
            <>
              <p>
                将删除铁路系统 <b>{s.name}</b> 及其全部 {lines.data?.length ?? s.lineCount} 条线路，并清除对应的线路地理数据。
                未发放的营收会一并丢失。此操作不可撤销。
              </p>
              {lines.data && lines.data.length > 0 && (
                <p className="c-muted">将被删除的线路：{lines.data.map((l) => l.name).join('、')}</p>
              )}
            </>
          }
          onConfirm={async () => {
            await centerApi.deleteSystem(s.id);
            await ctx.refreshProfile();
            navigate('/me');
          }}
          onClose={() => setDeleting(false)}
        />
      )}
      {editLine && (
        <LineEditor
          line={editLine}
          allLines={lines.data ?? []}
          systems={editableSystems}
          onClose={() => setEditLine(null)}
          onSaved={(warnings) => {
            notify('success', successText('线路已保存，插件已重载线路配置', warnings));
            refreshSoon();
          }}
        />
      )}
      {deleteLine && (
        <ConfirmModal
          title="删除线路"
          confirmText="删除"
          requireInput={deleteLine.id}
          message={
            <p>
              将删除线路 <b>{deleteLine.name}</b>，并清除其线路地理数据。此操作不可撤销。
            </p>
          }
          onConfirm={async () => {
            await centerApi.deleteLine(deleteLine.id);
            notify('success', '线路已删除');
            refreshSoon();
          }}
          onClose={() => setDeleteLine(null)}
        />
      )}
    </>
  );
}

function SystemEditModal({
  sys,
  onClose,
  onSaved,
}: {
  sys: SystemDetail;
  onClose: () => void;
  onSaved: (warnings?: string[] | null) => void;
}) {
  const [name, setName] = useState(sys.name);
  const [price, setPrice] = useState(sys.pricePerKm == null ? '' : String(sys.pricePerKm));
  const [members, setMembers] = useState<PlayerRow[]>(
    sys.members.map((m) => ({ uuid: m, name: sys.memberNames[m] ?? '' })),
  );
  const [err, setErr] = useState<CenterError | null>(null);
  const [busy, setBusy] = useState(false);
  const membersChanged =
    members.length !== sys.members.length || members.some((m, i) => m.uuid !== sys.members[i]);

  const submit = async () => {
    setErr(null);
    const p = price.trim() === '' ? null : Number(price);
    if (p !== null && (!Number.isFinite(p) || p < 0)) {
      setErr(new CenterError(400, 'invalid', '单价必须是非负数字，留空表示使用全局默认', 'pricePerKm'));
      return;
    }
    setBusy(true);
    try {
      const res = await centerApi.updateSystem(sys.id, {
        name: name.trim(),
        pricePerKm: p,
        members: sys.canEditMembers && membersChanged ? members.map((m) => m.uuid) : undefined,
      });
      onSaved(res.warnings);
      onClose();
    } catch (e) {
      setErr(toCenterError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <EditModalFrame title="编辑铁路系统" onClose={onClose} busy={busy} onSubmit={submit} error={err}>
      <label className="c-field">
        <span className="c-label">系统名称</span>
        <input className={`c-input ${err?.field === 'name' ? 'invalid' : ''}`} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="c-field">
        <span className="c-label">每公里票价（{getConfig().currencyName}/km）</span>
        <input
          className={`c-input ${err?.field === 'pricePerKm' ? 'invalid' : ''}`}
          inputMode="decimal"
          value={price}
          placeholder={`留空使用默认 ${sys.pricePerKm == null && sys.effectivePricePerKm != null ? sys.effectivePricePerKm : getConfig().defaultPricePerKm} ${getConfig().currencyName}/km`}
          onChange={(e) => setPrice(e.target.value)}
        />
        <span className="c-help">取值范围由插件 price-per-km-range 决定，超出范围会被拒绝。</span>
      </label>
      {sys.canEditMembers ? (
        <MemberEditor creator={sys.creator} members={members} onChange={setMembers} invalid={err?.field === 'members'} />
      ) : (
        <p className="c-help">只有系统创建者可以修改成员。</p>
      )}
    </EditModalFrame>
  );
}

/** 编辑弹窗外框（复用 Modal 的样式，带保存 / 取消与错误提示）。 */
export function EditModalFrame({
  title,
  onClose,
  onSubmit,
  busy,
  error,
  children,
  wide,
}: {
  title: string;
  onClose: () => void;
  onSubmit: () => void;
  busy: boolean;
  error: CenterError | null;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div className="c-modal-mask" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <form
        className={`c-modal ${wide ? 'wide' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit();
        }}
        onKeyDown={(e) => e.key === 'Escape' && !busy && onClose()}
      >
        <header className="c-modal-head">
          <h3>{title}</h3>
          <button type="button" className="c-icon-btn" onClick={onClose} aria-label="关闭" disabled={busy}>
            ✕
          </button>
        </header>
        <div className="c-modal-body">{children}</div>
        <footer className="c-modal-foot">
          {error && (
            <div className="c-form-error" role="alert">
              {error.message}
              {error.conflicts &&
                Object.keys(error.conflicts).length > 0 &&
                `：${Object.entries(error.conflicts)
                  .map(([s, g]) => `${s}（已在组 ${g}）`)
                  .join('、')}`}
            </div>
          )}
          <button type="button" className="c-btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button type="submit" className="c-btn primary" disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </button>
        </footer>
      </form>
    </div>
  );
}

function MemberEditor({
  creator,
  members,
  onChange,
  invalid,
}: {
  creator: string;
  members: PlayerRow[];
  onChange: (m: PlayerRow[]) => void;
  invalid: boolean;
}) {
  const [q, setQ] = useState('');
  const [results, setResults] = useState<PlayerRow[]>([]);
  const [searching, setSearching] = useState(false);
  const search = async () => {
    const term = q.trim();
    if (!term) return;
    setSearching(true);
    try {
      const found = await centerApi.searchPlayers(term);
      // 允许直接输入 UUID 添加未在库中的玩家
      if (/^[0-9a-fA-F-]{32,36}$/.test(term) && !found.some((p) => p.uuid === term.toLowerCase())) {
        found.unshift({ uuid: term.toLowerCase(), name: '' });
      }
      setResults(found);
    } finally {
      setSearching(false);
    }
  };
  const add = (p: PlayerRow) => {
    if (!members.some((m) => m.uuid === p.uuid)) onChange([...members, p]);
    setResults([]);
    setQ('');
  };
  return (
    <div className={`c-field ${invalid ? 'invalid' : ''}`}>
      <span className="c-label">成员</span>
      <div className="c-chips">
        {members.map((m) => (
          <span key={m.uuid} className="c-chip">
            {m.name || m.uuid.slice(0, 8)}
            {m.uuid === creator ? (
              <span className="c-chip-note">创建者</span>
            ) : (
              <button
                type="button"
                className="c-chip-x"
                aria-label={`移除 ${m.name || m.uuid}`}
                onClick={() => onChange(members.filter((x) => x.uuid !== m.uuid))}
              >
                ✕
              </button>
            )}
          </span>
        ))}
      </div>
      <div className="c-inline">
        <input
          className="c-input"
          placeholder="搜索玩家名或输入 UUID"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              void search();
            }
          }}
        />
        <button type="button" className="c-btn" onClick={search} disabled={searching}>
          搜索
        </button>
      </div>
      {results.length > 0 && (
        <ul className="c-suggest">
          {results.map((p) => (
            <li key={p.uuid}>
              <button type="button" onClick={() => add(p)}>
                {p.name || '（未知名称）'} <span className="c-muted c-mono">{p.uuid}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <span className="c-help">只能搜索到已绑定网页登录或乘过车的玩家；也可直接输入完整 UUID。</span>
    </div>
  );
}

function LogoCard({
  sys,
  online,
  notify,
  onDone,
}: {
  sys: SystemDetail;
  online: boolean;
  notify: (k: 'success' | 'error' | 'warn', t: string) => void;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState<'' | 'web' | 'mc'>('');
  const disabled = !online || !sys.canEdit || busy !== '';
  const upload = async (kind: 'web' | 'mc', file: File | undefined) => {
    if (!file) return;
    if (file.size > 3 * 1024 * 1024) {
      notify('error', '图片不能超过 3MB');
      return;
    }
    if (!['image/png', 'image/jpeg', 'image/webp', 'image/gif'].includes(file.type)) {
      notify('error', '只支持 PNG / JPEG / WebP / GIF 图片');
      return;
    }
    setBusy(kind);
    try {
      const res = await centerApi.uploadLogo(sys.id, kind, file);
      notify('success', successText('Logo 已更新', res.warnings));
      onDone();
    } catch (e) {
      notify('error', toCenterError(e).message);
    } finally {
      setBusy('');
    }
  };
  const clear = async (kind: 'web' | 'mc') => {
    setBusy(kind);
    try {
      await centerApi.clearLogo(sys.id, kind);
      notify('success', 'Logo 已清除，将使用默认图标');
      onDone();
    } catch (e) {
      notify('error', toCenterError(e).message);
    } finally {
      setBusy('');
    }
  };
  const slot = (kind: 'web' | 'mc', title: string, hint: string, url?: string | null) => (
    <div className="c-logo-slot">
      <div className="c-logo-preview">
        <img src={url || getConfig().defaultSystemLogo} alt={`${title}预览`} />
      </div>
      <div className="c-logo-info">
        <div className="c-logo-title">{title}</div>
        <div className="c-help">{hint}</div>
        <div className="c-inline">
          <label className={`c-btn small ${disabled ? 'disabled' : ''}`}>
            {busy === kind ? '上传中…' : '上传图片'}
            <input
              type="file"
              accept="image/png,image/jpeg,image/webp,image/gif"
              hidden
              disabled={disabled}
              onChange={(e) => {
                void upload(kind, e.target.files?.[0]);
                e.target.value = '';
              }}
            />
          </label>
          {url && (
            <button className="c-btn small danger-ghost" disabled={disabled} onClick={() => clear(kind)}>
              清除
            </button>
          )}
        </div>
      </div>
    </div>
  );
  return (
    <Card title="系统 Logo">
      <div className="c-logo-grid">
        {slot('web', '网页 Logo', '显示在线路图侧栏等网页位置，插件会自动缩放。', sys.logoUrl)}
        {slot('mc', '游戏内 Logo', '显示在游戏内车票等位置，插件会自动缩放。', sys.mcLogoUrl)}
      </div>
    </Card>
  );
}
