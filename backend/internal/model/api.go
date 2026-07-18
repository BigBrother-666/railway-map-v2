package model

import "railway-map-backend/internal/config"

// PurchaseRequest 是前端 POST /api/v1/purchase 的请求体（路线在前端已选定）。
type PurchaseRequest struct {
	NodeIDs        []string `json:"nodeIds"`
	LineIDSequence []string `json:"lineIdSequence"`
	SpeedKph       *float64 `json:"speedKph,omitempty"`
	MaxUses        *int     `json:"maxUses,omitempty"`
	// ThroughContinuation 表示这是联程票首段之后的续段：同一次购票动作的一部分，
	// 购票频率限制对其跳过间隔检查，避免联程票第二段被自己的冷却拦下。
	ThroughContinuation bool `json:"throughContinuation,omitempty"`
}

// PurchaseRequestWire 是后端转发给插件的 purchase.request 负载。
type PurchaseRequestWire struct {
	RequestID      string   `json:"requestId"`
	PlayerUUID     string   `json:"playerUuid"`
	PlayerName     string   `json:"playerName"`
	NodeIDs        []string `json:"nodeIds"`
	LineIDSequence []string `json:"lineIdSequence"`
	SpeedKph       *float64 `json:"speedKph,omitempty"`
	MaxUses        *int     `json:"maxUses,omitempty"`
}

// PurchaseResult 是插件回执 / 前端响应的购票结果。
type PurchaseResult struct {
	RequestID    string  `json:"requestId,omitempty"`
	Success      bool    `json:"success"`
	Reason       string  `json:"reason,omitempty"`
	TicketName   string  `json:"ticketName,omitempty"`
	Price        float64 `json:"price"`
	BalanceAfter float64 `json:"balanceAfter"`
}

// RouteSegment 是路径查询结果中的一段。
type RouteSegment struct {
	LineID   string  `json:"lineId"`
	Distance float64 `json:"distance"`
	SystemID string  `json:"systemId"`
}

type StationStep struct {
	StationName  string `json:"stationName"`
	DepartLineID string `json:"departLineId,omitempty"`
}

type FareDetail struct {
	SystemID   string  `json:"systemId"`
	SystemName string  `json:"systemName"`
	Distance   float64 `json:"distance"`
	Price      float64 `json:"price"`
	Rate       float64 `json:"rate"`
}

// RoutePath 是路径查询结果（见 docs/FRONTEND_PROMPT.md §4.6）。
type RoutePath struct {
	Stations       []string       `json:"stations"`
	StationSteps   []StationStep  `json:"stationSteps,omitempty"`
	NodeIDs        []string       `json:"nodeIds"`
	LineIDSequence []string       `json:"lineIdSequence"`
	Distance       float64        `json:"distance"`
	Segments       []RouteSegment `json:"segments"`
	FareDetails    []FareDetail   `json:"fareDetails,omitempty"`
	EstimatedFare  float64        `json:"estimatedFare"`
}

// Meta 是 GET /api/v1/meta 的响应。
type Meta struct {
	GeoVersion string `json:"geoVersion"`
	Online     bool   `json:"online"`
	ServerTime int64  `json:"serverTime"`
}

// Player 是登录玩家身份（GET /api/v1/auth/me）。
type Player struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type FrontendBootstrap struct {
	config.FrontendConfig
	TestAuthEnabled bool     `json:"testAuthEnabled"`
	TestAuthUUIDs   []string `json:"testAuthUUIDs,omitempty"`
}

type TestLoginRequest struct {
	UUID string `json:"uuid"`
}

type RideEventData struct {
	TrainID      string   `json:"trainId"`
	TrainType    string   `json:"trainType"`
	NodeID       string   `json:"nodeId"`
	StationName  string   `json:"stationName,omitempty"`
	Passengers   []Player `json:"passengers"`
	ArrivedAt    int64    `json:"arrivedAt,omitempty"`
	LineID       string   `json:"lineId,omitempty"`
	Express      bool     `json:"express"`
	RouteNodeIDs []string `json:"routeNodeIds,omitempty"`
	StartStation string   `json:"startStation,omitempty"`
	EndStation   string   `json:"endStation,omitempty"`
	Distance     float64  `json:"distance,omitempty"`
	PaidFare     float64  `json:"paidFare,omitempty"`
}

type RidePaymentData struct {
	TrainID      string   `json:"trainId"`
	TrainType    string   `json:"trainType"`
	Player       Player   `json:"player"`
	Express      bool     `json:"express"`
	RouteNodeIDs []string `json:"routeNodeIds"`
	StartStation string   `json:"startStation"`
	EndStation   string   `json:"endStation"`
	Distance     float64  `json:"distance"`
	PaidFare     float64  `json:"paidFare"`
	PaidAt       int64    `json:"paidAt,omitempty"`
}

type RideHistoryItem struct {
	ID           int64    `json:"id"`
	TrainID      string   `json:"trainId"`
	TrainType    string   `json:"trainType"`
	Express      bool     `json:"express"`
	StartedAt    int64    `json:"startedAt"`
	EndedAt      int64    `json:"endedAt"`
	Distance     float64  `json:"distance"`
	StartStation string   `json:"startStation"`
	EndStation   string   `json:"endStation"`
	PaidFare     float64  `json:"paidFare,omitempty"`
	NodeIDs      []string `json:"nodeIds"`
}

type RideHistoryResponse struct {
	Items      []RideHistoryItem `json:"items"`
	Page       int               `json:"page"`
	PageSize   int               `json:"pageSize"`
	Total      int               `json:"total"`
	TotalPages int               `json:"totalPages"`
}

// APIError 是统一错误体：{ "error": { "code": "...", "message": "..." } }。
type APIError struct {
	Error APIErrorBody `json:"error"`
}

type APIErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
