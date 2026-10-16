/** 操作日志：按时间倒序分页，筛选操作人 / 操作类型 / 时间范围，展开查看变更前后详情。 */
import { Fragment, useState } from 'react';
import { centerApi } from '../api';
import { Card, Empty, ErrorState, formatTime, Loading, PageHeader, useAsync } from '../ui';

const ACTIONS: { value: string; label: string }[] = [
  { value: '', label: '全部操作' },
  { value: 'system.', label: '铁路系统' },
  { value: 'line.', label: '线路' },
  { value: 'station_alias.', label: '车站别名' },
  { value: 'station_group.', label: '车站组' },
  { value: 'settings.', label: '服务器配置' },
  { value: 'rbac.', label: '权限管理' },
];

const ACTION_LABEL: Record<string, string> = {
  'system.update': '编辑铁路系统',
  'system.delete': '删除铁路系统',
  'system.logo': '修改系统 Logo',
  'line.update': '编辑线路',
  'line.delete': '删除线路',
  'station_alias.upsert': '设置车站别名',
  'station_alias.delete': '删除车站别名',
  'station_group.upsert': '设置车站组',
  'station_group.delete': '删除车站组',
  'settings.update': '修改服务器配置',
  'settings.reset': '恢复默认配置',
  'rbac.role.create': '新建角色',
  'rbac.role.update': '编辑角色',
  'rbac.role.delete': '删除角色',
  'rbac.user.grant': '授予角色',
  'rbac.user.revoke': '撤销角色',
};

function dateToMs(v: string, endOfDay: boolean): number | undefined {
  if (!v) return undefined;
  const d = new Date(`${v}T${endOfDay ? '23:59:59' : '00:00:00'}`);
  return Number.isNaN(d.getTime()) ? undefined : d.getTime();
}

export function AuditPage() {
  const [page, setPage] = useState(1);
  const [actor, setActor] = useState('');
  const [action, setAction] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [applied, setApplied] = useState({ actor: '', action: '', from: '', to: '' });
  const [open, setOpen] = useState<number | null>(null);
  const { data, error, loading, reload } = useAsync(
    () =>
      centerApi.audit({
        page,
        actor: applied.actor,
        action: applied.action,
        from: dateToMs(applied.from, false),
        to: dateToMs(applied.to, true),
      }),
    [page, applied],
  );

  const apply = () => {
    setPage(1);
    setApplied({ actor: actor.trim(), action, from, to });
  };

  return (
    <>
      <PageHeader title="操作日志" sub="个人中心中所有管理操作的记录（含失败的操作）。" />
      <Card>
        <form
          className="c-filter"
          onSubmit={(e) => {
            e.preventDefault();
            apply();
          }}
        >
          <input className="c-input" placeholder="操作人（名称或 UUID）" value={actor} onChange={(e) => setActor(e.target.value)} aria-label="操作人" />
          <select className="c-input" value={action} onChange={(e) => setAction(e.target.value)} aria-label="操作类型">
            {ACTIONS.map((a) => (
              <option key={a.value} value={a.value}>
                {a.label}
              </option>
            ))}
          </select>
          <input className="c-input" type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="开始日期" />
          <input className="c-input" type="date" value={to} onChange={(e) => setTo(e.target.value)} aria-label="结束日期" />
          <button className="c-btn primary" type="submit">
            筛选
          </button>
        </form>

        {loading && !data ? (
          <Loading rows={5} />
        ) : error ? (
          <ErrorState error={error} onRetry={reload} />
        ) : !data || data.items.length === 0 ? (
          <Empty title="没有操作记录" />
        ) : (
          <>
            <div className="c-table-wrap">
              <table className="c-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>操作人</th>
                    <th>操作</th>
                    <th>对象</th>
                    <th>结果</th>
                  </tr>
                </thead>
                <tbody>
                  {data.items.map((e) => (
                    <Fragment key={e.id}>
                      <tr className="c-row-click" onClick={() => setOpen(open === e.id ? null : e.id)} aria-expanded={open === e.id}>
                        <td data-label="时间">{formatTime(e.createdAt)}</td>
                        <td data-label="操作人">{e.actorName || e.actorUuid}</td>
                        <td data-label="操作">{ACTION_LABEL[e.action] ?? e.action}</td>
                        <td data-label="对象" className="c-mono c-ellipsis">
                          {e.target}
                        </td>
                        <td data-label="结果">
                          {e.success ? <span className="c-tag ok">成功</span> : <span className="c-tag err">失败 {e.reason}</span>}
                        </td>
                      </tr>
                      {open === e.id && (
                        <tr className="c-row-detail">
                          <td colSpan={5}>
                            <pre className="c-pre">{prettyDetail(e.detail)}</pre>
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="c-pager">
              <button className="c-btn small" disabled={page <= 1} onClick={() => setPage(page - 1)}>
                上一页
              </button>
              <span className="c-muted">
                第 {data.page} / {Math.max(1, data.totalPages)} 页 · 共 {data.total} 条
              </span>
              <button className="c-btn small" disabled={page >= data.totalPages} onClick={() => setPage(page + 1)}>
                下一页
              </button>
            </div>
          </>
        )}
      </Card>
    </>
  );
}

function prettyDetail(detail?: string): string {
  if (!detail) return '（无详情）';
  try {
    return JSON.stringify(JSON.parse(detail), null, 2);
  } catch {
    return detail;
  }
}
