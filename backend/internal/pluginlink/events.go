package pluginlink

import (
	"encoding/json"
	"time"

	"railway-map-backend/internal/model"
)

// replayFinalizeDelay 是离线补发停顿多久后结算补发列车的乘车历史（等同一列车的事件补发完）。
const replayFinalizeDelay = 5 * time.Second

// onConfigResult 关联挂起的配置操作请求。
func (s *Server) onConfigResult(env model.Envelope) {
	var res model.ConfigResult
	if err := json.Unmarshal(env.Data, &res); err != nil {
		s.logger.Warn("Invalid config.result payload", "err", err)
		return
	}
	if res.RequestID == "" {
		res.RequestID = env.ID
	}
	if s.configRouter != nil {
		s.configRouter.Deliver(res)
	}
}

// onRevenueEvent 落库营收事件（eventId 幂等），成功后回 ack。
func (s *Server) onRevenueEvent(env model.Envelope) {
	var d model.RevenueEventData
	if err := json.Unmarshal(env.Data, &d); err != nil || d.EventID == "" {
		s.logger.Warn("Invalid revenue.event payload", "err", err)
		return
	}
	if s.store == nil {
		return
	}
	if err := s.store.RecordRevenueEvent(d); err != nil {
		s.logger.Warn("Failed to record revenue event", "err", err, "eventId", d.EventID)
		return
	}
	s.ack(d.EventID)
}

// ack 通知插件该事件已落库，可从 outbox 删除。旧版插件不带 eventId 时不回。
func (s *Server) ack(eventID string) {
	if eventID == "" {
		return
	}
	data, _ := json.Marshal(model.EventAckData{EventIDs: []string{eventID}})
	s.Send(newEnvelope(model.TypeEventAck, "", data))
}

// scheduleReplayFinalize 记录补发事件涉及的列车，并在补发停顿 replayFinalizeDelay 后统一结算。
// 实时链路里乘车历史在列车从实时数据中消失时结算；补发事件对应的列车通常早已消失，不会再触发结算。
func (s *Server) scheduleReplayFinalize(trainID string) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	if s.replayTrains == nil {
		s.replayTrains = make(map[string]struct{})
	}
	s.replayTrains[trainID] = struct{}{}
	if s.replayTimer != nil {
		s.replayTimer.Stop()
	}
	s.replayTimer = time.AfterFunc(replayFinalizeDelay, s.finalizeReplayed)
}

func (s *Server) finalizeReplayed() {
	s.replayMu.Lock()
	trains := s.replayTrains
	s.replayTrains = nil
	s.replayMu.Unlock()
	for id := range trains {
		// 仍在运行的列车交给实时链路在其消失时结算，避免结算出不完整的行程
		if s.agg != nil && s.agg.Has(id) {
			continue
		}
		if err := s.store.FinalizeRide(id); err != nil {
			s.logger.Warn("Failed to finalize replayed ride", "err", err, "trainId", id)
		}
	}
	if len(trains) > 0 {
		s.logger.Info("Finalized replayed rides", "trains", len(trains))
	}
}
