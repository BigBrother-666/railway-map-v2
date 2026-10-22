// Package config 加载后端配置（yaml + 环境变量覆盖，见 docs/BACKEND_PROMPT.md §10）。
// frontend / route / plugin（除 sharedToken）/ realtime 已迁移到数据库（见 internal/settings），这里只保留类型定义与默认值。
package config

import (
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是后端总配置。
type Config struct {
	Server ServerConfig `yaml:"server"`
	Log    LogConfig    `yaml:"log"`
	Plugin PluginConfig `yaml:"plugin"`
	Auth   AuthConfig   `yaml:"auth"`
	DB     DBConfig     `yaml:"db"`

	// Legacy 是旧版 config.yml 中仍残留的 frontend / route / plugin / realtime 段（已迁移到数据库，由 web 端服务器配置维护）。
	// 只用于首次启动时一次性导入数据库，不参与运行时配置。
	Legacy LegacySections `yaml:"-"`
}

// LegacySections 是旧版 config.yml 中的 frontend / route / plugin / realtime 段；对应段不存在时为 nil。
// plugin 段只有 sharedToken（仍留在 config.yml）时 Plugin 为 nil。
type LegacySections struct {
	Frontend *FrontendConfig
	Route    *RouteConfig
	Plugin   *PluginSettings
	Realtime *RealtimeConfig
	// Tiles 只含旧版 frontend.worldTiles。
	Tiles *TilesConfig
}

type ServerConfig struct {
	Addr          string `yaml:"addr"`
	PublicBaseURL string `yaml:"publicBaseUrl"`
	// FrontendBaseURL 是地图页面的访问地址，OAuth 回调完成后跳转回此处（带 ?login=... 提示）。
	// 留空则回退到 PublicBaseURL（生产同源反代场景）；开发时前端独立端口需单独配置。
	FrontendBaseURL string `yaml:"frontendBaseUrl"`
}

type LogConfig struct {
	Level string `yaml:"level"`
	Dir   string `yaml:"dir"`
}

// PluginConfig 是 config.yml 中 plugin 段仍保留的部分（密钥不入库）。
type PluginConfig struct {
	SharedToken string `yaml:"sharedToken"`
}

// PluginSettings 是插件链路的可编辑参数（数据库 app_settings 的 plugin 段）。
type PluginSettings struct {
	// HeartbeatSeconds 是后端向插件发送 ping 的间隔秒数。<=0 用默认 15。
	HeartbeatSeconds int `yaml:"heartbeatSeconds" json:"heartbeatSeconds"`
	// PurchaseTimeoutSeconds 是在线购票等待插件扣款/出票回执的超时秒数。<=0 用默认 10。
	PurchaseTimeoutSeconds int `yaml:"purchaseTimeoutSeconds" json:"purchaseTimeoutSeconds"`
	// PurchaseMinIntervalSeconds 是同一玩家两次购票的最小间隔（秒），防止频繁购票压垮服务器。
	// <=0 时用默认 3 秒。
	PurchaseMinIntervalSeconds int `yaml:"purchaseMinIntervalSeconds" json:"purchaseMinIntervalSeconds"`
	// ConfigTimeoutSeconds 是 web 端编辑铁路系统 / 线路 / 车站别名 / 车站组时等待插件回执的超时秒数。<=0 用默认 10。
	ConfigTimeoutSeconds int `yaml:"configTimeoutSeconds" json:"configTimeoutSeconds"`
}

// RealtimeConfig 是实时列车推送的可编辑参数（数据库 app_settings 的 realtime 段）。
type RealtimeConfig struct {
	// TrainTimeoutSeconds 是列车多久未更新就视为消失。<=0 用默认 30。
	TrainTimeoutSeconds int `yaml:"trainTimeoutSeconds" json:"trainTimeoutSeconds"`
	// ClientSendBuffer 是每个前端实时 WebSocket 连接的发送缓冲帧数（只影响新连接）。<=0 用默认 64。
	ClientSendBuffer int `yaml:"clientSendBuffer" json:"clientSendBuffer"`
}

// TilesConfig 是世界底图配置：每个世界一组按优先级排序的底图（静态瓦片 / 外部网页地图），以及外部地图来源。
// 后端为每个世界选出第一个可用的底图，经 GET /api/v1/config 的 worldTiles 下发；外部瓦片经后端代理
// （GET /api/v1/tiles/...）并缓存。
type TilesConfig struct {
	// Worlds 是每个世界的底图列表，顺序即优先级。外部来源发现的世界会自动出现对应的外部地图条目
	// （未保存过时排在最前，按来源顺序）；外部来源最近一次读取失败时跳过，使用下一个。
	Worlds map[string][]WorldLayerConfig `yaml:"worlds" json:"worlds"`
	// WorldTiles 是旧版每世界单一配置，只用于迁移（ApplyTilesDefaults 转换为 Worlds 后清空）。
	WorldTiles map[string]LegacyWorldTileConfig `yaml:"worldTiles,omitempty" json:"worldTiles,omitempty"`
	// Sources 按顺序生效：同一世界出现在多个来源时取第一个。
	Sources []TileSourceConfig `yaml:"sources" json:"sources"`
	// CacheMaxMB 是后端瓦片内存缓存上限（MB）。<=0 用默认 64。
	CacheMaxMB int `yaml:"cacheMaxMb" json:"cacheMaxMb"`
	// CacheTTLSeconds 是上游瓦片在后端缓存中的有效期（秒）。过期后仍先返回旧瓦片，同时在后台重新拉取。<=0 用默认 300。
	CacheTTLSeconds int `yaml:"cacheTtlSeconds" json:"cacheTtlSeconds"`
	// SynthCacheTTLSeconds 是后端合成瓦片的缓存有效期（秒）。合成瓦片由大量上游瓦片拼成、重新生成代价高，宜设得较长。<=0 用默认 10800。
	SynthCacheTTLSeconds int `yaml:"synthCacheTtlSeconds" json:"synthCacheTtlSeconds"`
	// BlankCacheTTLSeconds 是空白瓦片（上游没有该瓦片，即未渲染区域）的缓存有效期（秒）。<=0 用默认 10800。
	BlankCacheTTLSeconds int `yaml:"blankCacheTtlSeconds" json:"blankCacheTtlSeconds"`
	// BrowserCacheSeconds 是代理瓦片响应的 Cache-Control max-age（秒）。<=0 用默认 300。
	BrowserCacheSeconds int `yaml:"browserCacheSeconds" json:"browserCacheSeconds"`
	// DiscoveryRefreshSeconds 是重新读取上游元数据（世界 / 地图列表）的间隔（秒）。<=0 用默认 600。
	DiscoveryRefreshSeconds int `yaml:"discoveryRefreshSeconds" json:"discoveryRefreshSeconds"`
	// UpstreamTimeoutSeconds 是单次向上游请求元数据或瓦片的超时（秒）。<=0 用默认 10。
	UpstreamTimeoutSeconds int `yaml:"upstreamTimeoutSeconds" json:"upstreamTimeoutSeconds"`
	// UpstreamConcurrency 是同时向上游拉取瓦片的最大请求数，避免压垮上游地图服务器。<=0 用默认 8。
	UpstreamConcurrency int `yaml:"upstreamConcurrency" json:"upstreamConcurrency"`
	// RenderJPEGQuality 是后端合成瓦片的 JPEG 编码质量（1~100；子瓦片都是 JPEG 时才编码为 JPEG，否则 PNG）。<=0 用默认 85。
	RenderJPEGQuality int `yaml:"renderJpegQuality" json:"renderJpegQuality"`
}

// WorldLayerConfig 是某世界的一个底图配置，Static 与 External 恰有一个非空。
type WorldLayerConfig struct {
	Static   *WorldTileConfig     `yaml:"static,omitempty" json:"static,omitempty"`
	External *ExternalLayerConfig `yaml:"external,omitempty" json:"external,omitempty"`
}

// ExternalLayerConfig 是某世界使用某外部来源时的参数（对齐参数由外部地图推导，这里只有视图与合成参数）。
type ExternalLayerConfig struct {
	// Source 是外部来源 ID（TileSourceConfig.ID）。
	Source string `yaml:"source" json:"source"`
	// 视图参数，含义同 WorldTileConfig；0 / 空表示使用外部地图推导值。minZoom 不会低于有底图的推导下限。
	Zoom    float64   `yaml:"zoom,omitempty" json:"zoom,omitempty"`
	Opacity float64   `yaml:"opacity,omitempty" json:"opacity,omitempty"`
	MinZoom float64   `yaml:"minZoom,omitempty" json:"minZoom,omitempty"`
	MaxZoom float64   `yaml:"maxZoom,omitempty" json:"maxZoom,omitempty"`
	Center  []float64 `yaml:"center,omitempty" json:"center,omitempty"`
	// SynthZoomLevels 是在上游最粗一级之上由后端合成的层级数（每级由下一级 2×2 张拼接缩小），
	// 使地图能缩得更远（MapLibre 不会把低于原生缩放下限的瓦片缩小显示）。每多一级，最远一级每张瓦片
	// 需要的上游瓦片数 ×4。0 表示不合成；未配置用默认 DefaultSynthZoomLevels。
	SynthZoomLevels *int `yaml:"synthZoomLevels,omitempty" json:"synthZoomLevels,omitempty"`
	// SynthMarginBlocks：合成层级只覆盖该世界线路 / 车站坐标范围向外扩展该方块数以内的区域，
	// 范围外的合成瓦片直接返回空白、不访问上游。世界没有线路数据时不限制。<=0 用默认 DefaultSynthMarginBlocks。
	SynthMarginBlocks int `yaml:"synthMarginBlocks,omitempty" json:"synthMarginBlocks,omitempty"`
}

// 外部地图合成层级的默认值（见 ExternalLayerConfig.SynthZoomLevels / SynthMarginBlocks）。
const (
	DefaultSynthZoomLevels   = 4
	DefaultSynthMarginBlocks = 2000
)

// SynthLevels 返回生效的合成层级数。
func (e ExternalLayerConfig) SynthLevels() int {
	if e.SynthZoomLevels == nil {
		return DefaultSynthZoomLevels
	}
	return *e.SynthZoomLevels
}

// SynthMargin 返回生效的合成范围边距（方块）。
func (e ExternalLayerConfig) SynthMargin() float64 {
	if e.SynthMarginBlocks <= 0 {
		return DefaultSynthMarginBlocks
	}
	return float64(e.SynthMarginBlocks)
}

// LegacyWorldTileConfig 是旧版 worldTiles 条目（静态瓦片参数，曾兼作外部地图的视图 / 合成参数），只用于迁移。
type LegacyWorldTileConfig struct {
	WorldTileConfig   `yaml:",inline"`
	SynthZoomLevels   *int `yaml:"synthZoomLevels,omitempty" json:"synthZoomLevels,omitempty"`
	SynthMarginBlocks int  `yaml:"synthMarginBlocks,omitempty" json:"synthMarginBlocks,omitempty"`
}

// migrateLegacyWorldTiles 把旧版 worldTiles 转为 Worlds，保持旧版优先级「外部地图优先，其次静态瓦片」：
// 每个来源一条外部地图条目（沿用旧条目的视图 / 合成参数；来源中没有该世界时运行时自动跳过），
// 有 tileUrl 或没有任何来源时再加一条静态瓦片。
func migrateLegacyWorldTiles(t *TilesConfig) {
	if len(t.WorldTiles) == 0 || t.Worlds != nil {
		t.WorldTiles = nil
		return
	}
	t.Worlds = map[string][]WorldLayerConfig{}
	for name, old := range t.WorldTiles {
		var layers []WorldLayerConfig
		for _, src := range t.Sources {
			layers = append(layers, WorldLayerConfig{External: &ExternalLayerConfig{
				Source: src.ID, Zoom: old.Zoom, Opacity: old.Opacity, MinZoom: old.MinZoom, MaxZoom: old.MaxZoom,
				Center: old.Center, SynthZoomLevels: old.SynthZoomLevels, SynthMarginBlocks: old.SynthMarginBlocks,
			}})
		}
		if old.TileURL != "" || len(t.Sources) == 0 {
			static := old.WorldTileConfig
			layers = append(layers, WorldLayerConfig{Static: &static})
		}
		t.Worlds[name] = layers
	}
	t.WorldTiles = nil
}

// TileSourceConfig 是一个外部地图来源。
type TileSourceConfig struct {
	// ID 是来源标识（字母、数字、- 和 _），出现在瓦片代理路径中。
	ID string `yaml:"id" json:"id"`
	// Type 是来源类型，目前支持 dynmap。留空用 dynmap。
	Type string `yaml:"type" json:"type"`
	// URL 是网页地图前端地址，如 https://map.example.com/dynmap/。
	URL string `yaml:"url" json:"url"`
	// Map 可选：指定使用的地图名（dynmap 的 maps[].name，如 flat）；留空则自动选第一张俯视地图。
	Map string `yaml:"map,omitempty" json:"map,omitempty"`
}

type AuthConfig struct {
	Microsoft       MicrosoftConfig `yaml:"microsoft"`
	JWTSecret       string          `yaml:"jwtSecret"`
	TestAuthEnabled bool            `yaml:"testAuthEnabled"`
	TestAuthUUIDs   []string        `yaml:"testAuthUUIDs"`
	// SuperAdminUUID 是初始超级管理员的 Minecraft UUID（启动时自动授予内置 super_admin 角色，web 端不可撤销）。
	SuperAdminUUID string `yaml:"superAdminUuid"`
}

type MicrosoftConfig struct {
	ClientID     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
	RedirectPath string `yaml:"redirectPath"`
}

type DBConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
	Path   string `yaml:"path"`
}

type FrontendConfig struct {
	RealtimeWSPath string `yaml:"realtimeWsPath" json:"realtimeWsPath"`
	DefaultWorld   string `yaml:"defaultWorld" json:"defaultWorld"`
	CurrencyName   string `yaml:"currencyName" json:"currencyName"`
	// 主题色（强调色），16 进制 #RRGGBB。留空用默认黄色 #ffd400。
	ThemeColor string `yaml:"themeColor" json:"themeColor"`
	// 世界底图（worldTiles）已移到 TilesConfig，由 GET /api/v1/config 合并下发。
	MapStyle          MapStyleConfig     `yaml:"mapStyle" json:"mapStyle"`
	RouteDiagram      RouteDiagramConfig `yaml:"routeDiagram" json:"routeDiagram"`
	TrainIcons        TrainIconsConfig   `yaml:"trainIcons" json:"trainIcons"`
	DefaultSystemLogo string             `yaml:"defaultSystemLogo" json:"defaultSystemLogo"`
	AvatarURLTemplate string             `yaml:"avatarUrlTemplate" json:"avatarUrlTemplate"`
	DefaultPricePerKm float64            `yaml:"defaultPricePerKm" json:"defaultPricePerKm"`
	// 路线查询（前端请求 /api/v1/route/query）超时毫秒数，超时则中止请求并提示失败。<=0 用默认 10000。
	RouteSearchTimeoutMs int `yaml:"routeSearchTimeoutMs" json:"routeSearchTimeoutMs"`
}

// RouteConfig 是路线/车票查询相关的后端内部参数（不下发到前端，见 GET /api/v1/config 只返回 FrontendConfig）。
type RouteConfig struct {
	// QueryRateLimitPerSecond 是单个登录玩家每秒最多发起的路线查询请求数。<=0 用默认 3。
	QueryRateLimitPerSecond float64 `yaml:"queryRateLimitPerSecond" json:"queryRateLimitPerSecond"`

	// 搜索结果排序（复刻插件 search.*，与菜单购票一致）
	MaxDistanceResults   int     `yaml:"maxDistanceResults" json:"maxDistanceResults"`
	MaxPriceResults      int     `yaml:"maxPriceResults" json:"maxPriceResults"`
	SearchWeightDistance float64 `yaml:"searchWeightDistance" json:"searchWeightDistance"`
	SearchWeightPrice    float64 `yaml:"searchWeightPrice" json:"searchWeightPrice"`
	MinDirectResults     int     `yaml:"minDirectResults" json:"minDirectResults"`

	// 联程票（一次换乘 / 两段直达）寻路参数（复刻插件 search.max-transfer-* / transfer-min-improvement）
	MaxTransferResults     int     `yaml:"maxTransferResults" json:"maxTransferResults"`
	TransferMinImprovement float64 `yaml:"transferMinImprovement" json:"transferMinImprovement"`

	// ComputeTimeoutMs 是单次寻路计算（含直达 + 联程票枚举）的最长耗时，超时则中止该计算并返回
	// 错误、不写入结果缓存（避免把「算到一半被掐断」的不完整结果误当成「无路线」永久缓存）。这是
	// 后端自身的计算耗时上限，独立于前端 frontend.routeSearchTimeoutMs（那个只控制单个浏览器请求
	// 等待多久）：同一份计算可能被多个玩家的请求共享（见 singleflight 折叠），不能用某一个请求的
	// 耐心去卡它。<=0 用默认 15000。
	ComputeTimeoutMs int `yaml:"computeTimeoutMs" json:"computeTimeoutMs"`
}

type WorldTileConfig struct {
	TileURL       string    `yaml:"tileUrl,omitempty" json:"tileUrl,omitempty"`
	Zoom          float64   `yaml:"zoom,omitempty" json:"zoom,omitempty"`
	TileSize      int       `yaml:"tileSize,omitempty" json:"tileSize,omitempty"`
	Opacity       float64   `yaml:"opacity,omitempty" json:"opacity,omitempty"`
	MinNativeZoom float64   `yaml:"minNativeZoom,omitempty" json:"minNativeZoom,omitempty"`
	MaxNativeZoom float64   `yaml:"maxNativeZoom,omitempty" json:"maxNativeZoom,omitempty"`
	MinZoom       float64   `yaml:"minZoom,omitempty" json:"minZoom,omitempty"`
	MaxZoom       float64   `yaml:"maxZoom,omitempty" json:"maxZoom,omitempty"`
	Scheme        string    `yaml:"scheme,omitempty" json:"scheme,omitempty"`
	MapScale      float64   `yaml:"mapScale,omitempty" json:"mapScale,omitempty"`
	MapOffset     []float64 `yaml:"mapOffset,omitempty" json:"mapOffset,omitempty"`
	// 进入页面时的初始镜头中心（游戏坐标 [x, z]）。未配置则回退到数据范围中心。
	Center []float64 `yaml:"center,omitempty" json:"center,omitempty"`
}

type MapStyleConfig struct {
	LineWidth                 float64 `yaml:"lineWidth" json:"lineWidth"`
	HighlightWidth            float64 `yaml:"highlightWidth" json:"highlightWidth"`
	DimOpacity                float64 `yaml:"dimOpacity" json:"dimOpacity"`
	LineOpacity               float64 `yaml:"lineOpacity" json:"lineOpacity"`
	StationRadius             float64 `yaml:"stationRadius" json:"stationRadius"`
	StationStrokeWidth        float64 `yaml:"stationStrokeWidth" json:"stationStrokeWidth"`
	StationTextSize           float64 `yaml:"stationTextSize" json:"stationTextSize"`
	StationEnTextSize         float64 `yaml:"stationEnTextSize" json:"stationEnTextSize"` // 车站英文名（显示在中文名下方）的字号
	StationMergePixelDistance float64 `yaml:"stationMergePixelDistance" json:"stationMergePixelDistance"`
	TrainIconSize             float64 `yaml:"trainIconSize" json:"trainIconSize"`
	TrainFocusZoom            float64 `yaml:"trainFocusZoom" json:"trainFocusZoom"`
}

type RouteDiagramConfig struct {
	ProjectionThresholdBlocks float64 `yaml:"projectionThresholdBlocks" json:"projectionThresholdBlocks"`
	MinStationGapPx           float64 `yaml:"minStationGapPx" json:"minStationGapPx"` // 路线图相邻车站最小间距（px）
	MaxStationGapPx           float64 `yaml:"maxStationGapPx" json:"maxStationGapPx"` // 路线图相邻车站最大间距（px）
	TrainClusterProgress      float64 `yaml:"trainClusterProgress" json:"trainClusterProgress"`
	TrainIconScale            float64 `yaml:"trainIconScale" json:"trainIconScale"`
	TrackWidthPx              float64 `yaml:"trackWidthPx" json:"trackWidthPx"`                           // 路线图线路线条粗细（px），普通车与快速车共用
	ExpressMinHeightPx        float64 `yaml:"expressMinHeightPx" json:"expressMinHeightPx"`               // 快速车路线竖线最小高度（px）
	ExpressGradientBlendRatio float64 `yaml:"expressGradientBlendRatio" json:"expressGradientBlendRatio"` // 快速车路线相邻颜色渐变过渡长度占竖线总长的比例
}

type TrainIconsConfig struct {
	Express string `yaml:"express" json:"express"`
	Normal  string `yaml:"normal" json:"normal"`
}

// envPattern 匹配 ${VAR} 形式的环境变量引用。
var envPattern = regexp.MustCompile(`\$\{(\w+)\}`)

// Load 读取 yaml 配置文件，展开其中的 ${ENV} 引用，并填充默认值。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// 展开 ${ENV}（缺失则留空，便于在缺省时落到下面的默认值）
	expanded := envPattern.ReplaceAllStringFunc(string(raw), func(m string) string {
		name := envPattern.FindStringSubmatch(m)[1]
		return os.Getenv(name)
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, err
	}
	var legacy struct {
		Frontend *FrontendConfig `yaml:"frontend"`
		Route    *RouteConfig    `yaml:"route"`
		Plugin   map[string]any  `yaml:"plugin"`
		Realtime *RealtimeConfig `yaml:"realtime"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &legacy); err != nil {
		return nil, err
	}
	cfg.Legacy = LegacySections{Frontend: legacy.Frontend, Route: legacy.Route, Realtime: legacy.Realtime}
	// frontend.worldTiles 已移到 tiles 段（FrontendConfig 不再有该字段），单独解析。
	var legacyTiles struct {
		Frontend struct {
			WorldTiles map[string]LegacyWorldTileConfig `yaml:"worldTiles"`
		} `yaml:"frontend"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &legacyTiles); err != nil {
		return nil, err
	}
	if legacyTiles.Frontend.WorldTiles != nil {
		cfg.Legacy.Tiles = &TilesConfig{WorldTiles: legacyTiles.Frontend.WorldTiles}
	}
	// plugin 段本身仍存在（sharedToken），只有出现了已迁移的键才视为旧段。
	delete(legacy.Plugin, "sharedToken")
	if len(legacy.Plugin) > 0 {
		b, err := yaml.Marshal(legacy.Plugin)
		if err != nil {
			return nil, err
		}
		var p PluginSettings
		if err := yaml.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		cfg.Legacy.Plugin = &p
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Server.FrontendBaseURL == "" {
		c.Server.FrontendBaseURL = c.Server.PublicBaseURL
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Dir == "" {
		c.Log.Dir = "data/logs"
	}
	if c.Auth.Microsoft.RedirectPath == "" {
		c.Auth.Microsoft.RedirectPath = "/api/v1/auth/callback"
	}
	c.DB.Driver = strings.ToLower(strings.TrimSpace(c.DB.Driver))
	if c.DB.Driver == "" {
		c.DB.Driver = "mysql"
	}
	if c.DB.Driver == "mysql" && c.DB.DSN == "" {
		c.DB.DSN = "bcts:change-me@tcp(127.0.0.1:3306)/bcts_web?charset=utf8mb4&parseTime=true&loc=Local"
	}
	if c.DB.Driver == "sqlite" && c.DB.Path == "" {
		c.DB.Path = "data/bcts-web.db"
	}
}

// ApplyFrontendDefaults 为前端运行时配置中未配置 / 非法的字段填充默认值。
func ApplyFrontendDefaults(f *FrontendConfig) {
	if f.RealtimeWSPath == "" {
		f.RealtimeWSPath = "/api/v1/realtime"
	}
	if f.RouteSearchTimeoutMs <= 0 {
		f.RouteSearchTimeoutMs = 10000
	}
	if f.DefaultWorld == "" {
		f.DefaultWorld = "world1"
	}
	if f.MapStyle.LineWidth <= 0 {
		f.MapStyle.LineWidth = 3
	}
	if f.MapStyle.HighlightWidth <= 0 {
		f.MapStyle.HighlightWidth = 7
	}
	if f.MapStyle.DimOpacity <= 0 {
		f.MapStyle.DimOpacity = 0.2
	}
	if f.MapStyle.LineOpacity <= 0 {
		f.MapStyle.LineOpacity = 0.9
	}
	if f.MapStyle.StationRadius <= 0 {
		f.MapStyle.StationRadius = 6
	}
	if f.MapStyle.StationStrokeWidth <= 0 {
		f.MapStyle.StationStrokeWidth = 2
	}
	if f.MapStyle.StationTextSize <= 0 {
		f.MapStyle.StationTextSize = 12
	}
	if f.MapStyle.StationEnTextSize <= 0 {
		f.MapStyle.StationEnTextSize = 10
	}
	if f.MapStyle.StationMergePixelDistance <= 0 {
		f.MapStyle.StationMergePixelDistance = 28
	}
	if f.MapStyle.TrainIconSize <= 0 {
		f.MapStyle.TrainIconSize = 0.6
	}
	if f.MapStyle.TrainFocusZoom <= 0 {
		f.MapStyle.TrainFocusZoom = 16
	}
	if f.RouteDiagram.ProjectionThresholdBlocks <= 0 {
		f.RouteDiagram.ProjectionThresholdBlocks = 40
	}
	if f.RouteDiagram.MinStationGapPx <= 0 {
		f.RouteDiagram.MinStationGapPx = 32
	}
	if f.RouteDiagram.MaxStationGapPx < f.RouteDiagram.MinStationGapPx {
		f.RouteDiagram.MaxStationGapPx = max(60, f.RouteDiagram.MinStationGapPx)
	}
	if f.RouteDiagram.TrainClusterProgress <= 0 {
		f.RouteDiagram.TrainClusterProgress = 0.035
	}
	if f.RouteDiagram.TrainIconScale <= 0 {
		f.RouteDiagram.TrainIconScale = 1
	}
	if f.RouteDiagram.TrackWidthPx <= 0 {
		f.RouteDiagram.TrackWidthPx = 7
	}
	if f.RouteDiagram.ExpressMinHeightPx <= 0 {
		f.RouteDiagram.ExpressMinHeightPx = 240
	}
	if f.RouteDiagram.ExpressGradientBlendRatio <= 0 {
		f.RouteDiagram.ExpressGradientBlendRatio = 0.06
	}
	if f.TrainIcons.Express == "" {
		f.TrainIcons.Express = "data:image/svg+xml;utf8,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='32'%20height='32'%20viewBox='0%200%2032%2032'%3E%3Ccircle%20cx='16'%20cy='16'%20r='13'%20fill='%23ff5252'%20stroke='%23fff'%20stroke-width='3'/%3E%3C/svg%3E"
	}
	if f.TrainIcons.Normal == "" {
		f.TrainIcons.Normal = "data:image/svg+xml;utf8,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='32'%20height='32'%20viewBox='0%200%2032%2032'%3E%3Ccircle%20cx='16'%20cy='16'%20r='13'%20fill='%2342a5f5'%20stroke='%23fff'%20stroke-width='3'/%3E%3C/svg%3E"
	}
	if f.DefaultSystemLogo == "" {
		f.DefaultSystemLogo = "data:image/svg+xml;utf8,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='20'%20height='20'%20viewBox='0%200%2020%2020'%3E%3Crect%20width='20'%20height='20'%20rx='4'%20fill='%233a3f4b'/%3E%3Cpath%20d='M6%205h8v7a3%203%200%200%201-3%203H9a3%203%200%200%201-3-3z'%20fill='%238a90a0'/%3E%3Ccircle%20cx='8'%20cy='16'%20r='1'%20fill='%238a90a0'/%3E%3Ccircle%20cx='12'%20cy='16'%20r='1'%20fill='%238a90a0'/%3E%3C/svg%3E"
	}
	if f.AvatarURLTemplate == "" {
		f.AvatarURLTemplate = "https://mineskin.eu/helm/{player}"
	}
	if f.DefaultPricePerKm <= 0 {
		f.DefaultPricePerKm = 0.2
	}
	if f.ThemeColor == "" {
		f.ThemeColor = "#ffd400"
	}
	if f.CurrencyName == "" {
		f.CurrencyName = "帕元"
	}
}

// ApplyRouteDefaults 为路线查询参数中未配置的字段填充默认值。
func ApplyRouteDefaults(r *RouteConfig) {
	if r.QueryRateLimitPerSecond <= 0 {
		r.QueryRateLimitPerSecond = 3
	}
	// 搜索排序默认值（与插件 config.yml search.* 对齐）。
	// max-*-results 允许配 <=0 表示不限制，故用「负数才纠正」而非 <=0；未配置时 yaml 零值 0 恰是「不限制」，
	// 但插件默认给 5，这里对「未出现即 0」统一给 5：区分不了「显式 0」与「缺省」，与插件一致按缺省处理。
	if r.MaxDistanceResults == 0 {
		r.MaxDistanceResults = 5
	}
	if r.MaxPriceResults == 0 {
		r.MaxPriceResults = 5
	}
	// 权重：两者都为 0（未配置）时给 0.5/0.5；显式配置其一即保留。
	if r.SearchWeightDistance == 0 && r.SearchWeightPrice == 0 {
		r.SearchWeightDistance = 0.5
		r.SearchWeightPrice = 0.5
	}
	if r.MinDirectResults == 0 {
		r.MinDirectResults = 1
	}
	if r.MaxTransferResults == 0 {
		r.MaxTransferResults = 3
	}
	if r.TransferMinImprovement == 0 {
		r.TransferMinImprovement = 0.2
	}
	if r.ComputeTimeoutMs <= 0 {
		r.ComputeTimeoutMs = 15000
	}
}

// ApplyPluginDefaults 为插件链路参数中未配置 / 非法的字段填充默认值。
func ApplyPluginDefaults(p *PluginSettings) {
	if p.HeartbeatSeconds <= 0 {
		p.HeartbeatSeconds = 15
	}
	if p.PurchaseTimeoutSeconds <= 0 {
		p.PurchaseTimeoutSeconds = 10
	}
	if p.PurchaseMinIntervalSeconds <= 0 {
		p.PurchaseMinIntervalSeconds = 3
	}
	if p.ConfigTimeoutSeconds <= 0 {
		p.ConfigTimeoutSeconds = 10
	}
}

// ApplyRealtimeDefaults 为实时列车参数中未配置 / 非法的字段填充默认值。
func ApplyRealtimeDefaults(r *RealtimeConfig) {
	if r.TrainTimeoutSeconds <= 0 {
		r.TrainTimeoutSeconds = 30
	}
	if r.ClientSendBuffer <= 0 {
		r.ClientSendBuffer = 64
	}
}

// ApplyTilesDefaults 为世界底图配置中未配置 / 非法的字段填充默认值。
func ApplyTilesDefaults(t *TilesConfig) {
	if t.Sources == nil {
		t.Sources = []TileSourceConfig{}
	}
	migrateLegacyWorldTiles(t)
	if t.Worlds == nil {
		t.Worlds = map[string][]WorldLayerConfig{"world1": {{Static: &WorldTileConfig{Zoom: 14}}}}
	}
	for i := range t.Sources {
		if t.Sources[i].Type == "" {
			t.Sources[i].Type = "dynmap"
		}
	}
	if t.CacheMaxMB <= 0 {
		t.CacheMaxMB = 64
	}
	if t.CacheTTLSeconds <= 0 {
		t.CacheTTLSeconds = 300
	}
	if t.SynthCacheTTLSeconds <= 0 {
		t.SynthCacheTTLSeconds = 10800
	}
	if t.BlankCacheTTLSeconds <= 0 {
		t.BlankCacheTTLSeconds = 10800
	}
	if t.BrowserCacheSeconds <= 0 {
		t.BrowserCacheSeconds = 300
	}
	if t.DiscoveryRefreshSeconds <= 0 {
		t.DiscoveryRefreshSeconds = 600
	}
	if t.UpstreamTimeoutSeconds <= 0 {
		t.UpstreamTimeoutSeconds = 10
	}
	if t.UpstreamConcurrency <= 0 {
		t.UpstreamConcurrency = 8
	}
	if t.RenderJPEGQuality <= 0 {
		t.RenderJPEGQuality = 85
	}
}
