// Package realtime 聚合插件推送的列车遥测，维护当前列车状态并把变化广播给前端
// （见 docs/BACKEND_PROMPT.md §7）。
package realtime

import (
	"encoding/json"
	"sync"
	"time"

	"railway-map-backend/internal/model"
)

// Broadcaster 是前端 WS hub 需实现的广播接口（解耦 realtime 与 ws，避免循环依赖）。
type Broadcaster interface {
	// Broadcast 向所有前端连接推送一条消息（msgType + 任意可序列化负载）。
	Broadcast(msgType string, payload any)
}

// trainEntry 是单列车的最近状态与更新时间。
type trainEntry struct {
	raw      json.RawMessage // 原始 JSON（保留插件的全部扩展字段，透传前端）
	trainID  string
	lastSeen time.Time
}

// Aggregator 维护当前所有列车状态，按超时剔除，并把增量广播给前端。
type Aggregator struct {
	mu      sync.RWMutex
	trains  map[string]*trainEntry
	timeout time.Duration
	bc      Broadcaster
}

// NewAggregator 创建聚合器。timeout 内未更新的列车视为消失。
func NewAggregator(timeout time.Duration, bc Broadcaster) *Aggregator {
	return &Aggregator{
		trains:  make(map[string]*trainEntry),
		timeout: timeout,
		bc:      bc,
	}
}

// Update 处理插件推送的一批列车遥测：刷新状态、广播 update。
func (a *Aggregator) Update(raws []json.RawMessage) {
	now := time.Now()
	updated := make([]json.RawMessage, 0, len(raws))

	a.mu.Lock()
	for _, raw := range raws {
		var t model.Train
		if err := json.Unmarshal(raw, &t); err != nil || t.TrainID == "" {
			continue
		}
		a.trains[t.TrainID] = &trainEntry{raw: raw, trainID: t.TrainID, lastSeen: now}
		updated = append(updated, raw)
	}
	a.mu.Unlock()

	if len(updated) > 0 && a.bc != nil {
		a.bc.Broadcast(model.WSUpdate, rawArray(updated))
	}
}

// Remove 主动移除指定列车并广播 remove（插件 realtime.removed 触发，矿车销毁即时消失）。
func (a *Aggregator) Remove(ids []string) {
	var removed []string
	a.mu.Lock()
	for _, id := range ids {
		if _, ok := a.trains[id]; ok {
			delete(a.trains, id)
			removed = append(removed, id)
		}
	}
	a.mu.Unlock()
	if len(removed) > 0 && a.bc != nil {
		a.bc.Broadcast(model.WSRemove, removed)
	}
}

// Snapshot 返回当前所有列车的原始 JSON（前端连接时先发一帧 / GET /trains）。
func (a *Aggregator) Snapshot() []json.RawMessage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]json.RawMessage, 0, len(a.trains))
	for _, e := range a.trains {
		out = append(out, e.raw)
	}
	return out
}

// Sweep 剔除超时未更新的列车，并广播 remove。应由定时器周期调用。
func (a *Aggregator) Sweep() {
	now := time.Now()
	var removed []string

	a.mu.Lock()
	for id, e := range a.trains {
		if now.Sub(e.lastSeen) > a.timeout {
			delete(a.trains, id)
			removed = append(removed, id)
		}
	}
	a.mu.Unlock()

	if len(removed) > 0 && a.bc != nil {
		a.bc.Broadcast(model.WSRemove, removed)
	}
}

// StartSweeper 启动周期剔除（每 timeout/2 扫一次），返回停止函数。
func (a *Aggregator) StartSweeper() (stop func()) {
	interval := a.timeout / 2
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				a.Sweep()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

// rawArray 把一组 RawMessage 包成一个 JSON 数组的 RawMessage，便于作为广播负载。
func rawArray(items []json.RawMessage) json.RawMessage {
	b, _ := json.Marshal(items)
	return b
}
