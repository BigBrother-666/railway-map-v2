/**
 * 服务器配置：按 schema 渲染 frontend（前端运行时配置）、route（寻路参数）、plugin（插件链路）、realtime（实时列车）、
 * tiles（世界底图：外部地图来源 + 静态瓦片）五段的分组表单。
 * 每项显示默认值、单项恢复默认；改动高亮，底部固定「保存 / 放弃」栏。保存后后端立即生效，前端用户刷新页面后生效。
 */
import { useEffect, useMemo, useState } from 'react';
import { centerApi, type FieldError } from '../api';
import type {
  FrontendSettings,
  PluginSettings,
  RealtimeSettings,
  RouteSettings,
  SettingsResponse,
  SettingsSection,
  ExternalLayerSettings,
  TileSourceSettings,
  TileSourcesSettings,
  TileSourcesStatus,
  WorldLayerSettings,
} from '../types';
import type { WorldTileConfig } from '../../types';
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
  /** 数值输入清空时传 ''（由调用方视为「未配置」），而不是 0。 */
  optional?: boolean;
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
      { path: 'defaultWorld', label: '默认世界', type: 'text', help: '必须是「世界底图」中已配置的世界，或外部地图来源中自动发现的世界' },
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
      { path: 'mapStyle.stationFocusZoom', label: '搜索车站时缩放级别', type: 'number', step: 0.5, min: 0 },
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

const TILES_GROUPS: GroupDef[] = [
  {
    title: '外部地图瓦片代理与缓存',
    desc: '外部地图的瓦片经后端代理（上游一般不允许跨域），并缓存在后端内存中。保存后立即生效；来源列表变化时清空缓存并重新读取世界列表。',
    fields: [
      { path: 'cacheMaxMb', label: '缓存上限（MB）', type: 'number', step: 16, min: 0, max: 4096, help: '后端内存中瓦片缓存的总大小（含合成的瓦片）' },
      { path: 'cacheTtlSeconds', label: '上游瓦片缓存有效期（秒）', type: 'number', step: 60, min: 0, help: '过期后先返回旧瓦片、后台重新拉取，地图更新才会显示' },
      { path: 'synthCacheTtlSeconds', label: '合成瓦片缓存有效期（秒）', type: 'number', step: 600, min: 0, help: '合成瓦片重新生成代价高，宜设得较长' },
      { path: 'blankCacheTtlSeconds', label: '空白瓦片缓存有效期（秒）', type: 'number', step: 600, min: 0, help: '上游没有的瓦片（未渲染区域）' },
      { path: 'browserCacheSeconds', label: '浏览器缓存（秒）', type: 'number', step: 60, min: 0, help: '瓦片响应的 Cache-Control max-age' },
      { path: 'discoveryRefreshSeconds', label: '世界列表刷新间隔（秒）', type: 'number', step: 60, min: 10, help: '重新读取上游的世界 / 地图列表' },
      { path: 'upstreamTimeoutSeconds', label: '上游请求超时（秒）', type: 'number', step: 1, min: 0 },
      { path: 'upstreamConcurrency', label: '上游最大并发请求数', type: 'number', step: 1, min: 0, max: 64, help: '避免拖垮上游地图服务器' },
      { path: 'renderJpegQuality', label: '合成瓦片 JPEG 质量', type: 'number', step: 5, min: 1, max: 100, help: '后端合成瓦片的编码质量' },
    ],
  },
];

const SECTION_LABELS: Record<SettingsSection, string> = {
  frontend: '前端显示',
  route: '路线查询',
  plugin: '插件链路',
  realtime: '实时列车',
  tiles: '世界底图',
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
      <PageHeader title="服务器配置" sub="原 config.yml 中的 frontend / route / plugin / realtime 段及外部地图接入，保存在数据库中。" />
      <div className="c-tabs" role="tablist">
        {(Object.keys(SECTION_LABELS) as SettingsSection[]).map((s) => (
          <button key={s} role="tab" aria-selected={tab === s} className={`c-tab ${tab === s ? 'active' : ''}`} onClick={() => setTab(s)}>
            {SECTION_LABELS[s]}
          </button>
        ))}
      </div>
      {tab === 'frontend' && (
        <SectionForm<FrontendSettings>
          key="frontend"
          section="frontend"
          value={data.frontend}
          defaults={data.defaults.frontend}
          meta={data.frontendMeta}
          groups={FRONTEND_GROUPS}
          save={(v) => centerApi.saveFrontend(v)}
          onSaved={setData}
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
      {tab === 'tiles' && (
        <SectionForm<TileSourcesSettings>
          key="tiles"
          section="tiles"
          value={data.tiles}
          defaults={data.defaults.tiles}
          meta={data.tilesMeta}
          groups={TILES_GROUPS}
          save={(v) => centerApi.saveTiles(v)}
          onSaved={setData}
          extraFirst
          extra={(v, set) => <TilesEditors value={v} set={set} />}
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
  extraFirst,
}: {
  section: SettingsSection;
  value: T;
  defaults: T;
  meta: { updatedBy?: string; updatedAt?: number };
  groups: GroupDef[];
  save: (v: T) => Promise<SettingsResponse>;
  onSaved: (r: SettingsResponse) => void;
  extra?: (v: T, set: (v: T) => void, defaults: T) => React.ReactNode;
  /** extra 渲染在分组表单之前（默认之后）。 */
  extraFirst?: boolean;
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
      {extraFirst && extra?.(draft, setDraft, defaults)}
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
      {!extraFirst && extra?.(draft, setDraft, defaults)}
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
          onChange={(e) => onChange(e.target.value === '' ? (def.optional ? '' : 0) : Number(e.target.value))}
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

/** 静态瓦片参数（坐标偏移 / 镜头中心另行编辑）。 */
const STATIC_FIELDS: FieldDef[] = [
  { path: 'tileUrl', label: '瓦片 URL', type: 'text', help: '含 {z}/{x}/{y}，留空则无底图' },
  { path: 'scheme', label: '瓦片坐标方案', type: 'text', help: 'xyz 或 tms' },
  { path: 'zoom', label: '初始缩放', type: 'number', step: 0.5 },
  { path: 'tileSize', label: '瓦片尺寸（px）', type: 'number', step: 1, min: 0 },
  { path: 'opacity', label: '不透明度', type: 'number', step: 0.05, min: 0, max: 1 },
  { path: 'minZoom', label: '最小缩放', type: 'number', step: 0.5 },
  { path: 'maxZoom', label: '最大缩放', type: 'number', step: 0.5 },
  { path: 'minNativeZoom', label: '最小原生缩放', type: 'number', step: 1, help: '瓦片最粗一级' },
  { path: 'maxNativeZoom', label: '最大原生缩放', type: 'number', step: 1, help: '瓦片最精细一级；再放大时拉伸显示' },
  { path: 'mapScale', label: '缩放比例', type: 'number', step: 0.1, help: '1 方块对应的原生瓦片像素' },
];

/** 外部地图参数（对齐参数由外部地图推导；镜头中心另行编辑）。 */
const EXTERNAL_FIELDS: FieldDef[] = [
  { path: 'zoom', label: '初始缩放', type: 'number', step: 0.5 },
  { path: 'opacity', label: '不透明度', type: 'number', step: 0.05, min: 0, max: 1 },
  { path: 'minZoom', label: '最小缩放', type: 'number', step: 0.5 },
  { path: 'maxZoom', label: '最大缩放', type: 'number', step: 0.5 },
  { path: 'synthZoomLevels', label: '合成缩小层级数', type: 'number', step: 1, min: 0, max: 6, help: '在外部地图最粗一级之上由后端拼接合成的层级数，越大地图能缩得越远（每多一级，最远一级需要的上游瓦片 ×4）；0 不合成，留空为 4' },
  { path: 'synthMarginBlocks', label: '合成范围边距（方块）', type: 'number', step: 500, min: 0, help: '只合成线路 / 车站范围向外扩展该距离以内的区域，范围外的缩小层级显示为空白；留空为 2000' },
];

const DEFAULT_SYNTH_LEVELS = 4;

type TilesEditorProps = { value: TileSourcesSettings; set: (v: TileSourcesSettings) => void };
type SourceStatus = TileSourcesStatus['sources'][number];

/** 外部地图推导值（与后端 externalTile 一致：原生缩放下限按合成层级扩展），用于提示留空时的取值。 */
function derivedExternal(native: WorldTileConfig | undefined, ext: ExternalLayerSettings): WorldTileConfig | undefined {
  if (!native) return undefined;
  const n = Math.min(ext.synthZoomLevels ?? DEFAULT_SYNTH_LEVELS, native.minNativeZoom ?? 0);
  return { ...native, minZoom: (native.minZoom ?? 0) - n };
}

function externalHelp(derived: WorldTileConfig | undefined, f: FieldDef): string | undefined {
  const v = derived ? getPath(derived, f.path) : undefined;
  if (v === undefined) return f.help;
  return f.path === 'minZoom' ? `留空使用推导值 ${v}；不能低于它（再缩小没有底图）` : `留空使用外部地图推导值 ${v}`;
}

/**
 * 各世界生效的底图列表：已保存的列表，末尾追加来源发现了、但列表中还没有的外部地图（按来源顺序）。
 * 与后端 tilesource.EffectiveLayers 一致；做任何编辑后，自动追加的条目会一并保存。
 */
function effectiveLayers(worlds: TileSourcesSettings['worlds'], status: TileSourcesStatus | null | undefined) {
  const out: TileSourcesSettings['worlds'] = { ...worlds };
  for (const src of status?.sources ?? []) {
    if (src.error) continue;
    for (const w of src.worlds) {
      if (!w.map) continue;
      const layers = out[w.name] ?? [];
      if (!layers.some((l) => l.external?.source === src.id)) out[w.name] = [...layers, { external: { source: src.id } }];
    }
  }
  return out;
}

/** 世界底图页：外部地图来源 + 每个世界的底图列表，共用一份来源发现状态。 */
function TilesEditors(props: TilesEditorProps) {
  const { data: status, reload } = useAsync(() => centerApi.tilesStatus(), []);
  return (
    <>
      <TileSourcesEditor {...props} status={status} reload={reload} />
      <WorldTilesEditor {...props} status={status} />
    </>
  );
}

/** 每个世界的底图列表：静态瓦片手动添加，外部地图按来源自动出现；上下顺序即优先级。 */
function WorldTilesEditor({ value, set, status }: TilesEditorProps & { status: TileSourcesStatus | null | undefined }) {
  const worlds = useMemo(() => effectiveLayers(value.worlds ?? {}, status), [value.worlds, status]);
  const names = Object.keys(worlds).sort();
  const [newName, setNewName] = useState('');
  // 展开状态受控：新添加的世界 / 静态瓦片所在世界自动展开，否则新条目落在折叠的世界里看不到
  const [opened, setOpened] = useState<Set<string>>(() => new Set(names.length === 1 ? names : []));
  const openWorld = (w: string) => setOpened((o) => new Set(o).add(w));
  const setLayers = (w: string, layers: WorldLayerSettings[]) => {
    const next = { ...worlds, [w]: layers };
    if (layers.length === 0) delete next[w];
    set({ ...value, worlds: next });
  };
  const addStatic = (w: string) => {
    setLayers(w, [...(worlds[w] ?? []), { static: { zoom: 14 } }]);
    openWorld(w);
  };
  const sourceOf = (id: string) => status?.sources.find((s) => s.id === id);
  return (
    <Card
      title="世界参数"
      extra={
        <form
          className="c-inline"
          onSubmit={(e) => {
            e.preventDefault();
            const n = newName.trim();
            if (!n) return;
            if (worlds[n]) openWorld(n);
            else addStatic(n);
            setNewName('');
          }}
        >
          <input
            className="c-input"
            style={{ width: 160 }}
            placeholder="新世界名"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            aria-label="新世界名"
          />
          <button className="c-btn small" type="submit" disabled={!newName.trim()}>
            添加世界
          </button>
        </form>
      }
    >
      <p className="c-help">
        每个世界可以有多个底图配置，从上到下依次尝试，使用第一个可用的：外部地图在来源读取失败或来源中没有该世界时跳过，静态瓦片始终可用。外部地图按来源自动出现，静态瓦片在世界中手动添加；两类配置各自独立保存。来源中没有的世界可以用「添加世界」新建。
      </p>
      {names.map((w) => (
        <details
          key={w}
          className="c-world"
          open={opened.has(w)}
          onToggle={(e) => {
            const isOpen = e.currentTarget.open;
            setOpened((o) => {
              const next = new Set(o);
              if (isOpen) next.add(w);
              else next.delete(w);
              return next;
            });
          }}
        >
          <summary>
            <b>{w}</b>
            <span className="c-muted c-ellipsis"> {worlds[w].map((l) => layerLabel(l)).join(' → ')}</span>
          </summary>
          {worlds[w].map((layer, i) => (
            <LayerEditor
              key={layer.external ? `ext-${layer.external.source}` : `static-${i}`}
              layer={layer}
              index={i}
              source={layer.external ? sourceOf(layer.external.source) : undefined}
              world={w}
              onChange={(l) => setLayers(w, worlds[w].map((x, j) => (j === i ? l : x)))}
              onMove={(to) => setLayers(w, move(worlds[w], i, to))}
              onDelete={() => setLayers(w, worlds[w].filter((_, j) => j !== i))}
              count={worlds[w].length}
            />
          ))}
          <button className="c-btn small" type="button" onClick={() => addStatic(w)}>
            添加静态瓦片
          </button>
        </details>
      ))}
    </Card>
  );
}

function layerLabel(l: WorldLayerSettings): string {
  return l.external ? `外部地图 ${l.external.source}` : `静态瓦片${l.static?.tileUrl ? '' : '（无 URL）'}`;
}

/** 单个底图配置：标题行（类型、状态、排序 / 删除）+ 参数。 */
function LayerEditor({
  layer,
  index,
  count,
  source,
  world,
  onChange,
  onMove,
  onDelete,
}: {
  layer: WorldLayerSettings;
  index: number;
  count: number;
  source?: SourceStatus;
  world: string;
  onChange: (l: WorldLayerSettings) => void;
  onMove: (to: number) => void;
  onDelete: () => void;
}) {
  const ext = layer.external;
  const params: Record<string, unknown> = (ext ?? layer.static ?? {}) as Record<string, unknown>;
  const patch = (key: string, v: unknown) => {
    const next = { ...params, [key]: v === '' ? undefined : v };
    onChange(ext ? { external: next as unknown as ExternalLayerSettings } : { static: next as WorldTileConfig });
  };
  const sw = ext ? source?.worlds.find((x) => x.name === world) : undefined;
  const derived = ext ? derivedExternal(sw?.tile, ext) : undefined;
  // 外部地图状态：与后端选择规则一致（来源读取失败或没有该世界的俯视地图时跳过）
  const extState = !ext
    ? null
    : !source
      ? { cls: 'warn', text: '状态未知（保存后刷新状态）' }
      : source.error
        ? { cls: 'err', text: '来源读取失败，跳过' }
        : sw?.map
          ? { cls: 'ok', text: `可用 · 地图 ${sw.map}` }
          : { cls: 'err', text: sw ? `不可用（${sw.skip}），跳过` : '来源中没有该世界，跳过' };
  const pair = (v?: number[]) => (v && v.length === 2 ? `${v[0]}, ${v[1]}` : '');
  const parsePair = (s: string): [number, number] | undefined => {
    const parts = s.split(/[,\s]+/).filter(Boolean).map(Number);
    return parts.length === 2 && parts.every(Number.isFinite) ? [parts[0], parts[1]] : undefined;
  };
  return (
    <div className="c-world">
      <div className="c-inline">
        <b>
          {index + 1}. {ext ? `外部地图 · ${ext.source}` : '静态瓦片'}
        </b>
        {extState && <span className={`c-tag ${extState.cls}`}>{extState.text}</span>}
        <span style={{ flex: 1 }} />
        <button className="c-btn small" type="button" disabled={index === 0} onClick={() => onMove(index - 1)} aria-label="上移">
          上移
        </button>
        <button className="c-btn small" type="button" disabled={index === count - 1} onClick={() => onMove(index + 1)} aria-label="下移">
          下移
        </button>
        {!ext && (
          <button className="c-btn small danger-ghost" type="button" onClick={onDelete}>
            删除
          </button>
        )}
      </div>
      <div className="c-settings-grid">
        {(ext ? EXTERNAL_FIELDS : STATIC_FIELDS).map((f) => (
          <SettingField
            key={f.path}
            def={{ ...f, optional: true, help: ext ? externalHelp(derived, f) : f.help }}
            value={params[f.path]}
            defaultValue={undefined}
            changed={false}
            onChange={(v) => patch(f.path, v)}
          />
        ))}
        {!ext && (
          <div className="c-setting">
            <label className="c-label">坐标偏移 [x, z]</label>
            <input
              key={pair(params.mapOffset as number[] | undefined)} // 排序后值变化时重新挂载（非受控输入）
              className="c-input"
              defaultValue={pair(params.mapOffset as number[] | undefined)}
              placeholder="0, 0"
              onBlur={(e) => patch('mapOffset', parsePair(e.target.value))}
            />
          </div>
        )}
        <div className="c-setting">
          <label className="c-label">初始镜头中心 [x, z]</label>
          <input
            key={pair(params.center as number[] | undefined)}
            className="c-input"
            defaultValue={pair(params.center as number[] | undefined)}
            placeholder="留空则使用数据范围中心"
            onBlur={(e) => patch('center', parsePair(e.target.value))}
          />
        </div>
      </div>
    </div>
  );
}

const SOURCE_TYPES = [{ value: 'dynmap', label: 'Dynmap' }];

/** 外部地图来源列表 + 当前发现状态（世界匹配情况）。 */
function TileSourcesEditor({
  value,
  set,
  status,
  reload,
}: TilesEditorProps & { status: TileSourcesStatus | null | undefined; reload: () => void }) {
  const sources = value.sources ?? [];
  const update = (i: number, patch: Partial<TileSourceSettings>) =>
    set({ ...value, sources: sources.map((s, j) => (j === i ? { ...s, ...patch } : s)) });
  // 世界参数中引用来源 ID 的外部地图配置随来源改名 / 删除同步（rename 为 null 表示删除）
  const remapWorlds = (from: string, to: string | null) => {
    const worlds: TileSourcesSettings['worlds'] = {};
    for (const [w, layers] of Object.entries(value.worlds ?? {})) {
      const next = layers.flatMap((l) =>
        l.external?.source !== from ? [l] : to === null ? [] : [{ external: { ...l.external, source: to } }],
      );
      if (next.length) worlds[w] = next;
    }
    return worlds;
  };
  const nextId = () => {
    for (let n = sources.length + 1; ; n++) if (!sources.some((s) => s.id === `map${n}`)) return `map${n}`;
  };
  return (
    <Card
      title="外部地图来源"
      extra={
        <button className="c-btn small" type="button" onClick={() => set({ ...value, sources: [...sources, { id: nextId(), type: 'dynmap', url: '' }] })}>
          添加来源
        </button>
      }
    >
      <p className="c-help">
        填写网页地图的前端地址（如 dynmap 页面地址），后端自动读取其中的世界，按世界名与线路数据对应，并在下方「世界参数」中为这些世界自动添加外部地图配置。只使用正南朝上的俯视地图（如 dynmap 的 flat），等距视角地图无法与线路对齐。
      </p>
      {sources.length === 0 && <p className="c-muted">未配置外部地图来源。</p>}
      {sources.map((src, i) => (
        <div key={i} className="c-world">
          <div className="c-settings-grid">
            <div className="c-setting">
              <label className="c-label" htmlFor={`tile-src-${i}-url`}>前端地址</label>
              <input id={`tile-src-${i}-url`} className="c-input" value={src.url} placeholder="https://map.example.com/dynmap/" onChange={(e) => update(i, { url: e.target.value.trim() })} />
            </div>
            <div className="c-setting">
              <label className="c-label" htmlFor={`tile-src-${i}-type`}>类型</label>
              <select id={`tile-src-${i}-type`} className="c-input" value={src.type} onChange={(e) => update(i, { type: e.target.value })}>
                {SOURCE_TYPES.map((t) => (
                  <option key={t.value} value={t.value}>{t.label}</option>
                ))}
              </select>
            </div>
            <div className="c-setting">
              <label className="c-label" htmlFor={`tile-src-${i}-id`}>标识</label>
              <input id={`tile-src-${i}-id`} className="c-input c-mono" value={src.id} onChange={(e) => {
                  const id = e.target.value.trim();
                  set({ ...value, sources: sources.map((s, j) => (j === i ? { ...s, id } : s)), worlds: remapWorlds(src.id, id) });
                }} />
              <span className="c-help">字母、数字、- 和 _，出现在瓦片代理地址中</span>
            </div>
            <div className="c-setting">
              <label className="c-label" htmlFor={`tile-src-${i}-map`}>地图名（可选）</label>
              <input id={`tile-src-${i}-map`} className="c-input c-mono" value={src.map ?? ''} placeholder="自动选第一张俯视地图" onChange={(e) => update(i, { map: e.target.value.trim() || undefined })} />
            </div>
          </div>
          <SourceStatusView status={status?.sources.find((s) => s.id === src.id && s.url === src.url)} />
          <div className="c-inline">
            <button
              className="c-btn small danger-ghost"
              type="button"
              onClick={() => set({ ...value, sources: sources.filter((_, j) => j !== i), worlds: remapWorlds(src.id, null) })}
            >
              删除来源
            </button>
          </div>
        </div>
      ))}
      {status && (
        <div className="c-inline c-muted">
          <span>后端瓦片缓存：{status.cacheItems} 张，{(status.cacheBytes / 1048576).toFixed(1)} MB</span>
          <button className="c-link" type="button" onClick={reload}>
            刷新状态
          </button>
        </div>
      )}
    </Card>
  );
}

function SourceStatusView({ status }: { status?: TileSourcesStatus['sources'][number] }) {
  if (!status) return <p className="c-help">保存后后端将读取该来源的世界列表，可点「刷新状态」查看。</p>;
  return (
    <div className="c-help">
      {status.error && <p className="c-error-text">读取失败：{status.error}</p>}
      {status.discoveredAt && <p className="c-muted">上次读取成功：{formatTime(status.discoveredAt)}</p>}
      <ul>
        {status.worlds.map((w) => (
          <li key={w.name}>
            <b>{w.name}</b> {w.map ? `→ 地图 ${w.map}` : <span className="c-muted">不可用（{w.skip}）</span>}
          </li>
        ))}
      </ul>
    </div>
  );
}

function move<T>(arr: T[], from: number, to: number): T[] {
  const next = [...arr];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}
