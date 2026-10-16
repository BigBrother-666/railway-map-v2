package model

import "encoding/json"

// 个人中心相关的插件 ↔ 后端消息类型。
const (
	// 后端 → 插件：配置写操作（op 区分系统 / 线路 / Logo / 车站别名 / 车站组）
	TypeConfigRequest = "config.request"
	// 插件 → 后端：配置写操作回执
	TypeConfigResult = "config.result"
	// 插件 → 后端：铁路系统私有快照（含 creator / members / income / withdrawn，不对公开接口暴露）
	TypeSnapshotSystemsPrivate = "snapshot.systems.private"
	// 插件 → 后端：线路完整配置私有快照（含 bossbar / notice 字段）
	TypeSnapshotLinesPrivate = "snapshot.lines.private"
	// 插件 → 后端：营收事件（购票 / 充值，含按系统分配）
	TypeRevenueEvent = "revenue.event"
	// 后端 → 插件：确认已落库的事件（插件据此删除 outbox 中对应行）
	TypeEventAck = "event.ack"
)

// ConfigRequestWire 是 config.request 的负载。Payload 结构随 Op 而定（见 pluginconfig 包）。
type ConfigRequestWire struct {
	RequestID string          `json:"requestId"`
	Op        string          `json:"op"`
	ActorUUID string          `json:"actorUuid"`
	ActorName string          `json:"actorName"`
	Payload   json.RawMessage `json:"payload"`
}

// ConfigResult 是 config.result 的负载。
type ConfigResult struct {
	RequestID string `json:"requestId"`
	Success   bool   `json:"success"`
	// Reason 是失败原因码（如 not-found / invalid / traversal-running），Message 为可直接展示的中文说明。
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// Field 是校验失败的字段名（可选）。
	Field string `json:"field,omitempty"`
	// Warnings 是非致命提示（如车站不存在）。
	Warnings []string `json:"warnings,omitempty"`
	// Conflicts 是车站组冲突（站名 → 已所在组名）。
	Conflicts map[string]string `json:"conflicts,omitempty"`
}

// PrivateSystem 是私有快照中的铁路系统。
type PrivateSystem struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	PricePerKm *float64 `json:"pricePerKm"`
	LogoURL    *string  `json:"logoUrl,omitempty"`
	MCLogoURL  *string  `json:"mcLogoUrl,omitempty"`
	Creator    string   `json:"creator"`
	Members    []string `json:"members"`
	Income     float64  `json:"income"`
	Withdrawn  float64  `json:"withdrawn"`
}

// PrivateLine 是私有快照中的线路完整配置（字段与插件 railway_routes.yml 一一对应）。
type PrivateLine struct {
	ID                   string   `json:"id"`
	SystemID             string   `json:"systemId"`
	Name                 string   `json:"name"`
	Color                string   `json:"color"`
	BossbarStations      []string `json:"bossbarStations"`
	BossbarArrivalNotice string   `json:"bossbarArrivalNotice,omitempty"`
	BossbarColor         string   `json:"bossbarColor,omitempty"`
	NoticeArrival        string   `json:"noticeArrival,omitempty"`
	NoticeDeparture      string   `json:"noticeDeparture,omitempty"`
}

// RevenueEventData 是 revenue.event 的负载。
type RevenueEventData struct {
	EventID     string             `json:"eventId"`
	Type        string             `json:"type"` // ticket | card_charge | card_ride
	Player      Player             `json:"player"`
	Amount      float64            `json:"amount"`
	Detail      string             `json:"detail,omitempty"`
	Allocations map[string]float64 `json:"allocations,omitempty"` // systemId → 分配金额
	Time        int64              `json:"time"`
}

// EventAckData 是 event.ack 的负载。
type EventAckData struct {
	EventIDs []string `json:"eventIds"`
}
