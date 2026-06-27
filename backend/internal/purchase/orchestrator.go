// Package purchase 编排在线购票：把前端下单转给插件，按 requestId 关联回执，带超时
// （见 docs/BACKEND_PROMPT.md §8）。
package purchase

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"railway-map-backend/internal/model"
)

// Sender 向插件发送 purchase.request（由 pluginlink.Server 实现）。
type Sender interface {
	Send(env model.Envelope) bool
	Online() bool
}

// pending 是一个挂起的购票请求。
type pending struct {
	ch chan model.PurchaseResult
}

// Orchestrator 维护挂起请求并关联插件回执。
type Orchestrator struct {
	sender  Sender
	timeout time.Duration

	mu      sync.Mutex
	pending map[string]*pending
}

// New 创建编排器。
func New(sender Sender, timeout time.Duration) *Orchestrator {
	return &Orchestrator{
		sender:  sender,
		timeout: timeout,
		pending: make(map[string]*pending),
	}
}

// Submit 提交一次购票：生成 requestId，发给插件，阻塞等待回执或超时。
// playerUUID/playerName 取自已鉴权会话；nodeIds/lineIdSequence 为前端选定路线。
func (o *Orchestrator) Submit(playerUUID, playerName string, req model.PurchaseRequest) model.PurchaseResult {
	if !o.sender.Online() {
		return model.PurchaseResult{Success: false, Reason: "internal-error"}
	}
	requestID := uuid.NewString()

	wire := model.PurchaseRequestWire{
		RequestID:      requestID,
		PlayerUUID:     playerUUID,
		PlayerName:     playerName,
		NodeIDs:        req.NodeIDs,
		LineIDSequence: req.LineIDSequence,
		SpeedKph:       req.SpeedKph,
		MaxUses:        req.MaxUses,
	}
	data, _ := json.Marshal(wire)

	p := &pending{ch: make(chan model.PurchaseResult, 1)}
	o.mu.Lock()
	o.pending[requestID] = p
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.pending, requestID)
		o.mu.Unlock()
	}()

	env := model.Envelope{Type: model.TypePurchaseRequest, ID: requestID, TS: time.Now().UnixMilli(), Data: data}
	if !o.sender.Send(env) {
		return model.PurchaseResult{RequestID: requestID, Success: false, Reason: "internal-error"}
	}

	select {
	case result := <-p.ch:
		return result
	case <-time.After(o.timeout):
		return model.PurchaseResult{RequestID: requestID, Success: false, Reason: "internal-error"}
	}
}

// Deliver 实现 pluginlink.PurchaseRouter：把插件回执投递给对应挂起请求。
func (o *Orchestrator) Deliver(result model.PurchaseResult) {
	o.mu.Lock()
	p := o.pending[result.RequestID]
	o.mu.Unlock()
	if p != nil {
		select {
		case p.ch <- result:
		default:
		}
	}
}
