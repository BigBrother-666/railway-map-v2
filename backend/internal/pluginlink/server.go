// Package pluginlink 实现对游戏插件的内部 WebSocket 服务端（见 docs/BACKEND_PROMPT.md §4）。
// 插件作为出站客户端连入 /internal/plugin，共享密钥握手；单条长连接双向：
// 插件推送快照 / 遥测 / 购票结果 / 绑定，后端下发 ping / purchase.request / sync.request。
package pluginlink

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"railway-map-backend/internal/geo"
	"railway-map-backend/internal/model"
	"railway-map-backend/internal/realtime"
	"railway-map-backend/internal/store"
)

// PurchaseRouter 接收插件回执的购票结果，关联挂起请求（由 purchase 包实现）。
type PurchaseRouter interface {
	Deliver(result model.PurchaseResult)
}

// Server 维护与插件的单条连接及其收发。
type Server struct {
	sharedToken    string
	heartbeat      time.Duration
	cache          *geo.Cache
	agg            *realtime.Aggregator
	store          *store.Store
	purchaseRouter PurchaseRouter
	logger         *slog.Logger

	mu       sync.RWMutex
	conn     *websocket.Conn
	writeMu  sync.Mutex // 串行化写
	serverID string
}

// Options 聚合构建 Server 所需依赖。
type Options struct {
	SharedToken    string
	Heartbeat      time.Duration
	Cache          *geo.Cache
	Aggregator     *realtime.Aggregator
	Store          *store.Store
	PurchaseRouter PurchaseRouter
	Logger         *slog.Logger
}

// NewServer 创建插件链路服务端。
func NewServer(o Options) *Server {
	return &Server{
		sharedToken:    o.SharedToken,
		heartbeat:      o.Heartbeat,
		cache:          o.Cache,
		agg:            o.Aggregator,
		store:          o.Store,
		purchaseRouter: o.PurchaseRouter,
		logger:         o.Logger,
	}
}

// SetPurchaseRouter 注入购票回执路由（编排器在 Server 之后构建，故用 setter）。
func (s *Server) SetPurchaseRouter(r PurchaseRouter) {
	s.purchaseRouter = r
}

// SetAggregator 注入列车聚合器（聚合器需广播器、广播器需快照器，故用 setter 解环）。
func (s *Server) SetAggregator(agg *realtime.Aggregator) {
	s.agg = agg
}

// Online 表示当前是否有插件连接。
func (s *Server) Online() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn != nil
}

// Send 向插件发送一条消息（线程安全，串行化写）。未连接返回 false。
func (s *Server) Send(env model.Envelope) bool {
	s.mu.RLock()
	conn := s.conn
	s.mu.RUnlock()
	if conn == nil {
		return false
	}
	b, err := json.Marshal(env)
	if err != nil {
		return false
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		s.logger.Warn("Failed to send message to plugin", "type", env.Type, "err", err)
		return false
	}
	return true
}

// RequestSync 请求插件补推快照（what: geo|systems|lines|all）。
func (s *Server) RequestSync(what string) {
	data, _ := json.Marshal(model.SyncRequestData{What: what})
	s.logger.Info("Requesting plugin sync", "what", what)
	s.Send(newEnvelope(model.TypeSyncRequest, "", data))
}

// HandlePlugin 是 /internal/plugin 的 handler：校验 token、升级、读循环。
func (s *Server) HandlePlugin(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		s.logger.Warn("Rejected unauthorized plugin connection", "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.logger.Warn("Failed to accept plugin WebSocket", "err", err)
		return
	}
	// 同一时刻只保留一条插件连接；新连接挤掉旧的
	s.setConn(conn)
	s.logger.Info("Plugin connected", "remote", r.RemoteAddr)

	ctx := r.Context()
	stopHeartbeat := s.startHeartbeat(ctx)
	defer stopHeartbeat()
	defer s.clearConn(conn)

	conn.SetReadLimit(64 << 20) // geojson 快照可能较大（数十 MB）
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			s.logger.Info("Plugin connection closed", "err", err)
			return
		}
		s.handleMessage(data)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
		return false
	}
	return auth[len(prefix):] == s.sharedToken
}

func (s *Server) setConn(conn *websocket.Conn) {
	s.mu.Lock()
	old := s.conn
	s.conn = conn
	s.mu.Unlock()
	if old != nil {
		_ = old.Close(websocket.StatusPolicyViolation, "superseded")
	}
}

func (s *Server) clearConn(conn *websocket.Conn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// startHeartbeat 周期向插件发 ping，返回停止函数。
func (s *Server) startHeartbeat(ctx context.Context) func() {
	ticker := time.NewTicker(s.heartbeat)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				s.Send(newEnvelope(model.TypePing, "", nil))
			case <-done:
				ticker.Stop()
				return
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

func newEnvelope(msgType, id string, data json.RawMessage) model.Envelope {
	return model.Envelope{Type: msgType, ID: id, TS: time.Now().UnixMilli(), Data: data}
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
