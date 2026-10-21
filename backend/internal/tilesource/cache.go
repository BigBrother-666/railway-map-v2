package tilesource

import (
	"container/list"
	"sync"
	"time"
)

// tileCache 是按字节数限容的 LRU 瓦片缓存，条目带过期时间（过期不删除，只按容量淘汰）。
type tileCache struct {
	mu    sync.Mutex
	ll    *list.List // 前端 = 最近使用
	items map[string]*list.Element
	bytes int64
}

type cacheEntry struct {
	key         string
	data        []byte
	contentType string
	expires     time.Time
}

func newTileCache() *tileCache {
	return &tileCache{ll: list.New(), items: make(map[string]*list.Element)}
}

// get 返回条目；过期的条目仍返回（stale 为 true），由调用方先用旧数据并在后台刷新。
func (c *tileCache) get(key string, now time.Time) (e *cacheEntry, stale, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false, false
	}
	c.ll.MoveToFront(el)
	e = el.Value.(*cacheEntry)
	return e, now.After(e.expires), true
}

// put 写入条目并淘汰最久未用的条目直到总字节数不超过 maxBytes（单条超过上限则不缓存）。
func (c *tileCache) put(e *cacheEntry, maxBytes int64) {
	size := int64(len(e.data))
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[e.key]; ok {
		c.remove(el)
	}
	if size > maxBytes {
		return
	}
	c.items[e.key] = c.ll.PushFront(e)
	c.bytes += size
	for c.bytes > maxBytes {
		c.remove(c.ll.Back())
	}
}

func (c *tileCache) remove(el *list.Element) {
	e := c.ll.Remove(el).(*cacheEntry)
	delete(c.items, e.key)
	c.bytes -= int64(len(e.data))
}

// clear 清空缓存（来源配置变更后旧瓦片失效）。
func (c *tileCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = make(map[string]*list.Element)
	c.bytes = 0
}

// stats 返回当前条目数与字节数。
func (c *tileCache) stats() (int, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.bytes
}
