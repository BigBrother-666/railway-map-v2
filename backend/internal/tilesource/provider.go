// Package tilesource 接入外部网页地图（dynmap 等）作为世界底图：读取上游元数据发现各世界可用的底图，
// 换算成前端 worldTiles 的对齐参数，并代理 + 缓存瓦片（上游通常不带 CORS 头，浏览器无法直接用作 WebGL 纹理）。
//
// 新增一种地图类型：实现 Provider / Layer，并在 providers 中注册类型名。
package tilesource

import (
	"context"
	"fmt"

	"railway-map-backend/internal/config"
)

// Provider 是一种外部网页地图的接入实现（一个已配置的来源一个实例）。
type Provider interface {
	// Discover 读取上游元数据，返回该来源中的全部世界（不可用的世界 Layer 为 nil，Skip 说明原因）。
	Discover(ctx context.Context, fetch Fetcher) ([]World, error)
}

// Layer 是某个世界的一张底图。
type Layer interface {
	// Tile 返回下发前端的对齐参数（tileSize / mapScale / mapOffset / 原生缩放 / 缩放范围），
	// 不含 tileUrl（由 Service 填为代理地址）。
	Tile() config.WorldTileConfig
	// UpstreamURL 把前端请求的 XYZ 瓦片 (z, x, y) 翻译成上游瓦片地址；超出上游范围返回 false。
	UpstreamURL(z, x, y int) (string, bool)
}

// World 是来源中的一个世界。
type World struct {
	// Name 是游戏内世界名，与 geojson 的 world 字段精确匹配。
	Name string
	// Map 是选用的上游地图名（如 dynmap 的 flat）。
	Map string
	// Layer 为 nil 表示该世界没有可用底图，原因见 Skip。
	Layer Layer
	Skip  string
}

// Fetcher 以 GET 读取上游地址的内容（由 Service 提供，带超时与大小限制）。
type Fetcher func(ctx context.Context, url string) ([]byte, error)

var providers = map[string]func(src config.TileSourceConfig) (Provider, error){
	"dynmap": newDynmap,
}

// Supported 报告是否支持该来源类型。
func Supported(typ string) bool {
	_, ok := providers[typ]
	return ok
}

func newProvider(src config.TileSourceConfig) (Provider, error) {
	f, ok := providers[src.Type]
	if !ok {
		return nil, fmt.Errorf("不支持的地图类型 %q", src.Type)
	}
	return f(src)
}
