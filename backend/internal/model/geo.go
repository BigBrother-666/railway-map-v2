package model

import "encoding/json"

// FeatureCollection 是 geojson 顶层结构。后端对 geojson 不做强类型解析，
// 整体透传给前端（合并已在插件侧完成），仅在需要时按 Feature 检视属性。
type FeatureCollection struct {
	Type     string    `json:"type"`
	Features []Feature `json:"features"`
}

// Feature 是 geojson 的一个要素（Point 或 LineString）。
type Feature struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
	Geometry   json.RawMessage `json:"geometry"`
}

// SnapshotGeoData 是插件推送的 geojson 快照负载（合并后的单个 FeatureCollection）。
type SnapshotGeoData struct {
	FeatureCollection json.RawMessage `json:"featureCollection"`
}

// Line 是向前端暴露的线路公开信息（见 docs/FRONTEND_PROMPT.md §4.4）。
type Line struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Color           string   `json:"color"`
	SystemID        string   `json:"systemId"`
	Stations        []string `json:"stations"`
	Ring            bool     `json:"ring"`
	ReverseStations []string `json:"reverseStations,omitempty"`
}

// System 是向前端暴露的铁路系统公开信息（不含 creator/income/withdrawn/members）。
type System struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	PricePerKm *float64 `json:"pricePerKm"`
	LogoURL    *string  `json:"logoUrl"`
}

// SnapshotSystemsData / SnapshotLinesData 是插件推送的系统 / 线路快照负载。
type SnapshotSystemsData struct {
	Systems []System `json:"systems"`
}

type SnapshotLinesData struct {
	Lines []Line `json:"lines"`
}
