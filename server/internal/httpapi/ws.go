package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"majiang-server/internal/game"
	"majiang-server/internal/protocol"
	"majiang-server/internal/ratelimit"
	"majiang-server/internal/room"
)

// hub 维护 guestID -> 活动连接, 用于向房间成员推送状态投影。
type hub struct {
	mu    sync.Mutex
	conns map[string]*wsConn
}

func newHub() *hub { return &hub{conns: map[string]*wsConn{}} }

func (h *hub) add(c *wsConn) {
	h.mu.Lock()
	h.conns[c.guestID] = c
	h.mu.Unlock()
}

// removeIf 仅当枢纽中注册的连接就是 c 本身时移除(快速重连时新连接顶替旧注册,
// 旧连接的 teardown 不得误删新注册)。返回是否执行了移除。
func (h *hub) removeIf(guestID string, c *wsConn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[guestID] == c {
		delete(h.conns, guestID)
		return true
	}
	return false
}

func (h *hub) get(guestID string) (*wsConn, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[guestID]
	return c, ok
}

// broadcastRoom 向房间全体在线成员推送各自视角的当前状态投影。
func (h *hub) broadcastRoom(r *room.Room) {
	players := r.Players()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range players {
		if c, ok := h.conns[p.GuestID]; ok && c.roomID == r.ID {
			c.sendEvent(r.ReserveSeq(), protocol.EvRoomState, r.ViewFor(p.GuestID), "")
		}
	}
}

// broadcastGameEvents 向房间全体在线成员下发其未见过的对局事件。
// 事件日志只含公共信息; 每条事件下发前按接收者座位实时投影填充 view,
// 他人手牌与牌墙在投影层被裁剪, 结构上不可能泄漏。
func (h *hub) broadcastGameEvents(r *room.Room) {
	players := r.Players()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range players {
		c, ok := h.conns[p.GuestID]
		if !ok || c.roomID != r.ID {
			continue
		}
		for _, e := range r.EventsSince(c.lastSentGameSeq) {
			if e.Type != protocol.EvGameEvent {
				continue
			}
			// 按接收者投影: 解包公共事件, 填充本人视角后重新封装。
			var out protocol.GameEventOut
			if json.Unmarshal(e.Payload, &out) == nil {
				if view, ok := r.GameViewFor(p.GuestID); ok {
					if raw, err := json.Marshal(view); err == nil {
						out.View = raw
					}
				}
				if data, err := json.Marshal(out); err == nil {
					e.Payload = data
				}
			}
			// 房间状态与对局事件共用房间序列空间(见 Room.ReserveSeq)。
			matchSeq := e.Seq
			e.Seq = r.ReserveSeq()
			c.sendRaw(e, "")
			c.lastSentGameSeq = matchSeq
		}
	}
}

// broadcastLobby 向所有已认证连接推送公开大厅快照。
// 只包含 Code/Name/人数/密码标记等公开元数据, 不含任何游客 ID 或手牌。
func (h *hub) broadcastLobby(entries []protocol.LobbyRoomView, online int) {
	payload := map[string]any{"rooms": entries, "onlineRooms": online}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		if c.guestID == "" {
			continue
		}
		c.sendEvent(0, protocol.EvRoomList, payload, "")
	}
}

// dismissRoom 房间解散后通知房内所有连接, 并清空它们的房间归属。
// 只发空房间状态(不含任何房间元数据), 避免解散后仍能读旧信息。
func (h *hub) dismissRoom(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		if c.roomID != roomID {
			continue
		}
		c.roomID = ""
		c.sendEvent(0, protocol.EvRoomState, map[string]any{"roomId": "", "status": "dissolved"}, "")
	}
}

// wsConn 一条 WebSocket 会话。会话状态(roomID)仅由读循环 goroutine 修改。
type wsConn struct {
	srv      *Router
	conn     *websocket.Conn
	guestID  string
	nickname string
	roomID   string
	bucket   *ratelimit.Bucket
	send     chan []byte
	done     chan struct{} // teardown 关闭, 通知写循环收尾
	wdone    chan struct{} // 写循环退出时关闭, teardown 等待它确认冲刷完成
	// lastSentGameSeq: 该连接已下发的对局事件水位(广播去重/断线补发用)。
	lastSentGameSeq int64
	killOnce        sync.Once // 底层连接立即关闭(幂等)
	doneOnce        sync.Once // done 通道仅关闭一次(幂等)
}

const (
	wsHelloTimeout          = 15 * time.Second // 握手超时
	wsIdleTimeout           = 90 * time.Second // 空闲读超时
	wsWriteTimeout          = 10 * time.Second
	wsSendBuffer            = 64
	wsFlushTimeout          = 3 * time.Second  // teardown 等待写循环冲刷的上限
	wsCloseHandshakeTimeout = 3 * time.Second // 关闭握手等待客户端回显关闭帧的上限
)

// handleWS 升级连接并进入会话循环。
func (r *Router) handleWS(w http.ResponseWriter, req *http.Request) {
	origin := req.Header.Get("Origin")
	if origin != "" && !r.cfg.IsOriginAllowed(origin) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		r.log.Warn("ws 升级失败", "error", err.Error())
		return
	}
	c := &wsConn{
		srv:    r,
		conn:   conn,
		bucket: ratelimit.NewBucket(r.cfg.Limits.MessageBurst, r.cfg.Limits.MessagesPerSec),
		send:   make(chan []byte, wsSendBuffer),
		done:   make(chan struct{}),
		wdone:  make(chan struct{}),
	}
	go c.writeLoop()
	c.readLoop()
}

// readLoop 读循环: 握手认证 -> 消息分发, 退出时清理会话。
func (c *wsConn) readLoop() {
	defer c.teardown()
	c.conn.SetReadLimit(int64(c.srv.cfg.Limits.MaxMessageBytes))

	// ---- 握手: 第一条消息必须是 guest.hello ----
	c.conn.SetReadDeadline(time.Now().Add(wsHelloTimeout))
	if !c.handleHello() {
		return
	}
	c.srv.hub.add(c)
	// 断线重连恢复: 游客可能已在房间内, 从注册表找回会话并标记在线。
	if rm, ok := c.srv.rooms.FindByGuest(c.guestID); ok {
		c.roomID = rm.ID
		rm.SetConnected(c.guestID, true)
		c.srv.hub.broadcastRoom(rm)
	}

	for {
		c.conn.SetReadDeadline(time.Now().Add(wsIdleTimeout))
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return // 超时/关闭/超大消息等, 统一按断线处理
		}
		if !c.bucket.Allow(time.Now()) {
			// 超频: 发送错误事件后优雅关闭(客户端可收到错误再断开)。
			c.sendEvent(0, protocol.EvError, protocol.ErrorOut{Code: "rate_limited", Message: "消息频率超限"}, "")
			c.beginShutdown()
			return
		}
		env, err := protocol.Decode(data, c.srv.cfg.Limits.MaxMessageBytes)
		if err != nil {
			c.sendError(env.Ref, "bad_message", err.Error())
			continue
		}
		c.dispatch(env)
	}
}

// handleHello 处理握手消息。
func (c *wsConn) handleHello() bool {
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return false
	}
	env, err := protocol.Decode(data, c.srv.cfg.Limits.MaxMessageBytes)
	if err != nil {
		c.sendEvent(0, protocol.EvError, protocol.ErrorOut{Code: "bad_message", Message: err.Error()}, "")
		return false
	}
	if env.Type != protocol.OpHello {
		c.sendError(env.Ref, "expected_hello", "首条消息必须是 guest.hello")
		return false
	}
	var in protocol.HelloIn
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		c.sendError(env.Ref, "bad_payload", "hello 负载格式错误")
		return false
	}
	g, ok, err := c.srv.store.VerifyGuest(context.Background(), in.GuestID, in.Token)
	if err != nil || !ok {
		c.sendError(env.Ref, "auth_failed", "游客身份或令牌无效")
		return false
	}
	c.guestID = g.ID
	c.nickname = g.Nickname
	_ = c.srv.store.TouchGuest(context.Background(), g.ID)
	c.sendEvent(0, protocol.EvHello, protocol.HelloOut{GuestID: g.ID, Nickname: g.Nickname}, env.Ref)
	return true
}

// dispatch 分发已解码的上行消息。
func (c *wsConn) dispatch(env protocol.Envelope) {
	switch env.Type {
	case protocol.OpPing:
		c.sendEvent(0, protocol.EvPong, map[string]int64{"ts": time.Now().UnixMilli()}, env.Ref)

	case protocol.OpRoomCreate:
		c.opRoomCreate(env)
	case protocol.OpRoomJoin:
		c.opRoomJoin(env)
	case protocol.OpRoomList:
		c.opRoomList(env)
	case protocol.OpRoomUpdate:
		c.opRoomUpdate(env)
	case protocol.OpRoomDissolve:
		c.opRoomDissolve(env)
	case protocol.OpSeatReady:
		c.opSeatReady(env)
	case protocol.OpSeatBot:
		c.opSeatBot(env)
	case protocol.OpInviteCreate:
		c.opInviteCreate(env)
	case protocol.OpInviteList:
		c.opInviteList(env)
	case protocol.OpInviteRevoke:
		c.opInviteRevoke(env)
	case protocol.OpInviteJoin:
		c.opInviteJoin(env)
	case protocol.OpRoomLeave:
		c.opRoomLeave(env)
	case protocol.OpResync:
		c.opResync(env)
	case protocol.OpGameStart:
		c.opGameStart(env)
	case protocol.OpGameAction:
		c.opGameAction(env)

	default:
		c.sendError(env.Ref, "unknown_type", "未知消息类型: "+env.Type)
	}
}

// opRoomCreate 创建房间。
func (c *wsConn) opRoomCreate(env protocol.Envelope) {
	if c.roomID != "" {
		c.sendError(env.Ref, "already_in_room", "请先离开当前房间")
		return
	}
	var in struct {
		Name     string          `json:"name"`
		Password string          `json:"password"`
		Public   *bool           `json:"public"`
		RuleSet  json.RawMessage `json:"ruleSet"`
		Theme    string          `json:"theme"`
		// 创建房间设置: 人数 / 玩法预设 / 自定义牌集(牌种→张数)
		Players  int            `json:"players"`
		PresetID string         `json:"presetId"`
		Pool     map[string]int `json:"pool"`
	}
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			c.sendError(env.Ref, "bad_payload", "room.create 负载格式错误")
			return
		}
	}
	rules := game.DefaultRuleSet()
	if len(in.RuleSet) > 0 && string(in.RuleSet) != "null" {
		// 逐字段合并而不是整体替换: 客户端只发送它认识的字段, 其余保留默认值,
		// 避免旧客户端把阶段10 新增的计时/截牌规则覆盖成零值(等于全部关闭)。
		var patch ruleSetPatch
		if err := json.Unmarshal(in.RuleSet, &patch); err != nil {
			c.sendError(env.Ref, "bad_payload", "ruleSet 负载格式错误")
			return
		}
		rules = patch.apply(rules)
	}
	// 2~4 人: 座数决定房间容量; 人数与牌集均在引擎层校验。
	if in.Players >= 2 && in.Players <= 4 {
		rules.Players = in.Players
	}
	if len(in.Pool) > 0 {
		rules.Pool = in.Pool
	}
	if err := rules.Validate(); err != nil {
		c.sendError(env.Ref, "bad_rule_set", err.Error())
		return
	}
	theme := game.ThemeClassic
	if in.Theme != "" {
		theme = game.Theme(in.Theme)
	}
	public := true
	if in.Public != nil {
		public = *in.Public
	}
	rm, err := c.srv.rooms.Create(context.Background(), room.CreateOptions{
		GuestID:  c.guestID,
		Nickname: c.nickname,
		Name:     in.Name,
		Password: in.Password,
		Public:   public,
		RuleSet:  rules,
		Theme:    theme,
	})
	if err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.roomID = rm.ID
	c.srv.log.Info("房间创建", "roomId", rm.ID, "code", rm.Code, "host", c.guestID, "public", public)
	// 单独响应创建者。
	c.sendEvent(rm.ReserveSeq(), protocol.EvRoomState, rm.ViewFor(c.guestID), env.Ref)
	c.srv.hub.broadcastRoom(rm)
	c.srv.hub.broadcastLobby(c.srv.rooms.PublicRooms(), c.srv.rooms.Count())
}

// opRoomJoin 按邀请码或房间 ID 加入(可带房间密码)。
func (c *wsConn) opRoomJoin(env protocol.Envelope) {
	if c.roomID != "" {
		c.sendError(env.Ref, "already_in_room", "请先离开当前房间")
		return
	}
	var in struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			c.sendError(env.Ref, "bad_payload", "room.join 负载格式错误")
			return
		}
	}
	rm, err := c.srv.rooms.Join(context.Background(), room.JoinOptions{
		GuestID:  c.guestID,
		Nickname: c.nickname,
		IDOrCode: in.Code,
		Password: in.Password,
	})
	if err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.roomID = rm.ID
	c.srv.log.Info("玩家加入房间", "roomId", rm.ID, "guestId", c.guestID)
	c.sendEvent(rm.ReserveSeq(), protocol.EvRoomState, rm.ViewFor(c.guestID), env.Ref)
	c.srv.hub.broadcastRoom(rm)
	c.srv.hub.broadcastLobby(c.srv.rooms.PublicRooms(), c.srv.rooms.Count())
}

// opInviteJoin 用邀请令牌加入房间。
func (c *wsConn) opInviteJoin(env protocol.Envelope) {
	if c.roomID != "" {
		c.sendError(env.Ref, "already_in_room", "请先离开当前房间")
		return
	}
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		c.sendError(env.Ref, "bad_payload", "invite.join 负载格式错误")
		return
	}
	rm, err := c.srv.rooms.Join(context.Background(), room.JoinOptions{
		GuestID:     c.guestID,
		Nickname:    c.nickname,
		IDOrCode:    in.Code,
		Password:    in.Password,
		InviteToken: in.Token,
	})
	if err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.roomID = rm.ID
	c.srv.log.Info("玩家凭邀请加入", "roomId", rm.ID, "guestId", c.guestID)
	c.sendEvent(rm.ReserveSeq(), protocol.EvRoomState, rm.ViewFor(c.guestID), env.Ref)
	c.srv.hub.broadcastRoom(rm)
	c.srv.hub.broadcastLobby(c.srv.rooms.PublicRooms(), c.srv.rooms.Count())
}

// roomForOp 取出当前连接所在房间, 失败时已写错误。
func (c *wsConn) roomForOp(env protocol.Envelope) (*room.Room, bool) {
	if c.roomID == "" {
		c.sendError(env.Ref, "not_in_room", "当前不在房间内")
		return nil, false
	}
	rm, ok := c.srv.rooms.Get(c.roomID)
	if !ok {
		c.roomID = ""
		c.sendError(env.Ref, "room_gone", "房间已不存在")
		return nil, false
	}
	return rm, true
}

// opRoomList 公开大厅快照。
func (c *wsConn) opRoomList(env protocol.Envelope) {
	entries := c.srv.rooms.PublicRooms()
	c.sendEvent(0, protocol.EvRoomList, map[string]any{
		"rooms": entries, "onlineRooms": c.srv.rooms.Count(),
	}, env.Ref)
}

// opRoomUpdate 房主修改房间名/公开性/密码(仅大厅阶段)。
func (c *wsConn) opRoomUpdate(env protocol.Envelope) {
	rm, ok := c.roomForOp(env)
	if !ok {
		return
	}
	var in struct {
		Name     *string `json:"name"`
		Password *string `json:"password"`
		Public   *bool   `json:"public"`
	}
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			c.sendError(env.Ref, "bad_payload", "room.update 负载格式错误")
			return
		}
	}
	if err := rm.SetConfig(c.guestID, in.Name, in.Password, in.Public); err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.srv.hub.broadcastRoom(rm)
	c.srv.hub.broadcastLobby(c.srv.rooms.PublicRooms(), c.srv.rooms.Count())
}

// opRoomDissolve 房主解散房间。
func (c *wsConn) opRoomDissolve(env protocol.Envelope) {
	rm, ok := c.roomForOp(env)
	if !ok {
		return
	}
	if err := c.srv.rooms.Dissolve(context.Background(), c.guestID, rm.ID); err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.roomID = ""
	c.srv.log.Info("房间解散", "roomId", rm.ID)
	// 解散是双端事实: 房主与其他成员都要收到"房间已不存在"的状态。
	c.srv.hub.dismissRoom(rm.ID)
	c.srv.hub.broadcastLobby(c.srv.rooms.PublicRooms(), c.srv.rooms.Count())
}

// opSeatReady 本人准备/取消准备。
func (c *wsConn) opSeatReady(env protocol.Envelope) {
	rm, ok := c.roomForOp(env)
	if !ok {
		return
	}
	var in struct {
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		c.sendError(env.Ref, "bad_payload", "seat.ready 负载格式错误")
		return
	}
	if err := rm.SetReady(c.guestID, in.Ready); err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.srv.hub.broadcastRoom(rm)
}

// opSeatBot 房主增删机器人座位。
func (c *wsConn) opSeatBot(env protocol.Envelope) {
	rm, ok := c.roomForOp(env)
	if !ok {
		return
	}
	var in struct {
		Seat int  `json:"seat"`
		Add  bool `json:"add"`
	}
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		c.sendError(env.Ref, "bad_payload", "seat.bot 负载格式错误")
		return
	}
	if err := rm.SetBot(c.guestID, in.Seat, in.Add); err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.srv.hub.broadcastRoom(rm)
}

// opInviteCreate 房主创建邀请(令牌明文仅此一次下发)。
func (c *wsConn) opInviteCreate(env protocol.Envelope) {
	rm, ok := c.roomForOp(env)
	if !ok {
		return
	}
	var in struct {
		ExpiresInSec       int64  `json:"expiresInSec"`
		MaxUses            int    `json:"maxUses"`
		Password           string `json:"password"`
		BypassRoomPassword bool   `json:"bypassRoomPassword"`
	}
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			c.sendError(env.Ref, "bad_payload", "invite.create 负载格式错误")
			return
		}
	}
	token, view, err := c.srv.rooms.CreateInvite(context.Background(), c.guestID, rm.ID, room.CreateInviteOptions{
		ExpiresInSec:       in.ExpiresInSec,
		MaxUses:            in.MaxUses,
		Password:           in.Password,
		BypassRoomPassword: in.BypassRoomPassword,
	})
	if err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.sendEvent(0, protocol.EvInviteCreated, protocol.InviteCreatedView{Token: token, Invite: view}, env.Ref)
}

// opInviteList 房主查看邀请列表(无令牌明文)。
func (c *wsConn) opInviteList(env protocol.Envelope) {
	rm, ok := c.roomForOp(env)
	if !ok {
		return
	}
	list, err := rm.ListInvites(c.guestID)
	if err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.sendEvent(0, protocol.EvInviteList, map[string]any{"invites": list}, env.Ref)
}

// opInviteRevoke 房主撤销邀请。
func (c *wsConn) opInviteRevoke(env protocol.Envelope) {
	rm, ok := c.roomForOp(env)
	if !ok {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		c.sendError(env.Ref, "bad_payload", "invite.revoke 负载格式错误")
		return
	}
	if err := rm.RevokeInvite(c.guestID, in.ID); err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.sendEvent(0, protocol.EvPong, map[string]string{"revoked": in.ID}, env.Ref)
}

// opGameStart 房主开局。
func (c *wsConn) opGameStart(env protocol.Envelope) {
	if c.roomID == "" {
		c.sendError(env.Ref, "not_in_room", "当前不在房间内")
		return
	}
	rm, ok := c.srv.rooms.Get(c.roomID)
	if !ok {
		c.roomID = ""
		c.sendError(env.Ref, "room_gone", "房间已不存在")
		return
	}
	// 只有房主能开局(包括一局结束后的“再来一局”)。
	if !rm.IsHost(c.guestID) {
		c.sendError(env.Ref, "not_host", "只有房主可以开始对局")
		return
	}
	// 开局随机源: crypto 种子(可重放记录由后续阶段补充)。
	if err := rm.StartMatch(game.NewCryptoSource()); err != nil {
		// 开局失败可能是"清掉中途离开的座位后人不够了", 先把最新名单推给客户端,
		// 否则房主会看到一个按钮点了没反应的界面(仍然是 4/4, 实际只剩 3 人)。
		c.srv.hub.broadcastRoom(rm)
		c.sendError(env.Ref, gameStartErrCode(err), err.Error())
		return
	}
	c.srv.log.Info("对局开始", "roomId", rm.ID, "players", rm.PlayerCount())
	c.srv.hub.broadcastGameEvents(rm)
	c.srv.hub.broadcastRoom(rm)
}

// opGameAction 对局动作: 服务端权威验证后广播各座位私有视图。
func (c *wsConn) opGameAction(env protocol.Envelope) {
	if c.roomID == "" {
		c.sendError(env.Ref, "not_in_room", "当前不在房间内")
		return
	}
	rm, ok := c.srv.rooms.Get(c.roomID)
	if !ok {
		c.roomID = ""
		c.sendError(env.Ref, "room_gone", "房间已不存在")
		return
	}
	var in struct {
		Action    string          `json:"action"`
		Payload   json.RawMessage `json:"payload"`
		ActionSeq int64           `json:"actionSeq"`
	}
	if err := json.Unmarshal(env.Payload, &in); err != nil {
		c.sendError(env.Ref, "bad_payload", "game.action 负载格式错误")
		return
	}
	if in.Action == "" {
		c.sendError(env.Ref, "bad_payload", "action 不能为空")
		return
	}
	applied, err := rm.ApplyActionWithSeq(c.guestID, in.Action, in.Payload, in.ActionSeq)
	if err != nil {
		c.srv.log.Debug("对局动作被拒",
			"roomId", rm.ID, "guest", c.guestID, "action", in.Action, "error", err.Error())
		c.sendError(env.Ref, gameActionErrCode(err), err.Error())
		return
	}
	if !applied {
		// 重放: 不重复执行, 仅把当前视图回推给本连接供客户端校准。
		c.srv.log.Debug("忽略重复动作", "roomId", rm.ID, "guest", c.guestID, "actionSeq", in.ActionSeq)
		if view, ok := rm.GameViewFor(c.guestID); ok {
			c.sendEvent(0, protocol.EvGameEvent, protocol.GameEventOut{Kind: "resync", View: mustJSON(view)}, env.Ref)
		}
		return
	}
	// 机器人座位立即接管(不等定时器, 降低人类等待感; 循环由 AutoStep 内部有界)。
	rm.AutoStep(time.Now())
	c.srv.hub.broadcastGameEvents(rm)
	// 终局后同时刷新房间状态投影。
	if rm.MatchFinished() {
		c.srv.hub.broadcastRoom(rm)
	}
}

// mustJSON 序列化视图(失败时返回 nil, 调用方按"无视图"处理)。
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

// gameStartErrCode / gameActionErrCode 将房间/引擎错误映射为稳定错误码。
func gameStartErrCode(err error) string {
	switch {
	case err == room.ErrMatchInProgress:
		return "match_in_progress"
	case err == room.ErrNeedPlayers:
		return "need_players"
	case err == room.ErrNotAllReady:
		return "not_all_ready"
	case err == room.ErrNotEnoughSeats:
		return "not_enough_seats"
	case err == room.ErrUnsupportedPlayers:
		return "unsupported_players"
	default:
		return "game_start_failed"
	}
}

func gameActionErrCode(err error) string {
	switch {
	case errors.Is(err, game.ErrMatchOver):
		return "match_over"
	case errors.Is(err, game.ErrWrongPhase):
		return "wrong_phase"
	case errors.Is(err, game.ErrNotYourTurn):
		return "not_your_turn"
	case errors.Is(err, game.ErrNotResponder):
		return "not_responder"
	case errors.Is(err, game.ErrAlreadyResponded):
		return "already_responded"
	case errors.Is(err, game.ErrTileNotInHand):
		return "tile_not_in_hand"
	case errors.Is(err, game.ErrIllegalMeld), errors.Is(err, game.ErrIllegalChow):
		return "illegal_meld"
	case errors.Is(err, game.ErrWinNotAllowed):
		return "win_not_allowed"
	case errors.Is(err, game.ErrIllegalAction):
		return "illegal_action"
	case err == room.ErrNoMatch:
		return "no_match"
	case err == room.ErrNotInRoom:
		return "not_in_room"
	default:
		return "game_action_failed"
	}
}

// opRoomLeave 离开房间。
func (c *wsConn) opRoomLeave(env protocol.Envelope) {
	if c.roomID == "" {
		c.sendError(env.Ref, "not_in_room", "当前不在房间内")
		return
	}
	roomID := c.roomID
	if err := c.srv.rooms.Leave(context.Background(), c.guestID, roomID); err != nil {
		c.sendError(env.Ref, roomErrCode(err), err.Error())
		return
	}
	c.roomID = ""
	c.sendEvent(0, protocol.EvRoomState, nil, env.Ref)
	// 房间仍在(还有成员)时向剩余成员广播; 空房已被注册表回收, 无需广播。
	if rm, ok := c.srv.rooms.Get(roomID); ok {
		c.srv.hub.broadcastRoom(rm)
	}
}

// opResync 断线重连: 增量补发事件 + 当前状态投影。
func (c *wsConn) opResync(env protocol.Envelope) {
	// 不在房间时属于正常状态(刚离房/刚连上还没建房), 不是错误:
	// 回一份空房间态让客户端清干净即可, 否则会自动重连的 resync 把
	// 「当前不在房间内」当报错弹在创建房间页上。
	if c.roomID == "" {
		c.sendEvent(0, protocol.EvRoomState, nil, env.Ref)
		return
	}
	rm, ok := c.srv.rooms.Get(c.roomID)
	if !ok {
		c.roomID = "" // 房间已被清理
		c.sendError(env.Ref, "room_gone", "房间已不存在")
		return
	}
	var in protocol.ResyncIn
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &in); err != nil {
			c.sendError(env.Ref, "bad_payload", "sync.resync 负载格式错误")
			return
		}
	}
	// 回放的历史事件保持原样(不带请求 ref): 它们不是本次请求的应答。
	// 对局事件不在日志回放中出现(日志只存公共信息, 缺接收者投影),
	// 当前私有视图随后由 GameViewFor 单独补发。
	// 回放同样重新领取序列号, 否则重连后的 seq 会回退到旧日志值。
	for _, e := range rm.EventsSince(in.LastSeq) {
		if e.Type == protocol.EvGameEvent {
			continue
		}
		e.Seq = rm.ReserveSeq()
		c.sendRaw(e, "")
	}
	// 最终状态投影携带请求 ref, 客户端据此识别同步完成。
	c.sendEvent(rm.ReserveSeq(), protocol.EvRoomState, rm.ViewFor(c.guestID), env.Ref)
	// 对局进行中: 立即补发一次当前私有视图(否则重连后要等下一次广播才有牌桌状态)。
	// 这同时把本连接的事件水位抬到当前序列, 避免后续广播重复回放整局。
	if view, ok := rm.GameViewFor(c.guestID); ok {
		c.sendEvent(0, protocol.EvGameEvent, protocol.GameEventOut{
			Kind: "resync",
			View: mustJSON(view),
		}, env.Ref)
	// 水位必须取"对局事件空间"的最后序列, 与 EventsSince 的入参同域。
	c.lastSentGameSeq = rm.GameSeq()
	}
}

// teardown 会话清理: 注销枢纽、标记断线并通知房间成员, 最后优雅关闭连接。
// 优雅关闭 = 等待写循环冲刷缓冲并发出 WS 关闭帧, 保证客户端收到全部已排队事件。
func (c *wsConn) teardown() {
	// 仅当枢纽中注册的仍是本连接时才注销并标记断线;
	// 若已被新连接顶替(快速重连), 不得清除新连接的注册或误标离线。
	if removed := c.srv.hub.removeIf(c.guestID, c); removed && c.roomID != "" {
		if rm, ok := c.srv.rooms.Get(c.roomID); ok {
			rm.SetConnected(c.guestID, false)
			c.srv.hub.broadcastRoom(rm)
		}
	}
	// 通知写循环收尾并等待其完成冲刷(有超时保护)。
	c.beginShutdown()
	select {
	case <-c.wdone:
	case <-time.After(wsFlushTimeout):
	}
	c.kill()
}

// beginShutdown 请求写循环优雅收尾(幂等)。
func (c *wsConn) beginShutdown() {
	c.doneOnce.Do(func() { close(c.done) })
}

// kill 立即关闭底层连接(幂等)。
func (c *wsConn) kill() {
	c.killOnce.Do(func() { c.conn.Close() })
}

// writeLoop 发送循环: 持续写出排队消息; 收到收尾信号后冲刷缓冲、
// 发送标准关闭帧再退出, 避免客户端因粗暴断连(1006)丢失已排队事件。
func (c *wsConn) writeLoop() {
	defer close(c.wdone)
	defer c.kill()
	for {
		select {
		case data := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				// 写失败: 连接已不可用, 等待收尾信号后退出, 避免与 teardown 竞态。
				<-c.done
				return
			}
		case <-c.done:
			// 冲刷缓冲中的剩余消息。
			for {
				select {
				case data := <-c.send:
					_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
					if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
						return
					}
				default:
					// 发送标准关闭帧。
					_ = c.conn.WriteControl(
						websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
						time.Now().Add(wsWriteTimeout),
					)
					// 完成关闭握手: 等待客户端回显关闭帧(有界), 避免客户端
					// 因 TCP 先于关闭帧被拆而观察到异常断连(1006)。
					// 此时读循环已退出, 此处是唯一读者, 无并发读冲突。
					c.conn.SetReadDeadline(time.Now().Add(wsCloseHandshakeTimeout))
					for {
						if _, _, err := c.conn.ReadMessage(); err != nil {
							return // 收到关闭帧(或超时/错误), 结束。
						}
					}
				}
			}
		}
	}
}

// sendEvent 构造并发送事件。
func (c *wsConn) sendEvent(seq int64, typ string, payload any, ref string) {
	env, err := protocol.NewEvent(seq, typ, payload)
	if err != nil {
		return
	}
	env.Ref = ref
	c.sendRaw(env, "")
}

// sendRaw 发送已构造的信封(ref 覆盖可选)。
func (c *wsConn) sendRaw(env protocol.Envelope, ref string) {
	if ref != "" {
		env.Ref = ref
	}
	data, err := protocol.Encode(env)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	case <-c.done:
		// 会话正在收尾, 丢弃迟到事件。
	default:
		// 发送缓冲已满视为慢客户端, 触发优雅收尾。
		c.beginShutdown()
	}
}

// sendError 发送错误事件(回显请求 ref)。
func (c *wsConn) sendError(ref, code, msg string) {
	c.sendEvent(0, protocol.EvError, protocol.ErrorOut{Code: code, Message: msg}, ref)
}

// roomErrCode 将房间错误映射为稳定错误码。
func roomErrCode(err error) string {
	switch {
	case err == room.ErrRoomFull:
		return "room_full"
	case err == room.ErrRoomStarted:
		return "room_started"
	case err == room.ErrAlreadyInRoom:
		return "already_in_room"
	case err == room.ErrNotInRoom:
		return "not_in_room"
	case err == room.ErrRoomNotFound:
		return "room_not_found"
	case err == room.ErrMaxOnlineRooms:
		return "max_online_rooms"
	case err == room.ErrBadPassword:
		return "bad_room_password"
	case err == room.ErrNotHost:
		return "not_host"
	case err == room.ErrRulesLocked:
		return "rules_locked"
	case err == room.ErrUnsupportedPlayers:
		return "unsupported_players"
	case err == room.ErrNotEnoughSeats:
		return "not_enough_seats"
	case err == room.ErrSeatOccupied:
		return "seat_occupied"
	case err == room.ErrSeatEmpty:
		return "seat_empty"
	case err == room.ErrBadRoomName:
		return "bad_room_name"
	case err == room.ErrInviteNotFound:
		return "invite_not_found"
	case err == room.ErrInviteExpired:
		return "invite_expired"
	case err == room.ErrInviteRevoked:
		return "invite_revoked"
	case err == room.ErrInviteUsedUp:
		return "invite_used_up"
	case err == room.ErrInvitePassword:
		return "bad_invite_password"
	default:
		return "room_error"
	}
}
