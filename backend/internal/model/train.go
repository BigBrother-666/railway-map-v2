package model

import "encoding/json"

// Train 是单列车实时遥测（见 docs/FRONTEND_PROMPT.md §4.8）。
// 字段尽量可扩展：插件可新增字段，后端透传、前端按存在性渲染。
type Train struct {
	TrainID      string   `json:"trainId"`
	World        string   `json:"world"`
	Head         Head     `json:"head"`
	SpeedKph     float64  `json:"speedKph"`
	CartCount    int      `json:"cartCount"`
	Passengers   []string `json:"passengers"`
	Express      bool     `json:"express"`
	LineID       string   `json:"lineId,omitempty"`
	Destination  string   `json:"destination,omitempty"`
	RouteNodeIDs []string `json:"routeNodeIds,omitempty"`
}

// Head 是车头位置与朝向。
type Head struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Z   float64 `json:"z"`
	Yaw float64 `json:"yaw"`
}

// RealtimeTrainsData 是插件推送的列车遥测批量负载。
// 后端透传时不强类型化 Train（保留扩展字段），但内部聚合用 Train 解析关键字段。
type RealtimeTrainsData struct {
	Trains []json.RawMessage `json:"trains"`
}

// 前端实时 WS 的消息类型（见 docs/BACKEND_PROMPT.md §7.2）。
const (
	WSSnapshot = "snapshot" // 全量列车
	WSUpdate   = "update"   // 变化的列车
	WSRemove   = "remove"   // 消失的 trainId
	WSPing     = "ping"
	WSPong     = "pong"
)
