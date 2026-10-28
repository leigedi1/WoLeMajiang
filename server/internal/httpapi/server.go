// Package httpapi HTTP/WS 服务层: 健康检查、游客身份 REST 接口与 WebSocket 会话。
// 本层只做传输与编解码, 不包含麻将规则; 权威状态在 room.Registry。
package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"majiang-server/internal/config"
	"majiang-server/internal/room"
	"majiang-server/internal/sig"
	"majiang-server/internal/store"
)

// Router 依赖集合。
type Router struct {
	cfg       config.Config
	log       *slog.Logger
	store     *store.Store
	rooms     *room.Registry
	sig       *sig.Registry
	hub       *hub
	startedAt time.Time
}

// NewRouter 创建路由(含 CORS、请求日志与 WS 枢纽)。不启动后台任务, 便于测试。
func NewRouter(cfg config.Config, log *slog.Logger, st *store.Store, rooms *room.Registry) http.Handler {
	_, h := NewServer(cfg, log, st, rooms)
	return h
}

// NewServer 创建路由器并返回其句柄(用于启动房间自动推进等后台任务)。
func NewServer(cfg config.Config, log *slog.Logger, st *store.Store, rooms *room.Registry) (*Router, http.Handler) {
	// 信令 Origin 白名单与 CORS 白名单共用: 线上挂在域名子路径时,
	// 「同源」判定依赖它兜底(详见 sig.originAllowed 的注释)。
	sigCfg := sig.DefaultConfig()
	sigCfg.AllowedOrigins = cfg.HTTP.AllowedOrigins
	sigReg := sig.NewRegistry(sigCfg)
	r := &Router{
		cfg:       cfg,
		log:       log,
		store:     st,
		rooms:     rooms,
		sig:       sigReg,
		hub:       newHub(),
		startedAt: time.Now(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", r.handleHealthz)
	mux.HandleFunc("GET /readyz", r.handleReadyz)
	mux.HandleFunc("GET /api/v1/health", r.handleAPIHealth)
	mux.HandleFunc("POST /api/v1/guests", r.handleCreateGuest)
	mux.HandleFunc("POST /api/v1/reconnect", r.handleReconnect)
	mux.HandleFunc("GET /api/v1/rooms/{code}/resolve", r.handleResolveRoom)
	mux.HandleFunc("GET /api/v1/diy", r.handleDIYState)
	mux.HandleFunc("POST /api/v1/diy/auth", r.handleDIYAuth)
	mux.HandleFunc("POST /api/v1/diy/password", r.handleDIYSetPassword)
	mux.HandleFunc("POST /api/v1/diy/tiles", r.handleDIYSaveTile)
	mux.HandleFunc("POST /api/v1/diy/tiles/{code}/delete", r.handleDIYDeleteTile)
	mux.HandleFunc("GET /api/v1/diy/images/{name}", r.handleDIYImage)
	mux.HandleFunc("GET /ws", r.handleWS)
	mux.Handle("/ws/signal", sig.NewHandler(sigReg))
	return r, r.withMiddleware(mux)
}

// Run 启动后台推进: 机器人座位与超时托管、在线人数心跳、信令房间清理。
// 每个 tick 只做一次房间单步推进, 由 AutoStep 内部串行化, 无并发写竞争。
func (r *Router) Run(ctx context.Context) {
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	sigTick := time.NewTicker(sig.DefaultSigRoomCleanupTick)
	defer sigTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, rm := range r.rooms.Tick(now) {
				// 房间成员/房主可能被回收(幽灵座位), 先同步房间状态
				// 再推送对局事件, 客户端才不会停留在旧座位表上。
				r.hub.broadcastRoom(rm)
				r.hub.broadcastGameEvents(rm)
			}
			r.hub.broadcastLobby(r.rooms.PublicRooms(), r.rooms.Count())
		case <-sigTick.C:
			cleaned := r.sig.CleanupRooms()
			if cleaned > 0 {
				r.log.Debug("sig 清理空闲 P2P 房间", "cleaned", cleaned)
			}
		}
	}
}

// TickInterval 房间自动推进与心跳间隔。
const TickInterval = 250 * time.Millisecond

// ---- 中间件 ----

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Hijack 透传底层连接劫持(WebSocket 升级必需)。
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("底层 ResponseWriter 不支持 Hijack")
	}
	return h.Hijack()
}

// Flush 透传流式刷新。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// applySecurityHeaders 写入基础安全响应头(阶段9)。
// 静态资源由 WithStatic 在中间件之外直接返回，因此两处都要调用。
func applySecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

// withMiddleware 应用 CORS 与请求日志。
func (r *Router) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		applySecurityHeaders(w.Header())
		origin := req.Header.Get("Origin")
		if origin != "" && r.cfg.IsOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, req)
		r.log.Debug("http 请求",
			"method", req.Method, "path", req.URL.Path,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

// ---- 工具 ----

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

// decodeJSON 严格解码(拒绝未知字段与坏 JSON)。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "body_too_large", "请求体过大")
		} else {
			writeErr(w, http.StatusBadRequest, "bad_json", "请求体格式错误: "+err.Error())
		}
		return false
	}
	return true
}

// ---- 健康检查 ----

func (r *Router) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *Router) handleReadyz(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	if err := r.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (r *Router) handleAPIHealth(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	dbOK := r.store.Ping(ctx) == nil
	guests := int64(-1)
	if dbOK {
		if n, err := r.store.CountGuests(ctx); err == nil {
			guests = n
		}
	}
	status := "ok"
	if !dbOK {
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          status,
		"service":         "majiang-server",
		"go":              runtime.Version(),
		"uptimeSeconds":   int64(time.Since(r.startedAt).Seconds()),
		"onlineRooms":     r.rooms.Count(),
		"maxOnlineRooms":  r.cfg.Rooms.MaxOnline,
		"guests":          guests,
		"protocolVersion": 1,
	})
}

// ---- 游客身份 REST ----

func (r *Router) handleCreateGuest(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Nickname string `json:"nickname"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	g, token, err := r.store.CreateGuest(req.Context(), body.Nickname)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_nickname", err.Error())
		return
	}
	r.log.Info("游客创建", "guestId", g.ID, "nickname", g.Nickname)
	writeJSON(w, http.StatusCreated, map[string]string{
		"guestId":  g.ID,
		"nickname": g.Nickname,
		// 重连令牌仅此一次下发, 客户端须持久保存。
		"token": token,
	})
}

func (r *Router) handleReconnect(w http.ResponseWriter, req *http.Request) {
	var body struct {
		GuestID string `json:"guestId"`
		Token   string `json:"token"`
	}
	if !decodeJSON(w, req, &body) {
		return
	}
	g, ok, err := r.store.VerifyGuest(req.Context(), body.GuestID, body.Token)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "验证失败")
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "invalid_token", "游客身份或令牌无效")
		return
	}
	_ = r.store.TouchGuest(req.Context(), g.ID)
	writeJSON(w, http.StatusOK, map[string]string{"guestId": g.ID, "nickname": g.Nickname})
}

// handleResolveRoom 按房间码判定联机方式, 供加入端自动选择通道:
//   - 在线托管房间 → transport="server"
//   - P2P/局域网信令房间 → transport="p2p" (局域网在加入端同样走 WebRTC, 同网段会自动直连)
//   - 两套注册表都查不到 → 404
//
// 只读、不泄露房间细节, 仅回传输方式。
func (r *Router) handleResolveRoom(w http.ResponseWriter, req *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(req.PathValue("code")))
	if code == "" {
		writeErr(w, http.StatusBadRequest, "bad_code", "房间码不能为空")
		return
	}
	if _, ok := r.rooms.Get(code); ok {
		writeJSON(w, http.StatusOK, map[string]any{"code": code, "transport": "server", "peers": 1})
		return
	}
	if rm := r.sig.LookupRoom(code); rm != nil {
		// peers=0: 房主信令暂时断开(P2P 模式下关页面/锁屏会这样), 房间仍在宽限期内。
		// 加入端据此提示「请让房主回到房间页面」, 而不是笼统的「房间不存在」。
		writeJSON(w, http.StatusOK, map[string]any{
			"code": code, "transport": "p2p", "peers": rm.PeerCount(),
		})
		return
	}
	writeErr(w, http.StatusNotFound, "room_not_found", "房间不存在或已结束")
}

// StorePersister 将房间元数据持久化到 SQLite(实现 room.Persister)。
type StorePersister struct{ st *store.Store }

// NewStorePersister 创建适配器。
func NewStorePersister(st *store.Store) *StorePersister { return &StorePersister{st: st} }

// CreateRoom 写入房间元数据。
func (p *StorePersister) CreateRoom(ctx context.Context, rec room.RoomRecord) error {
	return p.st.CreateRoom(ctx, store.RoomRow{
		ID: rec.ID, Code: rec.Code, HostGuestID: rec.HostGuestID, Mode: rec.Mode,
		RuleSet: rec.RuleSet, Theme: rec.Theme, Status: rec.Status,
		Name: rec.Name, IsPublic: rec.IsPublic, PasswordHash: rec.PasswordHash,
		CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	})
}

// DeleteRoom 删除房间元数据(同时清理其邀请)。
func (p *StorePersister) DeleteRoom(ctx context.Context, id string) error {
	_ = p.st.DeleteInvitesByRoom(ctx, id)
	return p.st.DeleteRoom(ctx, id)
}

// UpdateRoomActivity 刷新房间活跃时间与状态。
func (p *StorePersister) UpdateRoomActivity(ctx context.Context, id, status string) error {
	return p.st.UpdateRoomActivity(ctx, id, status)
}

// CreateInvite 落库邀请(令牌与密码只有哈希)。
func (p *StorePersister) CreateInvite(ctx context.Context, rec room.InviteRecord) error {
	return p.st.CreateInvite(ctx, store.InviteRow{
		ID: rec.ID, RoomID: rec.RoomID, TokenHash: rec.TokenHash,
		PasswordHash: rec.PasswordHash, BypassRoomPassword: rec.BypassRoomPassword,
		MaxUses: rec.MaxUses, Uses: rec.Uses, Revoked: rec.Revoked,
		ExpiresAt: rec.ExpiresAt, CreatedAt: rec.CreatedAt,
	})
}

// RevokeInvite 撤销邀请。
func (p *StorePersister) RevokeInvite(ctx context.Context, id string) error {
	return p.st.RevokeInvite(ctx, id)
}

// DeleteInvitesByRoom 清理房间的全部邀请。
func (p *StorePersister) DeleteInvitesByRoom(ctx context.Context, roomID string) error {
	return p.st.DeleteInvitesByRoom(ctx, roomID)
}
