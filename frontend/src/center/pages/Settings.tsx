/**
 * 服务器配置：按 schema 渲染 frontend（前端运行时配置）、route（寻路参数）、plugin（插件链路）、realtime（实时列车）四段的分组表单。
 * 每项显示默认值、单项恢复默认；改动高亮，底部固定「保存 / 放弃」栏。保存后后端立即生效，前端用户刷新页面后生效。
 */
import { useEffect, useMemo, useState } from 'react';
import { centerApi, type FieldError } from '../api';
import type { FrontendConfig, PluginSettings, RealtimeSettings, RouteSettings, SettingsResponse, SettingsSection } from '../types';
import { Card, ConfirmModal, ErrorState, formatTime, Loading, PageHeader, toCenterError, useAsync, useNotice } from '../ui';

type FieldType = 'text' | 'number' | 'color' | 'image' | 'textarea';

interface FieldDef {
  path: string; // 点分路径，如 mapStyle.lineWidth
  label: string;
  type: FieldType;
  help?: string;
  step?: number;
  min?: number;
  max?: number;
}

interface GroupDef {
  title: string;
  desc?: string;
  fields: FieldDef[];
}

const FRONTEND_GROUPS: GroupDef[] = [
  {
    title: '基础',
    fields: [
      { path: 'currencyName', label: '货币名称', type: 'text', help: '票价展示使用的货币名称' },
      { path: 'themeColor', label: '主题色', type: 'color', help: '地图页按钮、高亮等强调色' },
      { path: 'defaultPricePerKm', label: '默认每公里价格', type: 'number', step: 0.01, min: 0, help: '铁路系统未设置单价时用于估算票价' },
      { path: 'defaultWorld', label: '默认世界', type: 'text', help: '必须是下方「世界底图」中已配置的世界' },
      { path: 'avatarUrlTemplate', label: '头像 URL 模板', type: 'text', help: '{player} 替换为玩家名或 UUID' },
      { path: 'routeSearchTimeoutMs', label: '路线查询超时（毫秒）', type: 'number', step: 500, min: 0 },
      { path: 'realtimeWsPath', label: '实时列车 WebSocket 路径', type: 'text', help: '一般无需修改' },
    ],
  },
  {
    title: '地图样式',
    fields: [
      { path: 'mapStyle.lineWidth', label: '线路宽度', type: 'number', step: 0.5, min: 0 },
      { path: 'mapStyle.highlightWidth', label: '高亮线路宽度', type: 'number', step: 0.5, min: 0 },
      { path: 'mapStyle.lineOpacity', label: '线路不透明度', type: 'number', step: 0.05, min: 0, max: 1 },
      { path: 'mapStyle.dimOpacity', label: '非高亮线路不透明度', type: 'number', step: 0.05, min: 0, max: 1 },
      { path: 'mapStyle.stationRadius', label: '车站半径', type: 'number', step: 0.5, min: 0 },
      { path: 'mapStyle.stationStrokeWidth', label: '车站描边宽度', type: 'number', step: 0.5, min: 0 },
      { path: 'mapStyle.stationTextSize', label: '车站字号', type: 'number', step: 1, min: 0 },
      { path: 'mapStyle.stationEnTextSize', label: '车站英文名字号', type: 'number', step: 1, min: 0 },
      { path: 'mapStyle.stationMergePixelDistance', label: '车站合并像素距离', type: 'number', step: 1, min: 0 },
      { path: 'mapStyle.trainIconSize', label: '列车图标大小', type: 'number', step: 0.05, min: 0 },
      { path: 'mapStyle.trainFocusZoom', label: '聚焦列车时缩放级别', type: 'number', step: 0.5, min: 0 },
    ],
  },
  {
    title: '侧栏路线图',
    fields: [
      { path: 'routeDiagram.projectionThresholdBlocks', label: '列车投影最大距离（方块）', type: 'number', step: 1, min: 0 },
      { path: 'routeDiagram.minStationGapPx', label: '最小站距（px）', type: 'number', step: 1, min: 0 },
      { path: 'routeDiagram.maxStationGapPx', label: '最大站距（px）', type: 'number', step: 1, min: 0 },
      { path: 'routeDiagram.trackWidthPx', label: '线条粗细（px）', type: 'number', step: 1, min: 0 },
      { path: 'routeDiagram.trainIconScale', label: '列车图标缩放', type: 'number', step: 0.1, min: 0 },
      { path: 'routeDiagram.trainClusterProgress', label: '列车错开阈值', type: 'number', step: 0.005, min: 0 },
      { path: 'routeDiagram.expressMinHeightPx', label: '快速车竖线最小高度（px）', type: 'number', step: 10, min: 0 },
      { path: 'routeDiagram.expressGradientBlendRatio', label: '快速车颜色渐变比例', type: 'number', step: 0.01, min: 0, max: 0.5 },
    ],
  },
  {
    title: '图标',
    fields: [
      { path: 'trainIcons.express', label: '快速车图标', type: 'image', help: 'data:、http(s): 或静态资源 URL' },
      { path: 'trainIcons.normal', label: '普通车图标', type: 'image' },
      { path: 'defaultSystemLogo', label: '默认铁路系统图标', type: 'image', help: '系统没有 Logo 时使用' },
    ],
  },
];

const ROUTE_GROUPS: GroupDef[] = [
  {
    title: '寻路与排序',
    desc: '后端路线 / 车票查询参数，与游戏内购票菜单的 search.* 对齐。保存后立即生效并清空查询缓存。',
    fields: [
      { path: 'queryRateLimitPerSecond', label: '每玩家每秒查询次数上限', type: 'number', step: 1, min: 0 },
      { path: 'maxDistanceResults', label: '「距离最近」结果条数', type: 'number', step: 1, help: '≤0 表示不限制' },
      { path: 'maxPriceResults', label: '「票价最低」结果条数', type: 'number', step: 1, help: '≤0 表示不限制' },
      { path: 'searchWeightDistance', label: '距离权重', type: 'number', step: 0.05, min: 0, max: 1 },
      { path: 'searchWeightPrice', label: '票价权重', type: 'number', step: 0.05, min: 0, max: 1 },
      { path: 'minDirectResults', label: '至少保留直达条数', type: 'number', step: 1, help: '≤0 不兜底' },
      { path: 'maxTransferResults', label: '联程票最多条数', type: 'number', step: 1, help: '≤0 不限制' },
      { path: 'transferMinImprovement', label: '联程票最低改善比例', type: 'number', step: 0.05, min: 0, max: 1 },
      { path: 'computeTimeoutMs', label: '单次计算超时（毫秒）', type: 'number', step: 1000, min: 0 },
    ],
  },
];

const PLUGIN_GROUPS: GroupDef[] = [
  {
    title: '插件链路',
    desc: '后端与游戏插件之间的心跳与回执等待参数。保存后立即生效（心跳间隔从下一次 ping 起生效）。',
    fields: [
      { path: 'heartbeatSeconds', label: '心跳间隔（秒）', type: 'number', step: 1, min: 0, help: '后端向插件发送 ping 的间隔' },
      { path: 'purchaseTimeoutSeconds', label: '购票回执超时（秒）', type: 'number', step: 1, min: 0, help: '等待插件扣款 / 出票回执' },
      { path: 'purchaseMinIntervalSeconds', label: '购票最小间隔（秒）', type: 'number', step: 1, min: 0, help: '同一玩家两次购票的最小间隔' },
      { path: 'configTimeoutSeconds', label: '配置回执超时（秒）', type: 'number', step: 1, min: 0, help: '个人中心编辑系统 / 线路 / 车站时等待插件写入' },
    ],
  },
];

const REALTIME_GROUPS: GroupDef[] = [
  {
    title: '实时列车',
    desc: '实时列车推送参数。保存后立即生效；发送缓冲只影响之后新建立的连接。',
    fields: [
      { path: 'trainTimeoutSeconds', label: '列车消失超时（秒）', type: 'number', step: 1, min: 0, help: '超过该时长未更新的列车从地图移除' },
      { path: 'clientSendBuffer', label: '每连接发送缓冲（帧）', type: 'number', step: 1, min: 0, max: 4096, help: '满时丢弃最旧帧' },
    ],
  },
];

const SECTION_LABELS: Record<SettingsSection, string> = {
  frontend: '前端显示',
  route: '路线查询',
  plugin: '插件链路',
  realtime: '实时列车',
};

function getPath(obj: unknown, path: string): unknown {
  return path.split('.').reduce<any>((o, k) => (o == null ? undefined : o[k]), obj);
}

function setPath<T>(obj: T, path: string, value: unknown): T {
  const keys = path.split('.');
  const clone: any = structuredClone(obj);
  let cur = clone;
  for (const k of keys.slice(0, -1)) cur = cur[k] ??= {};
  cur[keys[keys.length - 1]] = value;
  return clone;
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

export function SettingsPage() {
  const { data, error, loading, reload, setData } = useAsync(() => centerApi.settings(), []);
  const [tab, setTab] = useState<SettingsSection>('frontend');

  if (loading && !data) return <Loading rows={8} />;
  if (error || !data) return error ? <ErrorState error={error} onRetry={reload} /> : null;

  return (
    <>
      <PageHeader title="服务器配置" sub="原 config.yml 中的 frontend / route / plugin / realtime 段，保存在数据库中。" />
      <div className="c-tabs" role="tablist">
        {(Object.keys(SECTION_LABELS) as SettingsSection[]).map((s) => (
          <button key={s} role="tab" aria-selected={tab === s} className={`c-tab ${tab === s ? 'active' : ''}`} onClick={() => setTab(s)}>
            {SECTION_LABELS[s]}
          </button>
        ))}
      </div>
      {tab === 'frontend' && (
        <SectionForm<FrontendConfig>
          key="frontend"
          section="frontend"
          value={data.frontend}
          defaults={data.defaults.frontend}
          meta={data.frontendMeta}
          groups={FRONTEND_GROUPS}
          save={(v) => centerApi.saveFrontend(v)}
          onSaved={setData}
          extra={(v, set, def) => <WorldTilesEditor value={v} set={set} defaults={def} />}
        />
      )}
      {tab === 'route' && (
        <SectionForm<RouteSettings>
          key="route"
          section="route"
          value={data.route}
          defaults={data.defaults.route}
          meta={data.routeMeta}
          groups={ROUTE_GROUPS}
          save={(v) => centerApi.saveRoute(v)}
          onSaved={setData}
        />
      )}
      {tab === 'plugin' && (
        <SectionForm<PluginSettings>
          key="plugin"
          section="plugin"
          value={data.plugin}
          defaults={data.defaults.plugin}
          meta={data.pluginMeta}
          groups={PLUGIN_GROUPS}
          save={(v) => centerApi.savePlugin(v)}
          onSaved={setData}
        />
      )}
      {tab === 'realtime' && (
        <SectionForm<RealtimeSettings>
          key="realtime"
          section="realtime"
          value={data.realtime}
          defaults={data.defaults.realtime}
          meta={data.realtimeMeta}
          groups={REALTIME_GROUPS}
          save={(v) => centerApi.saveRealtime(v)}
          onSaved={setData}
        />
      )}
    </>
  );
}

function SectionForm<T extends object>({
  section,
  value,
  defaults,
  meta,
  groups,
  save,
  onSaved,
  extra,
}: {
  section: SettingsSection;
  value: T;
  defaults: T;
  meta: { updatedBy?: string; updatedAt?: number };
  groups: GroupDef[];
  save: (v: T) => Promise<SettingsResponse>;
  onSaved: (r: SettingsResponse) => void;
  extra?: (v: T, set: (v: T) => void, defaults: T) => React.ReactNode;
}) {
  const [draft, setDraft] = useState<T>(value);
  const [fieldErrs, setFieldErrs] = useState<FieldError[]>([]);
  const [busy, setBusy] = useState(false);
  const [resetting, setResetting] = useState(false);
  const { notify, node } = useNotice();
  useEffect(() => setDraft(value), [value]);
  const dirty = !same(draft, value);

  // 离开页面前提示未保存的修改
  useEffect(() => {
    if (!dirty) return;
    const h = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener('beforeunload', h);
    return () => window.removeEventListener('beforeunload', h);
  }, [dirty]);

  const errOf = (path: string) => fieldErrs.find((f) => f.field === path || f.field.startsWith(path + '.'))?.message;

  const submit = async () => {
    setBusy(true);
    setFieldErrs([]);
    try {
      const res = await save(draft);
      onSaved(res);
      notify('success', section === 'frontend' ? '已保存并生效，前端用户刷新页面后看到新配置' : '已保存并生效');
    } catch (e) {
      const ce = toCenterError(e);
      if (ce.fields) setFieldErrs(ce.fields);
      notify('error', ce.fields ? `校验失败：${ce.fields.map((f) => `${f.field} ${f.message}`).join('；')}` : ce.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {node}
      <div className="c-muted c-settings-meta">
        {meta.updatedAt
          ? `最后修改：${formatTime(meta.updatedAt)}${meta.updatedBy === 'migration' ? '（从 config.yml 导入）' : ''}`
          : meta.updatedBy === 'migration'
            ? '从 config.yml 导入'
            : '当前使用默认值'}
        <button className="c-link" onClick={() => setResetting(true)}>
          全部恢复默认
        </button>
      </div>
      {groups.map((g) => (
        <Card key={g.title} title={g.title}>
          {g.desc && <p className="c-help">{g.desc}</p>}
          <div className="c-settings-grid">
            {g.fields.map((f) => (
              <SettingField
                key={f.path}
                def={f}
                value={getPath(draft, f.path)}
                defaultValue={getPath(defaults, f.path)}
                changed={!same(getPath(draft, f.path), getPath(value, f.path))}
                error={errOf(f.path)}
                onChange={(v) => setDraft(setPath(draft, f.path, v))}
              />
            ))}
          </div>
        </Card>
      ))}
      {extra?.(draft, setDraft, defaults)}
      <div className={`c-savebar ${dirty ? 'show' : ''}`} aria-hidden={!dirty}>
        <span>有未保存的修改</span>
        <button className="c-btn" onClick={() => { setDraft(value); setFieldErrs([]); }} disabled={busy}>
          放弃
        </button>
        <button className="c-btn primary" onClick={submit} disabled={busy}>
          {busy ? '保存中…' : '保存'}
        </button>
      </div>
      {resetting && (
        <ConfirmModal
          title="恢复默认配置"
          confirmText="恢复默认"
          message={<p>将「{SECTION_LABELS[section]}」全部配置恢复为默认值并立即生效。</p>}
          onConfirm={async () => {
            onSaved(await centerApi.resetSettings(section));
            notify('success', '已恢复默认');
          }}
          onClose={() => setResetting(false)}
        />
      )}
    </>
  );
}

function SettingField({
  def,
  value,
  defaultValue,
  changed,
  error,
  onChange,
}: {
  def: FieldDef;
  value: unknown;
  defaultValue: unknown;
  changed: boolean;
  error?: string;
  onChange: (v: unknown) => void;
}) {
  const id = `set-${def.path}`;
  const isDefault = same(value, defaultValue);
  const str = value == null ? '' : String(value);
  let input: React.ReactNode;
  switch (def.type) {
    case 'number':
      input = (
        <input
          id={id}
          className="c-input"
          type="number"
          step={def.step}
          min={def.min}
          max={def.max}
          value={str}
          onChange={(e) => onChange(e.target.value === '' ? 0 : Number(e.target.value))}
        />
      );
      break;
    case 'color':
      input = (
        <div className="c-inline">
          <input type="color" className="c-color" value={/^#[0-9a-fA-F]{6}$/.test(str) ? str : '#ffd400'} onChange={(e) => onChange(e.target.value)} aria-label={def.label} />
          <input id={id} className="c-input c-mono" value={str} onChange={(e) => onChange(e.target.value)} />
        </div>
      );
      break;
    case 'image':
      input = (
        <div className="c-image-field">
          <img src={str} alt="" className="c-image-preview" />
          <textarea id={id} className="c-input c-mono" rows={2} value={str} onChange={(e) => onChange(e.target.value)} />
          <label className="c-btn small">
            上传 SVG / PNG
            <input
              type="file"
              accept="image/svg+xml,image/png"
              hidden
              onChange={(e) => {
                const file = e.target.files?.[0];
                if (!file) return;
                if (file.size > 512 * 1024) {
                  alert('图标不能超过 512KB');
                  return;
                }
                const reader = new FileReader();
                reader.onload = () => onChange(String(reader.result));
                reader.readAsDataURL(file);
                e.target.value = '';
              }}
            />
          </label>
        </div>
      );
      break;
    default:
      input = <input id={id} className="c-input" value={str} onChange={(e) => onChange(e.target.value)} />;
  }
  return (
    <div className={`c-setting ${changed ? 'changed' : ''} ${error ? 'invalid' : ''}`}>
      <label className="c-label" htmlFor={id}>
        {def.label}
        {changed && <span className="c-dot" title="已修改" />}
      </label>
      {input}
      <div className="c-setting-foot">
        <span className="c-help">
          {error ? <span className="c-error-text">{error}</span> : def.help}
          {!error && def.type !== 'image' && <span className="c-muted"> 默认：{String(defaultValue ?? '空')}</span>}
        </span>
        {!isDefault && (
          <button type="button" className="c-link" onClick={() => onChange(structuredClone(defaultValue))}>
            恢复默认
          </button>
        )}
      </div>
    </div>
  );
}

const TILE_FIELDS: FieldDef[] = [
  { path: 'tileUrl', label: '瓦片 URL', type: 'text', help: '含 {z}/{x}/{y}，留空则无底图' },
  { path: 'scheme', label: '瓦片坐标方案', type: 'text', help: 'xyz 或 tms' },
  { path: 'zoom', label: '初始缩放', type: 'number', step: 0.5 },
  { path: 'tileSize', label: '瓦片尺寸（px）', type: 'number', step: 1, min: 0 },
  { path: 'opacity', label: '不透明度', type: 'number', step: 0.05, min: 0, max: 1 },
  { path: 'minZoom', label: '最小缩放', type: 'number', step: 0.5 },
  { path: 'maxZoom', label: '最大缩放', type: 'number', step: 0.5 },
  { path: 'minNativeZoom', label: '最小原生缩放', type: 'number', step: 1 },
  { path: 'maxNativeZoom', label: '最大原生缩放', type: 'number', step: 1 },
  { path: 'mapScale', label: '缩放比例', type: 'number', step: 0.1, help: '1 方块对应的原生瓦片像素' },
];

/** 世界底图：可增删世界，每个世界一组瓦片参数（mapOffset / center 为 [x, z]）。 */
function WorldTilesEditor<T extends object>({ value, set }: { value: T; set: (v: T) => void; defaults: T }) {
  const tiles = (getPath(value, 'worldTiles') ?? {}) as FrontendConfig['worldTiles'];
  const [newName, setNewName] = useState('');
  const names = useMemo(() => Object.keys(tiles).sort(), [tiles]);
  const update = (next: FrontendConfig['worldTiles']) => set(setPath(value, 'worldTiles', next));
  const pair = (v?: number[]) => (v && v.length === 2 ? `${v[0]}, ${v[1]}` : '');
  const parsePair = (s: string): [number, number] | undefined => {
    const parts = s.split(/[,\s]+/).filter(Boolean).map(Number);
    return parts.length === 2 && parts.every(Number.isFinite) ? [parts[0], parts[1]] : undefined;
  };
  return (
    <Card
      title="世界底图"
      extra={
        <form
          className="c-inline"
          onSubmit={(e) => {
            e.preventDefault();
            const n = newName.trim();
            if (!n || tiles[n]) return;
            update({ ...tiles, [n]: { zoom: 14 } });
            setNewName('');
          }}
        >
          <input className="c-input" placeholder="新世界名" value={newName} onChange={(e) => setNewName(e.target.value)} aria-label="新世界名" />
          <button className="c-btn small" type="submit">
            添加世界
          </button>
        </form>
      }
    >
      {names.map((w) => (
        <details key={w} className="c-world" open={names.length === 1}>
          <summary>
            <b>{w}</b>
            <span className="c-muted c-ellipsis"> {tiles[w].tileUrl || '无底图'}</span>
          </summary>
          <div className="c-settings-grid">
            {TILE_FIELDS.map((f) => (
              <SettingField
                key={f.path}
                def={f}
                value={getPath(tiles[w], f.path)}
                defaultValue={undefined}
                changed={false}
                onChange={(v) => update({ ...tiles, [w]: { ...tiles[w], [f.path]: v === '' ? undefined : v } })}
              />
            ))}
            <div className="c-setting">
              <label className="c-label">坐标偏移 [x, z]</label>
              <input
                className="c-input"
                defaultValue={pair(tiles[w].mapOffset)}
                placeholder="0, 0"
                onBlur={(e) => update({ ...tiles, [w]: { ...tiles[w], mapOffset: parsePair(e.target.value) } })}
              />
            </div>
            <div className="c-setting">
              <label className="c-label">初始镜头中心 [x, z]</label>
              <input
                className="c-input"
                defaultValue={pair(tiles[w].center)}
                placeholder="留空则使用数据范围中心"
                onBlur={(e) => update({ ...tiles, [w]: { ...tiles[w], center: parsePair(e.target.value) } })}
              />
            </div>
          </div>
          <button
            className="c-btn small danger-ghost"
            disabled={names.length <= 1}
            onClick={() => {
              const next = { ...tiles };
              delete next[w];
              update(next);
            }}
          >
            删除世界 {w}
          </button>
        </details>
      ))}
    </Card>
  );
}
