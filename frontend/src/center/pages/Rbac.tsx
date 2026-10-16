/** 权限管理（RBAC）：角色与权限勾选、用户授权。后端会拒绝内置角色修改、撤销初始超管、把自己锁在外面的操作。 */
import { useMemo, useState } from 'react';
import { centerApi, CenterError } from '../api';
import type { CenterContext } from '../CenterApp';
import type { PermissionInfo, PlayerRow, Role } from '../types';
import { Card, ConfirmModal, ErrorState, formatTime, Loading, PageHeader, toCenterError, useAsync, useNotice } from '../ui';
import { EditModalFrame } from './System';

export function RbacPage({ ctx }: { ctx: CenterContext }) {
  const { data, error, loading, reload } = useAsync(() => centerApi.rbac(), []);
  const { notify, node } = useNotice();
  const [editRole, setEditRole] = useState<Role | 'new' | null>(null);
  const [delRole, setDelRole] = useState<Role | null>(null);
  const [granting, setGranting] = useState(false);

  // 按玩家分组展示授权
  const users = useMemo(() => {
    const m = new Map<string, { uuid: string; name: string; roles: { id: number; name: string; grantedAt: number }[] }>();
    for (const u of data?.users ?? []) {
      const e = m.get(u.uuid) ?? { uuid: u.uuid, name: u.name, roles: [] };
      e.roles.push({ id: u.roleId, name: u.roleName, grantedAt: u.grantedAt });
      m.set(u.uuid, e);
    }
    return [...m.values()];
  }, [data]);

  if (loading && !data) return <Loading rows={6} />;
  if (error || !data) return error ? <ErrorState error={error} onRetry={reload} /> : null;
  const permName = (k: string) => data.permissions.find((p) => p.key === k)?.name ?? k;

  const after = async (text: string) => {
    notify('success', text);
    await reload();
    await ctx.refreshProfile();
  };
  const revoke = async (uuid: string, roleId: number) => {
    try {
      await centerApi.revokeRole(uuid, roleId);
      await after('已撤销');
    } catch (e) {
      notify('error', toCenterError(e).message);
    }
  };

  return (
    <>
      {node}
      <PageHeader title="权限管理" sub="基于角色的访问控制：给角色分配权限，再把角色授予玩家。" />

      <Card title="角色" extra={<button className="c-btn primary small" onClick={() => setEditRole('new')}>新建角色</button>}>
        <div className="c-table-wrap">
          <table className="c-table">
            <thead>
              <tr>
                <th>角色</th>
                <th>权限</th>
                <th>人数</th>
                <th className="c-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {data.roles.map((r) => (
                <tr key={r.id}>
                  <td data-label="角色">
                    <div>
                      {r.name}
                      {r.builtin && <span className="c-tag">内置</span>}
                    </div>
                    {r.description && <div className="c-muted">{r.description}</div>}
                  </td>
                  <td data-label="权限">
                    <div className="c-chips">
                      {r.permissions.map((p) => (
                        <span key={p} className="c-chip small">
                          {permName(p)}
                        </span>
                      ))}
                      {r.permissions.length === 0 && <span className="c-muted">无</span>}
                    </div>
                  </td>
                  <td data-label="人数">{r.userCount}</td>
                  <td className="c-right c-actions">
                    {!r.builtin && (
                      <>
                        <button className="c-btn small" onClick={() => setEditRole(r)}>
                          编辑
                        </button>
                        <button className="c-btn small danger-ghost" onClick={() => setDelRole(r)}>
                          删除
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      <Card title="用户授权" extra={<button className="c-btn primary small" onClick={() => setGranting(true)}>授予角色</button>}>
        {users.length === 0 ? (
          <div className="c-muted">还没有任何授权。</div>
        ) : (
          <div className="c-table-wrap">
            <table className="c-table">
              <thead>
                <tr>
                  <th>玩家</th>
                  <th>角色</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.uuid}>
                    <td data-label="玩家">
                      <div>{u.name || '未知玩家'}</div>
                      <div className="c-muted c-mono">{u.uuid}</div>
                    </td>
                    <td data-label="角色">
                      <div className="c-chips">
                        {u.roles.map((r) => {
                          const locked = r.id === data.superRoleId && u.uuid === data.superAdminUuid;
                          return (
                            <span key={r.id} className="c-chip" title={`授予于 ${formatTime(r.grantedAt)}`}>
                              {r.name}
                              {locked ? (
                                <span className="c-chip-note">环境变量指定</span>
                              ) : (
                                <button className="c-chip-x" aria-label={`撤销 ${r.name}`} onClick={() => revoke(u.uuid, r.id)}>
                                  ✕
                                </button>
                              )}
                            </span>
                          );
                        })}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {editRole && (
        <RoleModal
          role={editRole === 'new' ? null : editRole}
          permissions={data.permissions}
          onClose={() => setEditRole(null)}
          onSaved={() => after('角色已保存')}
        />
      )}
      {delRole && (
        <ConfirmModal
          title="删除角色"
          confirmText="删除"
          message={<p>删除角色「{delRole.name}」？将同时撤销 {delRole.userCount} 名玩家的该角色。</p>}
          onConfirm={async () => {
            await centerApi.deleteRole(delRole.id);
            await after('角色已删除');
          }}
          onClose={() => setDelRole(null)}
        />
      )}
      {granting && (
        <GrantModal roles={data.roles} onClose={() => setGranting(false)} onSaved={() => after('已授予')} />
      )}
    </>
  );
}

function RoleModal({
  role,
  permissions,
  onClose,
  onSaved,
}: {
  role: Role | null;
  permissions: PermissionInfo[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(role?.name ?? '');
  const [desc, setDesc] = useState(role?.description ?? '');
  const [perms, setPerms] = useState<string[]>(role?.permissions ?? []);
  const [err, setErr] = useState<CenterError | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setErr(null);
    if (!name.trim()) return setErr(new CenterError(400, 'invalid', '角色名不能为空', 'name'));
    setBusy(true);
    try {
      const body = { name: name.trim(), description: desc.trim(), permissions: perms };
      if (role) await centerApi.updateRole(role.id, body);
      else await centerApi.createRole(body);
      onSaved();
      onClose();
    } catch (e) {
      setErr(toCenterError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <EditModalFrame title={role ? `编辑角色 · ${role.name}` : '新建角色'} onClose={onClose} onSubmit={submit} busy={busy} error={err}>
      <label className="c-field">
        <span className="c-label">角色名</span>
        <input className={`c-input ${err?.field === 'name' ? 'invalid' : ''}`} value={name} maxLength={32} onChange={(e) => setName(e.target.value)} autoFocus />
      </label>
      <label className="c-field">
        <span className="c-label">描述（可选）</span>
        <input className="c-input" value={desc} onChange={(e) => setDesc(e.target.value)} />
      </label>
      <fieldset className="c-fieldset">
        <legend>权限</legend>
        {permissions.map((p) => (
          <label key={p.key} className="c-perm-row">
            <input
              type="checkbox"
              checked={perms.includes(p.key)}
              onChange={(e) => setPerms(e.target.checked ? [...perms, p.key] : perms.filter((x) => x !== p.key))}
            />
            <span>
              <span className="c-perm-name">{p.name}</span>
              <span className="c-muted"> {p.description}</span>
            </span>
          </label>
        ))}
      </fieldset>
    </EditModalFrame>
  );
}

function GrantModal({ roles, onClose, onSaved }: { roles: Role[]; onClose: () => void; onSaved: () => void }) {
  const [q, setQ] = useState('');
  const [results, setResults] = useState<PlayerRow[]>([]);
  const [player, setPlayer] = useState<PlayerRow | null>(null);
  const [roleId, setRoleId] = useState<number>(roles.find((r) => !r.builtin)?.id ?? roles[0]?.id ?? 0);
  const [err, setErr] = useState<CenterError | null>(null);
  const [busy, setBusy] = useState(false);
  const search = async () => {
    const term = q.trim();
    if (!term) return;
    try {
      const found = await centerApi.searchPlayers(term);
      if (/^[0-9a-fA-F-]{32,36}$/.test(term) && !found.some((p) => p.uuid === term.toLowerCase())) {
        found.unshift({ uuid: term.toLowerCase(), name: '' });
      }
      setResults(found);
    } catch (e) {
      setErr(toCenterError(e));
    }
  };
  const submit = async () => {
    setErr(null);
    if (!player) return setErr(new CenterError(400, 'invalid', '请选择玩家', 'uuid'));
    setBusy(true);
    try {
      await centerApi.grantRole(player.uuid, roleId);
      onSaved();
      onClose();
    } catch (e) {
      setErr(toCenterError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <EditModalFrame title="授予角色" onClose={onClose} onSubmit={submit} busy={busy} error={err}>
      <div className="c-field">
        <span className="c-label">玩家</span>
        {player ? (
          <div className="c-chips">
            <span className="c-chip">
              {player.name || player.uuid}
              <button type="button" className="c-chip-x" aria-label="重新选择" onClick={() => setPlayer(null)}>
                ✕
              </button>
            </span>
          </div>
        ) : (
          <>
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
                autoFocus
              />
              <button type="button" className="c-btn" onClick={search}>
                搜索
              </button>
            </div>
            {results.length > 0 && (
              <ul className="c-suggest">
                {results.map((p) => (
                  <li key={p.uuid}>
                    <button type="button" onClick={() => setPlayer(p)}>
                      {p.name || '（未知名称）'} <span className="c-muted c-mono">{p.uuid}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>
      <label className="c-field">
        <span className="c-label">角色</span>
        <select className="c-input" value={roleId} onChange={(e) => setRoleId(Number(e.target.value))}>
          {roles.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      </label>
    </EditModalFrame>
  );
}
