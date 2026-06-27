// Package model 定义后端与插件、前端之间共享的 DTO。
// 字段 JSON tag 用 camelCase，与前端 TS 类型一一对应（见 docs/FRONTEND_PROMPT.md §四）。
package model

import "encoding/json"

// Envelope 是插件 ↔ 后端 WebSocket 的统一消息信封（见 docs/BACKEND_PROMPT.md §4.2）。
type Envelope struct {
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	TS   int64           `json:"ts"`
	Data json.RawMessage `json:"data,omitempty"`
}

// 插件 ↔ 后端消息类型常量（点分命名空间）。
const (
	// 插件 → 后端
	TypeHello           = "hello"
	TypeSnapshotGeo     = "snapshot.geo"
	TypeSnapshotSystems = "snapshot.systems"
	TypeSnapshotLines   = "snapshot.lines"
	TypeRealtimeTrains  = "realtime.trains"
	TypeRealtimeRemoved = "realtime.removed"
	TypePurchaseResult  = "purchase.result"
	TypeRideEvent       = "ride.event"
	TypeRidePayment     = "ride.payment"
	TypePong            = "pong"
	TypeAuthBind        = "auth.bind"
	TypeAuthUnbind      = "auth.unbind"

	// 后端 → 插件
	TypeWelcome         = "welcome"
	TypePing            = "ping"
	TypePurchaseRequest = "purchase.request"
	TypeSyncRequest     = "sync.request"
)

// HelloData 是插件首帧 hello 的负载。
type HelloData struct {
	ServerID      string   `json:"serverId"`
	PluginVersion string   `json:"pluginVersion"`
	Worlds        []string `json:"worlds"`
}

// WelcomeData 是后端回应 hello 的负载。
type WelcomeData struct {
	ServerTime      int64  `json:"serverTime"`
	AcceptedVersion string `json:"acceptedVersion"`
}

// SyncRequestData 是后端请求插件补推快照的负载。
type SyncRequestData struct {
	What string `json:"what"` // geo|systems|lines|all
}

// AuthBindData 是插件同步「允许登录网页」白名单的负载。
type AuthBindData struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}
