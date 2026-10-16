package geo

import (
	"encoding/json"

	"railway-map-backend/internal/model"
)

// 私有快照（铁路系统的创建者 / 成员 / 营收，线路的完整配置）只供个人中心鉴权与编辑使用，
// 不经任何公开接口输出。

// SetPrivateSystems 整体替换铁路系统私有快照并落库。payload 为数组 JSON。
func (c *Cache) SetPrivateSystems(payload []byte) error {
	var list []model.PrivateSystem
	if err := json.Unmarshal(payload, &list); err != nil {
		return err
	}
	c.mu.Lock()
	c.privateSystems = list
	c.mu.Unlock()
	c.persist("systems_private", "", payload)
	return nil
}

// SetPrivateLines 整体替换线路私有快照并落库。payload 为数组 JSON。
func (c *Cache) SetPrivateLines(payload []byte) error {
	var list []model.PrivateLine
	if err := json.Unmarshal(payload, &list); err != nil {
		return err
	}
	c.mu.Lock()
	c.privateLines = list
	c.mu.Unlock()
	c.persist("lines_private", "", payload)
	return nil
}

// PrivateSystems 返回铁路系统私有快照（只读，调用方不得修改）。
func (c *Cache) PrivateSystems() []model.PrivateSystem {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.privateSystems
}

// PrivateSystem 按 ID 查铁路系统私有信息。
func (c *Cache) PrivateSystem(id string) (model.PrivateSystem, bool) {
	for _, s := range c.PrivateSystems() {
		if s.ID == id {
			return s, true
		}
	}
	return model.PrivateSystem{}, false
}

// PrivateLines 返回线路私有快照（只读）。
func (c *Cache) PrivateLines() []model.PrivateLine {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.privateLines
}

// PrivateLine 按 ID 查线路完整配置。
func (c *Cache) PrivateLine(id string) (model.PrivateLine, bool) {
	for _, l := range c.PrivateLines() {
		if l.ID == id {
			return l, true
		}
	}
	return model.PrivateLine{}, false
}

func (c *Cache) restorePrivate() {
	if snap, err := c.store.LoadSnapshot("systems_private"); err == nil && snap != nil {
		_ = json.Unmarshal(snap.Payload, &c.privateSystems)
	}
	if snap, err := c.store.LoadSnapshot("lines_private"); err == nil && snap != nil {
		_ = json.Unmarshal(snap.Payload, &c.privateLines)
	}
}
