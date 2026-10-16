package settings

import (
	"fmt"
	"regexp"
	"strings"

	"railway-map-backend/internal/config"
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

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

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

// ValidateFrontend 校验前端运行时配置；零值字段视为「使用默认值」不报错。
func ValidateFrontend(f config.FrontendConfig) FieldErrors {
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
	if f.DefaultWorld != "" && len(f.WorldTiles) > 0 {
		if _, ok := f.WorldTiles[f.DefaultWorld]; !ok {
			v.add("defaultWorld", "必须是已配置的世界之一")
		}
	}
	for name, t := range f.WorldTiles {
		p := "worldTiles." + name
		if strings.TrimSpace(name) == "" {
			v.add("worldTiles", "世界名不能为空")
		}
		if t.Opacity != 0 {
			v.between(p+".opacity", t.Opacity, 0, 1)
		}
		if t.Scheme != "" && t.Scheme != "xyz" && t.Scheme != "tms" {
			v.add(p+".scheme", "只能是 xyz 或 tms")
		}
		if t.TileSize < 0 {
			v.add(p+".tileSize", "不能为负数")
		}
		if t.MinZoom != 0 && t.MaxZoom != 0 && t.MinZoom > t.MaxZoom {
			v.add(p+".minZoom", "不能大于 maxZoom")
		}
		if t.MinNativeZoom != 0 && t.MaxNativeZoom != 0 && t.MinNativeZoom > t.MaxNativeZoom {
			v.add(p+".minNativeZoom", "不能大于 maxNativeZoom")
		}
		if len(t.MapOffset) != 0 && len(t.MapOffset) != 2 {
			v.add(p+".mapOffset", "必须是 [x, z] 两个数")
		}
		if len(t.Center) != 0 && len(t.Center) != 2 {
			v.add(p+".center", "必须是 [x, z] 两个数")
		}
	}
	ms := f.MapStyle
	for field, x := range map[string]float64{
		"lineWidth": ms.LineWidth, "highlightWidth": ms.HighlightWidth, "stationRadius": ms.StationRadius,
		"stationStrokeWidth": ms.StationStrokeWidth, "stationTextSize": ms.StationTextSize,
		"stationEnTextSize": ms.StationEnTextSize, "stationMergePixelDistance": ms.StationMergePixelDistance,
		"trainIconSize": ms.TrainIconSize, "trainFocusZoom": ms.TrainFocusZoom,
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

// sortErrors 让 map 遍历产生的错误顺序稳定。
func sortErrors(errs FieldErrors) {
	for i := 1; i < len(errs); i++ {
		for j := i; j > 0 && errs[j].Field < errs[j-1].Field; j-- {
			errs[j], errs[j-1] = errs[j-1], errs[j]
		}
	}
}
