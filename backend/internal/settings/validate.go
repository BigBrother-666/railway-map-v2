package settings

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/tilesource"
)

// FieldError 是单个字段的校验错误（field 为 JSON 路径，如 mapStyle.lineWidth）。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// FieldErrors 是一组字段校验错误，实现 error。
type FieldErrors []FieldError

func (e FieldErrors) Error() string {
	parts := make([]string, len(e))
	for i, fe := range e {
		parts[i] = fe.Field + ": " + fe.Message
	}
	return strings.Join(parts, "; ")
}

var (
	hexColor     = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	tileSourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
)

type validator struct{ errs FieldErrors }

func (v *validator) add(field, format string, args ...any) {
	v.errs = append(v.errs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

// nonNegative 校验数值 >= 0（0 表示使用默认值）。
func (v *validator) nonNegative(field string, x float64) {
	if x < 0 {
		v.add(field, "不能为负数")
	}
}

func (v *validator) between(field string, x, lo, hi float64) {
	if x < lo || x > hi {
		v.add(field, "取值范围 %g ~ %g", lo, hi)
	}
}

// ValidateFrontend 校验前端运行时配置；零值字段视为「使用默认值」不报错。tiles 用于校验 defaultWorld。
func ValidateFrontend(f config.FrontendConfig, tiles config.TilesConfig) FieldErrors {
	v := &validator{}
	if f.ThemeColor != "" && !hexColor.MatchString(f.ThemeColor) {
		v.add("themeColor", "必须是 #RRGGBB 格式")
	}
	if f.RealtimeWSPath != "" && !strings.HasPrefix(f.RealtimeWSPath, "/") {
		v.add("realtimeWsPath", "必须以 / 开头")
	}
	if f.AvatarURLTemplate != "" && !strings.Contains(f.AvatarURLTemplate, "{player}") {
		v.add("avatarUrlTemplate", "必须包含 {player} 占位符")
	}
	v.nonNegative("defaultPricePerKm", f.DefaultPricePerKm)
	if f.RouteSearchTimeoutMs < 0 {
		v.add("routeSearchTimeoutMs", "不能为负数")
	}
	if !defaultWorldKnown(f.DefaultWorld, tiles) {
		v.add("defaultWorld", "必须是「世界底图」中已配置的世界")
	}
	ms := f.MapStyle
	for field, x := range map[string]float64{
		"lineWidth": ms.LineWidth, "highlightWidth": ms.HighlightWidth, "stationRadius": ms.StationRadius,
		"stationStrokeWidth": ms.StationStrokeWidth, "stationTextSize": ms.StationTextSize,
		"stationEnTextSize": ms.StationEnTextSize, "stationMergePixelDistance": ms.StationMergePixelDistance,
		"trainIconSize": ms.TrainIconSize, "trainFocusZoom": ms.TrainFocusZoom,
		"stationFocusZoom": ms.StationFocusZoom,
	} {
		v.nonNegative("mapStyle."+field, x)
	}
	v.between("mapStyle.dimOpacity", ms.DimOpacity, 0, 1)
	v.between("mapStyle.lineOpacity", ms.LineOpacity, 0, 1)
	rd := f.RouteDiagram
	for field, x := range map[string]float64{
		"projectionThresholdBlocks": rd.ProjectionThresholdBlocks, "minStationGapPx": rd.MinStationGapPx,
		"maxStationGapPx": rd.MaxStationGapPx, "trainClusterProgress": rd.TrainClusterProgress,
		"trainIconScale": rd.TrainIconScale, "trackWidthPx": rd.TrackWidthPx, "expressMinHeightPx": rd.ExpressMinHeightPx,
	} {
		v.nonNegative("routeDiagram."+field, x)
	}
	v.between("routeDiagram.expressGradientBlendRatio", rd.ExpressGradientBlendRatio, 0, 0.5)
	if rd.MinStationGapPx > 0 && rd.MaxStationGapPx > 0 && rd.MaxStationGapPx < rd.MinStationGapPx {
		v.add("routeDiagram.maxStationGapPx", "不能小于 minStationGapPx")
	}
	sortErrors(v.errs)
	return v.errs
}

// ValidateRoute 校验路线查询参数。max*Results 允许 <=0（不限制），权重允许 0。
func ValidateRoute(r config.RouteConfig) FieldErrors {
	v := &validator{}
	v.nonNegative("queryRateLimitPerSecond", r.QueryRateLimitPerSecond)
	v.between("searchWeightDistance", r.SearchWeightDistance, 0, 1)
	v.between("searchWeightPrice", r.SearchWeightPrice, 0, 1)
	v.between("transferMinImprovement", r.TransferMinImprovement, 0, 1)
	if r.ComputeTimeoutMs < 0 {
		v.add("computeTimeoutMs", "不能为负数")
	}
	return v.errs
}

func validateStaticLayer(v *validator, p string, w config.WorldTileConfig) {
	validateView(v, p, w.Opacity, w.MinZoom, w.MaxZoom, w.Center)
	if w.Scheme != "" && w.Scheme != "xyz" && w.Scheme != "tms" {
		v.add(p+".scheme", "只能是 xyz 或 tms")
	}
	if w.TileSize < 0 {
		v.add(p+".tileSize", "不能为负数")
	}
	if w.MinNativeZoom != 0 && w.MaxNativeZoom != 0 && w.MinNativeZoom > w.MaxNativeZoom {
		v.add(p+".minNativeZoom", "不能大于 maxNativeZoom")
	}
	if len(w.MapOffset) != 0 && len(w.MapOffset) != 2 {
		v.add(p+".mapOffset", "必须是 [x, z] 两个数")
	}
}

// validateView 校验静态瓦片与外部地图共有的视图参数。
func validateView(v *validator, p string, opacity, minZoom, maxZoom float64, center []float64) {
	if opacity != 0 {
		v.between(p+".opacity", opacity, 0, 1)
	}
	if minZoom != 0 && maxZoom != 0 && minZoom > maxZoom {
		v.add(p+".minZoom", "不能大于 maxZoom")
	}
	if len(center) != 0 && len(center) != 2 {
		v.add(p+".center", "必须是 [x, z] 两个数")
	}
}

// sortErrors 让 map 遍历产生的错误顺序稳定。
func sortErrors(errs FieldErrors) {
	for i := 1; i < len(errs); i++ {
		for j := i; j > 0 && errs[j].Field < errs[j-1].Field; j-- {
			errs[j], errs[j-1] = errs[j-1], errs[j]
		}
	}
}

// ValidatePlugin 校验插件链路参数；0 表示使用默认值。
func ValidatePlugin(p config.PluginSettings) FieldErrors {
	v := &validator{}
	for _, f := range []struct {
		field string
		x     int
	}{
		{"heartbeatSeconds", p.HeartbeatSeconds},
		{"purchaseTimeoutSeconds", p.PurchaseTimeoutSeconds},
		{"purchaseMinIntervalSeconds", p.PurchaseMinIntervalSeconds},
		{"configTimeoutSeconds", p.ConfigTimeoutSeconds},
	} {
		v.nonNegative(f.field, float64(f.x))
	}
	return v.errs
}

// ValidateRealtime 校验实时列车参数；0 表示使用默认值。
func ValidateRealtime(r config.RealtimeConfig) FieldErrors {
	v := &validator{}
	v.nonNegative("trainTimeoutSeconds", float64(r.TrainTimeoutSeconds))
	v.nonNegative("clientSendBuffer", float64(r.ClientSendBuffer))
	if r.ClientSendBuffer > 4096 {
		v.add("clientSendBuffer", "不能超过 4096")
	}
	return v.errs
}

// defaultWorldKnown 报告 defaultWorld 是否可用：配置了外部来源时世界由来源自动发现，不强制写进 worlds。
func defaultWorldKnown(world string, t config.TilesConfig) bool {
	if world == "" || len(t.Worlds) == 0 || len(t.Sources) > 0 {
		return true
	}
	_, ok := t.Worlds[world]
	return ok
}

// ValidateTiles 校验世界底图配置；数值 0 表示使用默认值。defaultWorld 为当前前端配置的默认世界。
func ValidateTiles(t config.TilesConfig, defaultWorld string) FieldErrors {
	v := &validator{}
	sourceIDs := map[string]bool{}
	for _, src := range t.Sources {
		sourceIDs[src.ID] = true
	}
	for name, layers := range t.Worlds {
		if strings.TrimSpace(name) == "" {
			v.add("worlds", "世界名不能为空")
		}
		usedSources := map[string]bool{}
		for i, l := range layers {
			p := fmt.Sprintf("worlds.%s.%d", name, i)
			switch {
			case (l.Static == nil) == (l.External == nil):
				v.add(p, "必须是静态瓦片或外部地图之一")
			case l.Static != nil:
				validateStaticLayer(v, p+".static", *l.Static)
			default:
				e := *l.External
				p += ".external"
				switch {
				case !sourceIDs[e.Source]:
					v.add(p+".source", "外部来源 %s 不存在", e.Source)
				case usedSources[e.Source]:
					v.add(p+".source", "同一世界不能重复使用外部来源 %s", e.Source)
				}
				usedSources[e.Source] = true
				validateView(v, p, e.Opacity, e.MinZoom, e.MaxZoom, e.Center)
				if n := e.SynthZoomLevels; n != nil && (*n < 0 || *n > 6) {
					v.add(p+".synthZoomLevels", "取值范围 0 ~ 6")
				}
				v.nonNegative(p+".synthMarginBlocks", float64(e.SynthMarginBlocks))
			}
		}
	}
	if !defaultWorldKnown(defaultWorld, t) {
		v.add("worlds", "缺少默认世界 %s（可先在「前端显示」中修改默认世界）", defaultWorld)
	}
	seen := map[string]bool{}
	for i, src := range t.Sources {
		p := fmt.Sprintf("sources.%d", i)
		switch {
		case !tileSourceID.MatchString(src.ID):
			v.add(p+".id", "只能包含字母、数字、- 和 _（1~32 个字符）")
		case seen[src.ID]:
			v.add(p+".id", "与其他来源重复")
		}
		seen[src.ID] = true
		if src.Type != "" && !tilesource.Supported(src.Type) {
			v.add(p+".type", "不支持的地图类型")
		}
		if u, err := url.Parse(src.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			v.add(p+".url", "必须是 http(s) 地址")
		}
	}
	for _, f := range []struct {
		field string
		x     int
	}{
		{"cacheMaxMb", t.CacheMaxMB},
		{"cacheTtlSeconds", t.CacheTTLSeconds},
		{"synthCacheTtlSeconds", t.SynthCacheTTLSeconds},
		{"blankCacheTtlSeconds", t.BlankCacheTTLSeconds},
		{"browserCacheSeconds", t.BrowserCacheSeconds},
		{"discoveryRefreshSeconds", t.DiscoveryRefreshSeconds},
		{"upstreamTimeoutSeconds", t.UpstreamTimeoutSeconds},
		{"upstreamConcurrency", t.UpstreamConcurrency},
	} {
		v.nonNegative(f.field, float64(f.x))
	}
	if t.CacheMaxMB > 4096 {
		v.add("cacheMaxMb", "不能超过 4096")
	}
	if t.DiscoveryRefreshSeconds > 0 && t.DiscoveryRefreshSeconds < 10 {
		v.add("discoveryRefreshSeconds", "不能小于 10")
	}
	if t.UpstreamConcurrency > 64 {
		v.add("upstreamConcurrency", "不能超过 64")
	}
	if t.RenderJPEGQuality < 0 || t.RenderJPEGQuality > 100 {
		v.add("renderJpegQuality", "取值范围 1 ~ 100")
	}
	sortErrors(v.errs)
	return v.errs
}
