// Package config 加载后端配置（yaml + 环境变量覆盖，见 docs/BACKEND_PROMPT.md §10）。
package config

import (
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是后端总配置。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Log      LogConfig      `yaml:"log"`
	Plugin   PluginConfig   `yaml:"plugin"`
	Realtime RealtimeConfig `yaml:"realtime"`
	Auth     AuthConfig     `yaml:"auth"`
	DB       DBConfig       `yaml:"db"`
	Frontend FrontendConfig `yaml:"frontend"`
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

type PluginConfig struct {
	SharedToken            string `yaml:"sharedToken"`
	HeartbeatSeconds       int    `yaml:"heartbeatSeconds"`
	PurchaseTimeoutSeconds int    `yaml:"purchaseTimeoutSeconds"`
	// PurchaseMinIntervalSeconds 是同一玩家两次购票的最小间隔（秒），防止频繁购票压垮服务器。
	// <=0 时用默认 3 秒。
	PurchaseMinIntervalSeconds int `yaml:"purchaseMinIntervalSeconds"`
}

type RealtimeConfig struct {
	TrainTimeoutSeconds int `yaml:"trainTimeoutSeconds"`
	ClientSendBuffer    int `yaml:"clientSendBuffer"`
}

type AuthConfig struct {
	Microsoft       MicrosoftConfig `yaml:"microsoft"`
	JWTSecret       string          `yaml:"jwtSecret"`
	TestAuthEnabled bool            `yaml:"testAuthEnabled"`
	TestAuthUUIDs   []string        `yaml:"testAuthUUIDs"`
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
	ThemeColor        string                     `yaml:"themeColor" json:"themeColor"`
	WorldTiles        map[string]WorldTileConfig `yaml:"worldTiles" json:"worldTiles"`
	MapStyle          MapStyleConfig             `yaml:"mapStyle" json:"mapStyle"`
	TrainIcons        TrainIconsConfig           `yaml:"trainIcons" json:"trainIcons"`
	DefaultSystemLogo string                     `yaml:"defaultSystemLogo" json:"defaultSystemLogo"`
	AvatarURLTemplate string                     `yaml:"avatarUrlTemplate" json:"avatarUrlTemplate"`
	DefaultPricePerKm float64                    `yaml:"defaultPricePerKm" json:"defaultPricePerKm"`

	// 搜索结果排序（复刻插件 search.*，与菜单购票一致）
	MaxDistanceResults   int     `yaml:"maxDistanceResults" json:"maxDistanceResults"`
	MaxPriceResults      int     `yaml:"maxPriceResults" json:"maxPriceResults"`
	SearchWeightDistance float64 `yaml:"searchWeightDistance" json:"searchWeightDistance"`
	SearchWeightPrice    float64 `yaml:"searchWeightPrice" json:"searchWeightPrice"`
	MinDirectResults     int     `yaml:"minDirectResults" json:"minDirectResults"`

	// 联程票（一次换乘 / 两段直达）寻路参数（复刻插件 search.max-transfer-* / transfer-min-improvement）
	MaxTransferResults     int     `yaml:"maxTransferResults" json:"maxTransferResults"`
	MaxTransferCandidates  int     `yaml:"maxTransferCandidates" json:"maxTransferCandidates"`
	TransferMinImprovement float64 `yaml:"transferMinImprovement" json:"transferMinImprovement"`
	// 路线查询（前端 Web Worker 寻路）超时毫秒数，超时则终止计算并提示失败。<=0 用默认 10000。
	RouteSearchTimeoutMs int `yaml:"routeSearchTimeoutMs" json:"routeSearchTimeoutMs"`
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
	StationMergePixelDistance float64 `yaml:"stationMergePixelDistance" json:"stationMergePixelDistance"`
	TrainIconSize             float64 `yaml:"trainIconSize" json:"trainIconSize"`
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
	if c.Plugin.HeartbeatSeconds <= 0 {
		c.Plugin.HeartbeatSeconds = 15
	}
	if c.Plugin.PurchaseTimeoutSeconds <= 0 {
		c.Plugin.PurchaseTimeoutSeconds = 10
	}
	if c.Plugin.PurchaseMinIntervalSeconds <= 0 {
		c.Plugin.PurchaseMinIntervalSeconds = 3
	}
	if c.Realtime.TrainTimeoutSeconds <= 0 {
		c.Realtime.TrainTimeoutSeconds = 30
	}
	if c.Realtime.ClientSendBuffer <= 0 {
		c.Realtime.ClientSendBuffer = 64
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
	if c.Frontend.RealtimeWSPath == "" {
		c.Frontend.RealtimeWSPath = "/api/v1/realtime"
	}
	if c.Frontend.RouteSearchTimeoutMs <= 0 {
		c.Frontend.RouteSearchTimeoutMs = 10000
	}
	if c.Frontend.DefaultWorld == "" {
		c.Frontend.DefaultWorld = "world1"
	}
	if c.Frontend.WorldTiles == nil {
		c.Frontend.WorldTiles = map[string]WorldTileConfig{"world1": {Zoom: 14}}
	}
	if c.Frontend.MapStyle.LineWidth <= 0 {
		c.Frontend.MapStyle.LineWidth = 3
	}
	if c.Frontend.MapStyle.HighlightWidth <= 0 {
		c.Frontend.MapStyle.HighlightWidth = 7
	}
	if c.Frontend.MapStyle.DimOpacity <= 0 {
		c.Frontend.MapStyle.DimOpacity = 0.2
	}
	if c.Frontend.MapStyle.LineOpacity <= 0 {
		c.Frontend.MapStyle.LineOpacity = 0.9
	}
	if c.Frontend.MapStyle.StationRadius <= 0 {
		c.Frontend.MapStyle.StationRadius = 6
	}
	if c.Frontend.MapStyle.StationStrokeWidth <= 0 {
		c.Frontend.MapStyle.StationStrokeWidth = 2
	}
	if c.Frontend.MapStyle.StationTextSize <= 0 {
		c.Frontend.MapStyle.StationTextSize = 12
	}
	if c.Frontend.MapStyle.StationMergePixelDistance <= 0 {
		c.Frontend.MapStyle.StationMergePixelDistance = 28
	}
	if c.Frontend.MapStyle.TrainIconSize <= 0 {
		c.Frontend.MapStyle.TrainIconSize = 0.6
	}
	if c.Frontend.TrainIcons.Express == "" {
		c.Frontend.TrainIcons.Express = "data:image/svg+xml;utf8,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='32'%20height='32'%20viewBox='0%200%2032%2032'%3E%3Ccircle%20cx='16'%20cy='16'%20r='13'%20fill='%23ff5252'%20stroke='%23fff'%20stroke-width='3'/%3E%3C/svg%3E"
	}
	if c.Frontend.TrainIcons.Normal == "" {
		c.Frontend.TrainIcons.Normal = "data:image/svg+xml;utf8,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='32'%20height='32'%20viewBox='0%200%2032%2032'%3E%3Ccircle%20cx='16'%20cy='16'%20r='13'%20fill='%2342a5f5'%20stroke='%23fff'%20stroke-width='3'/%3E%3C/svg%3E"
	}
	if c.Frontend.DefaultSystemLogo == "" {
		c.Frontend.DefaultSystemLogo = "data:image/svg+xml;utf8,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='20'%20height='20'%20viewBox='0%200%2020%2020'%3E%3Crect%20width='20'%20height='20'%20rx='4'%20fill='%233a3f4b'/%3E%3Cpath%20d='M6%205h8v7a3%203%200%200%201-3%203H9a3%203%200%200%201-3-3z'%20fill='%238a90a0'/%3E%3Ccircle%20cx='8'%20cy='16'%20r='1'%20fill='%238a90a0'/%3E%3Ccircle%20cx='12'%20cy='16'%20r='1'%20fill='%238a90a0'/%3E%3C/svg%3E"
	}
	if c.Frontend.AvatarURLTemplate == "" {
		c.Frontend.AvatarURLTemplate = "https://mineskin.eu/helm/{player}"
	}
	if c.Frontend.DefaultPricePerKm <= 0 {
		c.Frontend.DefaultPricePerKm = 0.2
	}
	if c.Frontend.ThemeColor == "" {
		c.Frontend.ThemeColor = "#ffd400"
	}
	if c.Frontend.CurrencyName == "" {
		c.Frontend.CurrencyName = "帕元"
	}
	// 搜索排序默认值（与插件 config.yml search.* 对齐）。
	// max-*-results 允许配 <=0 表示不限制，故用「负数才纠正」而非 <=0；未配置时 yaml 零值 0 恰是「不限制」，
	// 但插件默认给 5，这里对「未出现即 0」统一给 5：区分不了「显式 0」与「缺省」，与插件一致按缺省处理。
	if c.Frontend.MaxDistanceResults == 0 {
		c.Frontend.MaxDistanceResults = 5
	}
	if c.Frontend.MaxPriceResults == 0 {
		c.Frontend.MaxPriceResults = 5
	}
	// 权重：两者都为 0（未配置）时给 0.5/0.5；显式配置其一即保留。
	if c.Frontend.SearchWeightDistance == 0 && c.Frontend.SearchWeightPrice == 0 {
		c.Frontend.SearchWeightDistance = 0.5
		c.Frontend.SearchWeightPrice = 0.5
	}
	if c.Frontend.MinDirectResults == 0 {
		c.Frontend.MinDirectResults = 1
	}
	if c.Frontend.MaxTransferResults == 0 {
		c.Frontend.MaxTransferResults = 3
	}
	if c.Frontend.MaxTransferCandidates == 0 {
		c.Frontend.MaxTransferCandidates = 30
	}
	if c.Frontend.TransferMinImprovement == 0 {
		c.Frontend.TransferMinImprovement = 0.2
	}
}
