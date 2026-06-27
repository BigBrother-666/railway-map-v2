// Package ws 实现对前端的实时列车广播 hub（见 docs/BACKEND_PROMPT.md §7.2）。
// 经典 hub 模式：每个前端连接一个有缓冲的发送 channel，慢客户端丢弃最旧帧（背压）。
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"railway-map-backend/internal/model"
)

// Snapshotter 提供前端连接时的首帧全量列车。
type Snapshotter interface {
	Snapshot() []json.RawMessage
}

// envelope 是前端 WS 的消息信封（复用插件侧约定：type/ts/data）。
type envelope struct {
	Type string `json:"type"`
	TS   int64  `json:"ts"`
	Data any    `json:"data,omitempty"`
}

// client 是一个前端连接。
type client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub 管理所有前端连接并广播消息，实现 realtime.Broadcaster。
type Hub struct {
	mu          sync.RWMutex
	clients     map[*client]struct{}
	sendBuffer  int
	snapshotter Snapshotter
	logger      *slog.Logger
}

// NewHub 创建 hub。sendBuffer 为每客户端发送缓冲大小。
func NewHub(sendBuffer int, snapshotter Snapshotter, logger *slog.Logger) *Hub {
	return &Hub{
		clients:     make(map[*client]struct{}),
		sendBuffer:  sendBuffer,
		snapshotter: snapshotter,
		logger:      logger,
	}
}

// SetSnapshotter 注入首帧快照来源（聚合器需广播器、hub 首帧需聚合器，故用 setter 解环）。
func (h *Hub) SetSnapshotter(s Snapshotter) {
	h.snapshotter = s
}

// Broadcast 实现 realtime.Broadcaster：把消息扇出到所有前端连接。
func (h *Hub) Broadcast(msgType string, payload any) {
	msg := encode(msgType, payload)
	if msg == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		h.enqueue(c, msg)
	}
}

// enqueue 尝试入队；满则丢弃最旧一帧再入队（实时数据过期即无意义）。
func (h *Hub) enqueue(c *client, msg []byte) {
	select {
	case c.send <- msg:
	default:
		select {
		case <-c.send: // 丢弃最旧
		default:
		}
		select {
		case c.send <- msg:
		default:
		}
	}
}

// HandleWS 是 /api/v1/realtime 的 handler：升级连接、发首帧 snapshot、读写泵。
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// 允许跨源（生产建议按 publicBaseUrl 收紧）
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.logger.Warn("Failed to accept frontend WebSocket", "err", err)
		return
	}

	c := &client{conn: conn, send: make(chan []byte, h.sendBuffer)}
	h.add(c)
	defer h.remove(c)
	h.logger.Info("Frontend WebSocket connected", "remote", r.RemoteAddr)

	// 首帧：当前全部列车
	if h.snapshotter != nil {
		if msg := encode(model.WSSnapshot, h.snapshotter.Snapshot()); msg != nil {
			h.enqueue(c, msg)
		}
	}

	ctx := r.Context()
	go h.readPump(ctx, c)
	h.writePump(ctx, c)
}

// readPump 读取（仅用于感知客户端 pong / 关闭），丢弃内容。
func (h *Hub) readPump(ctx context.Context, c *client) {
	for {
		if _, _, err := c.conn.Read(ctx); err != nil {
			return
		}
	}
}

// writePump 把 send channel 的消息写出，并周期发 ping 心跳。
func (h *Hub) writePump(ctx context.Context, c *client) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			if msg := encode(model.WSPing, nil); msg != nil {
				h.enqueue(c, msg)
			}
		}
	}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	count := len(h.clients)
	h.mu.Unlock()
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	h.logger.Info("Frontend WebSocket disconnected", "clients", count)
}

func encode(msgType string, payload any) []byte {
	b, err := json.Marshal(envelope{Type: msgType, TS: time.Now().UnixMilli(), Data: payload})
	if err != nil {
		return nil
	}
	return b
}
