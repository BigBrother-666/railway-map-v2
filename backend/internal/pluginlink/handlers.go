package pluginlink

import (
	"encoding/json"

	"railway-map-backend/internal/model"
)

// handleMessage 解析并分派插件发来的一条消息。
func (s *Server) handleMessage(data []byte) {
	var env model.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		s.logger.Warn("Failed to parse plugin message", "err", err)
		return
	}
	switch env.Type {
	case model.TypeHello:
		s.onHello(env)
	case model.TypePong:
		// 心跳应答，无需处理
	case model.TypeSnapshotGeo:
		s.onSnapshotGeo(env)
	case model.TypeSnapshotSystems:
		s.cache.SetSystems(s.extractArray(env.Data, "systems"))
		s.logger.Info("Received systems snapshot")
	case model.TypeSnapshotLines:
		s.cache.SetLines(s.extractArray(env.Data, "lines"))
		s.logger.Info("Received lines snapshot")
	case model.TypeRealtimeTrains:
		s.onRealtimeTrains(env)
	case model.TypeRealtimeRemoved:
		s.onRealtimeRemoved(env)
	case model.TypePurchaseResult:
		s.onPurchaseResult(env)
	case model.TypeRideEvent:
		s.onRideEvent(env)
	case model.TypeRidePayment:
		s.onRidePayment(env)
	case model.TypeAuthBind:
		s.onAuthBind(env, true)
	case model.TypeAuthUnbind:
		s.onAuthBind(env, false)
	default:
		s.logger.Warn("Unknown plugin message type", "type", env.Type)
	}
}

func (s *Server) onRidePayment(env model.Envelope) {
	var d model.RidePaymentData
	if err := json.Unmarshal(env.Data, &d); err != nil || d.TrainID == "" || d.Player.UUID == "" {
		s.logger.Warn("Invalid ride.payment payload", "err", err)
		return
	}
	if s.store == nil {
		return
	}
	if err := s.store.RecordRidePayment(d); err != nil {
		s.logger.Warn("Failed to record ride payment", "err", err, "trainId", d.TrainID, "player", d.Player.UUID)
	}
}

func (s *Server) onRideEvent(env model.Envelope) {
	var d model.RideEventData
	if err := json.Unmarshal(env.Data, &d); err != nil || d.TrainID == "" || d.NodeID == "" {
		s.logger.Warn("Invalid ride.event payload", "err", err)
		return
	}
	if s.store == nil {
		return
	}
	if err := s.store.RecordRideEvent(d); err != nil {
		s.logger.Warn("Failed to record ride event", "err", err, "trainId", d.TrainID)
	}
}

// onHello 记录元信息并回 welcome，随后请求全量同步。
func (s *Server) onHello(env model.Envelope) {
	var hello model.HelloData
	_ = json.Unmarshal(env.Data, &hello)
	s.mu.Lock()
	s.serverID = hello.ServerID
	s.mu.Unlock()
	s.logger.Info("Plugin handshake received", "serverId", hello.ServerID, "version", hello.PluginVersion)

	welcome, _ := json.Marshal(model.WelcomeData{
		ServerTime:      nowMillis(),
		AcceptedVersion: hello.PluginVersion,
	})
	s.Send(newEnvelope(model.TypeWelcome, env.ID, welcome))

	// 插件握手后会主动全量同步；这里仍主动请求一次，保证拿到最新数据
	s.RequestSync("all")
}

// onSnapshotGeo 整体替换 geo 缓存。data 形如 { "featureCollection": <FeatureCollection> }。
func (s *Server) onSnapshotGeo(env model.Envelope) {
	var d model.SnapshotGeoData
	if err := json.Unmarshal(env.Data, &d); err != nil || len(d.FeatureCollection) == 0 {
		s.logger.Warn("Invalid snapshot.geo payload", "err", err)
		return
	}
	s.cache.SetGeojson(d.FeatureCollection)
	s.logger.Info("Received geo snapshot", "version", s.cache.GeoVersion())
}

// onRealtimeTrains 把列车遥测交给聚合器（广播给前端）。
func (s *Server) onRealtimeTrains(env model.Envelope) {
	var d model.RealtimeTrainsData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return
	}
	s.agg.Update(d.Trains)
}

// onRealtimeRemoved 处理矿车销毁：立即移除并广播 remove。
// 兼容两种负载：{ "trainId": "x" } 或 { "trainIds": ["x","y"] }。
func (s *Server) onRealtimeRemoved(env model.Envelope) {
	var d struct {
		TrainID  string   `json:"trainId"`
		TrainIDs []string `json:"trainIds"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return
	}
	ids := d.TrainIDs
	if d.TrainID != "" {
		ids = append(ids, d.TrainID)
	}
	if len(ids) > 0 {
		s.agg.Remove(ids)
	}
}

// onPurchaseResult 关联挂起的购票请求。
func (s *Server) onPurchaseResult(env model.Envelope) {
	var result model.PurchaseResult
	if err := json.Unmarshal(env.Data, &result); err != nil {
		return
	}
	if result.RequestID == "" {
		result.RequestID = env.ID
	}
	if s.purchaseRouter != nil {
		s.purchaseRouter.Deliver(result)
	}
}

// onAuthBind 维护「允许登录网页」白名单。
func (s *Server) onAuthBind(env model.Envelope, bind bool) {
	var d model.AuthBindData
	if err := json.Unmarshal(env.Data, &d); err != nil || d.UUID == "" {
		return
	}
	if s.store == nil {
		return
	}
	if bind {
		if err := s.store.UpsertBoundPlayer(d.UUID, d.Name); err != nil {
			s.logger.Warn("Failed to upsert auth binding", "err", err)
		}
	} else {
		if err := s.store.DeleteBoundPlayer(d.UUID); err != nil {
			s.logger.Warn("Failed to delete auth binding", "err", err)
		}
	}
}

// extractArray 从 {"key":[...]} 形态的负载里取出数组原始 JSON；取不到返回 "[]"。
func (s *Server) extractArray(data json.RawMessage, key string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return []byte("[]")
	}
	if arr, ok := m[key]; ok {
		return arr
	}
	return []byte("[]")
}
