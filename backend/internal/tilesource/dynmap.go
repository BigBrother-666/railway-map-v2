package tilesource

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"railway-map-backend/internal/config"
)

// dynmapNativeZoom 是 dynmap 最精细一级（zoomoutlevel 0）对应的前端 XYZ 缩放级。
// 取 14 时墨卡托世界宽 128·2^14 px，scale=4 的地图可容纳 ±262144 方块，覆盖常见世界范围；
// 只影响前端缩放级的数值，不影响清晰度（前端 minZoom / maxZoom 按此换算）。
const dynmapNativeZoom = 14

// dynmap 前端 standalone/config.js 缺失时的默认地址（与 dynmap 默认 standalone 部署一致）。
const (
	dynmapDefaultConfigPath = "standalone/dynmap_config.json?_={timestamp}"
	dynmapDefaultTilesPath  = "tiles/"
)

var (
	dynmapConfigURLPattern = regexp.MustCompile(`configuration\s*:\s*['"]([^'"]+)['"]`)
	dynmapTilesURLPattern  = regexp.MustCompile(`tiles\s*:\s*['"]([^'"]+)['"]`)
)

type dynmapProvider struct {
	base    *url.URL
	mapName string
}

func newDynmap(src config.TileSourceConfig) (Provider, error) {
	base, err := url.Parse(src.URL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("地址无效: %q", src.URL)
	}
	// 前端地址视为目录：config.js / tiles/ 等相对地址都相对它解析。
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	base.RawQuery, base.Fragment = "", ""
	return &dynmapProvider{base: base, mapName: src.Map}, nil
}

// dynmapConfiguration 是 dynmap_config.json 中用到的字段。
type dynmapConfiguration struct {
	Worlds []struct {
		Name string      `json:"name"`
		Maps []dynmapMap `json:"maps"`
	} `json:"worlds"`
}

type dynmapMap struct {
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Prefix      string    `json:"prefix"`
	TileScale   int       `json:"tilescale"`
	MapZoomOut  int       `json:"mapzoomout"`
	MapZoomIn   int       `json:"mapzoomin"`
	WorldToMap  []float64 `json:"worldtomap"`
	ImageFormat string    `json:"image-format"`
	NightAndDay bool      `json:"nightandday"`
	Protected   bool      `json:"protected"`
}

func (p *dynmapProvider) Discover(ctx context.Context, fetch Fetcher) ([]World, error) {
	configPath, tilesPath := dynmapDefaultConfigPath, dynmapDefaultTilesPath
	// standalone/config.js 是一段 JS（var config = { url: {...} }），只抽取两个地址；读不到则用默认值。
	if js, err := fetch(ctx, p.resolve("standalone/config.js")); err == nil {
		if m := dynmapConfigURLPattern.FindSubmatch(js); m != nil {
			configPath = string(m[1])
		}
		if m := dynmapTilesURLPattern.FindSubmatch(js); m != nil {
			tilesPath = string(m[1])
		}
	}
	configPath = strings.ReplaceAll(configPath, "{timestamp}", strconv.FormatInt(time.Now().UnixMilli(), 10))
	raw, err := fetch(ctx, p.resolve(configPath))
	if err != nil {
		return nil, fmt.Errorf("读取 dynmap 配置失败: %w", err)
	}
	var cfg dynmapConfiguration
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 dynmap 配置失败: %w", err)
	}
	tilesBase := p.resolve(tilesPath)

	worlds := make([]World, 0, len(cfg.Worlds))
	for _, w := range cfg.Worlds {
		world := World{Name: w.Name}
		var reasons []string
		for _, m := range w.Maps {
			if p.mapName != "" && m.Name != p.mapName {
				continue
			}
			layer, why := newDynmapLayer(tilesBase, w.Name, m)
			if layer != nil {
				world.Map, world.Layer = m.Name, layer
				break
			}
			reasons = append(reasons, m.Name+"："+why)
		}
		if world.Layer == nil {
			switch {
			case len(reasons) > 0:
				world.Skip = strings.Join(reasons, "；")
			case p.mapName != "":
				world.Skip = "没有名为 " + p.mapName + " 的地图"
			default:
				world.Skip = "没有地图"
			}
		}
		worlds = append(worlds, world)
	}
	return worlds, nil
}

// resolve 把 dynmap 前端中的相对地址解析为绝对地址。
func (p *dynmapProvider) resolve(ref string) string {
	u, err := p.base.Parse(ref)
	if err != nil {
		return p.base.String() + ref
	}
	return u.String()
}

// dynmapLayer 是一张 dynmap 俯视 HD 地图。
//
// dynmap（hdmap.js / dynmaputils.js）的映射：最精细一级像素 px = scale·x，py = T + scale·z（T = 128<<tilescale
// 为瓦片边长）；第 zl 级缩小图的第 (col, row) 块瓦片文件名为
// {prefix}/{x>>5}_{y>>5}/{"z"×zl + "_"}{x}_{y}.{fmt}，其中 x = col·2^zl，y = -row·2^zl。
//
// 前端 coords.ts 用「墨卡托像素 = 中心 + (游戏坐标 + offset)·mapScale」线性映射。取 tileSize = T、mapScale = scale、
// mapOffset = [0, T/scale]、原生缩放 N 后，前端缩放级 k 的瓦片 (X, Y) 恰好对应 dynmap 的 zl = N-k、
// col = X - 2^(k-1)、row = Y - 2^(k-1)，两边瓦片网格逐级整数对齐。
type dynmapLayer struct {
	tilesBase string // 上游瓦片根地址（如 https://host/dynmap/tiles/ 或 .../MySQL_tiles.php?tile=）
	world     string
	m         dynmapMap
	tileSize  int
	scale     float64
}

// newDynmapLayer 校验地图是否能与平面线路对齐；不能时返回原因。
func newDynmapLayer(tilesBase, world string, m dynmapMap) (*dynmapLayer, string) {
	switch {
	case m.Type != "HDMapType":
		return nil, "不支持的地图类型 " + m.Type
	case m.Protected:
		return nil, "地图需要登录才能查看"
	case m.NightAndDay:
		return nil, "昼夜地图暂不支持"
	case len(m.WorldToMap) != 9:
		return nil, "缺少 worldtomap 投影矩阵"
	case m.TileScale < 0 || m.TileScale > 4 || m.MapZoomOut < 0 || m.MapZoomOut >= dynmapNativeZoom || m.MapZoomIn < 0:
		return nil, "缩放参数异常"
	}
	// 俯视且未旋转：lng = scale·x，lat = -scale·z，与高度 y 无关（倾斜视角的像素位置依赖 y，无法与平面线路对齐）。
	w := m.WorldToMap
	scale := w[0]
	const eps = 1e-6
	if scale <= 0 || math.Abs(w[1]) > eps || math.Abs(w[2]) > eps || math.Abs(w[3]) > eps ||
		math.Abs(w[4]) > eps || math.Abs(w[5]+scale) > eps {
		return nil, "不是正南朝上的俯视地图"
	}
	return &dynmapLayer{tilesBase: tilesBase, world: world, m: m, tileSize: 128 << m.TileScale, scale: scale}, ""
}

func (l *dynmapLayer) Tile() config.WorldTileConfig {
	minNative := float64(dynmapNativeZoom - l.m.MapZoomOut)
	// minNativeZoom / maxNativeZoom 是瓦片 z；minZoom / maxZoom 是 MapLibre 地图缩放级，
	// 二者相差 log2(512 / tileSize)（MapLibre 以 512px 为一级瓦片），tileSize = 128<<tilescale 时为 2 - tilescale。
	tileZoomOffset := float64(2 - l.m.TileScale)
	return config.WorldTileConfig{
		TileSize:      l.tileSize,
		MapScale:      l.scale,
		MapOffset:     []float64{0, float64(l.tileSize) / l.scale},
		MinNativeZoom: minNative,
		MaxNativeZoom: dynmapNativeZoom,
		MinZoom:       minNative - tileZoomOffset,
		MaxZoom:       float64(dynmapNativeZoom+l.m.MapZoomIn) - tileZoomOffset,
	}
}

func (l *dynmapLayer) UpstreamURL(z, x, y int) (string, bool) {
	zl := dynmapNativeZoom - z
	if zl < 0 || zl > l.m.MapZoomOut {
		return "", false
	}
	half := 1 << (z - 1)
	col, row := x-half, y-half
	tx, ty := col<<zl, -(row << zl)
	prefix := ""
	if zl > 0 {
		prefix = strings.Repeat("z", zl) + "_"
	}
	format := l.m.ImageFormat
	if format == "" {
		format = "png"
	}
	// Go 的 >> 对负数是算术右移（向下取整），与 JS 一致。
	name := fmt.Sprintf("%s/%d_%d/%s%d_%d.%s", l.m.Prefix, tx>>5, ty>>5, prefix, tx, ty, format)
	return l.tilesBase + jsEscape(l.world+"/"+name), true
}

// jsEscape 复刻 dynmap 拼瓦片地址时用的 JS escape()：保留 A-Za-z0-9@*_+-./，其余按 UTF-8 字节百分号编码
// （JS 对非 ASCII 输出 %uXXXX，静态文件服务器无法识别，这里改用标准 UTF-8 编码）。
func jsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || strings.IndexByte("@*_+-./", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
