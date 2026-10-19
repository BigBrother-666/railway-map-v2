// Package pluginconfig 把 web 端对铁路系统 / 线路 / 车站别名 / 车站组的修改转发给插件执行，
// 按 requestId 关联插件回执（config.result），带超时。插件是 yml 配置的唯一写入方，
// 写入后自行局部重载并推送最新快照。权限判定在后端完成，插件只做数据校验。
package pluginconfig

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"railway-map-backend/internal/model"
)

// 操作类型（config.request 的 op）。
const (
	OpSystemUpdate       = "system.update"
	OpSystemDelete       = "system.delete"
	OpSystemLogo         = "system.logo"
	OpLineUpdate         = "line.update"
	OpLineDelete         = "line.delete"
	OpStationAliasUpsert = "station_alias.upsert"
	OpStationAliasDelete = "station_alias.delete"
	OpStationGroupUpsert = "station_group.upsert"
	OpStationGroupDelete = "station_group.delete"
)

var (
	// ErrPluginOffline 表示插件未连接。
	ErrPluginOffline = errors.New("plugin offline")
	// ErrTimeout 表示插件未在超时内回执（插件可能已执行，前端应刷新确认）。
	ErrTimeout = errors.New("plugin config timeout")
)

// Sender 向插件发送消息（由 pluginlink.Server 实现）。
type Sender interface {
	Send(env model.Envelope) bool
	Online() bool
}

// Requester 维护挂起的配置请求。
type Requester struct {
	sender  Sender
	timeout func() time.Duration // 读当前配置，web 端修改后立即生效

	mu      sync.Mutex
	pending map[string]chan model.ConfigResult
}

// New 创建 Requester。timeout 返回当前等待插件回执的超时。
func New(sender Sender, timeout func() time.Duration) *Requester {
	return &Requester{sender: sender, timeout: timeout, pending: make(map[string]chan model.ConfigResult)}
}

// Do 发送一次配置操作并阻塞等待回执。返回的 ConfigResult.Success=false 表示插件拒绝（Reason 为原因码）。
func (r *Requester) Do(op string, actor model.Player, payload any) (model.ConfigResult, error) {
	if !r.sender.Online() {
		return model.ConfigResult{}, ErrPluginOffline
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return model.ConfigResult{}, err
	}
	requestID := uuid.NewString()
	data, _ := json.Marshal(model.ConfigRequestWire{
		RequestID: requestID, Op: op, ActorUUID: actor.UUID, ActorName: actor.Name, Payload: body,
	})

	ch := make(chan model.ConfigResult, 1)
	r.mu.Lock()
	r.pending[requestID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, requestID)
		r.mu.Unlock()
	}()

	env := model.Envelope{Type: model.TypeConfigRequest, ID: requestID, TS: time.Now().UnixMilli(), Data: data}
	if !r.sender.Send(env) {
		return model.ConfigResult{}, ErrPluginOffline
	}
	select {
	case res := <-ch:
		return res, nil
	case <-time.After(r.timeout()):
		return model.ConfigResult{}, ErrTimeout
	}
}

// Deliver 实现 pluginlink.ConfigRouter：把插件回执投递给对应挂起请求。
func (r *Requester) Deliver(res model.ConfigResult) {
	r.mu.Lock()
	ch := r.pending[res.RequestID]
	r.mu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}
