// Package room 服务端权威房间: 在线房间注册表、房间状态(大厅阶段)、
// 事件日志(单调序列号)与按座位的可见状态投影。
// 本阶段实现大厅级真实行为(创建/加入/离开/事件/清扫); 对局状态机在阶段2+扩展。
package room

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"majiang-server/internal/game"
	"majiang-server/internal/protocol"
)

// MaxPlayers 房间人数上限(规则引擎将在 2~4 内按规则配置校验)。
const MaxPlayers = 4

// Mode 联机方式。
type Mode string

// 三种联机方式: 在线(服务器权威)、WebRTC P2P、局域网。
// P2P/LAN 房间不经服务端注册表(见架构文档适配层设计), 服务端仅在线模式持有权威房间。
const (
	ModeOnline Mode = "online"
	ModeP2P    Mode = "p2p"
	ModeLAN    Mode = "lan"
)

// Status 房间状态。
type Status string

// 房间状态: 大厅、对局中、已结束。
const (
	StatusLobby    Status = "lobby"
	StatusPlaying  Status = "playing"
	StatusFinished Status = "finished"
)

// Player 房间内玩家(服务端权威记录)。
type Player struct {
	GuestID   string
	Nickname  string
	Seat      int
	Connected bool
	Ready     bool // 大厅准备状态
	IsBot     bool // 服务端 AI 座位
	// Left 玩家在对局中途主动离开房间。座位暂时保留(托管给机器人打,
	// 否则牌局会在该座位卡死), 一局结束或下次入座时再让位。
	Left bool
	// disconnectedAt 断线时刻(仅大厅/已结束房间用于回收幽灵座位)。
	disconnectedAt time.Time
	// 手牌等私有状态(阶段2+) 以 TileID 记录, 永不进入他人投影。
}

// Room 服务端权威房间。所有变更持锁进行; 事件日志提供重连增量同步。
type Room struct {
	ID          string
	Code        string
	Name        string
	IsPublic    bool
	PasswordHash string // 空串表示无房间密码
	HostGuestID string
	Mode        Mode
	Status      Status
	Theme       game.Theme
	RuleSet     game.RuleSet
	RulesLocked bool
	CreatedAt   time.Time

	mu           sync.Mutex
	lastActivity time.Time
	players      map[string]*Player // guestID -> player
	seatTaken    [MaxPlayers]bool
	seq          int64
	events       []protocol.Envelope
	ready        map[string]bool    // guestID -> 准备状态
	invites      map[string]*Invite // inviteID -> 邀请
	actionSeq    map[string]int64   // guestID -> 已应用的最大动作序号(幂等重放)
	turnDeadline time.Time          // 当前行动截止时间(超时托管)

	// 对局(阶段2): 非空表示权威对局进行中/已结束。
	match       *game.Match

	// 阶段7 房主迁移: 主机任期(每次房主变更 +1)、迁移日志、宽限期覆盖(测试用)。
	hostEpoch  int
	migrations []MigrationEntry
	hostGrace  time.Duration
	seatGrace  time.Duration
}

// Touch 更新活跃时间。
func (r *Room) Touch() {
	r.mu.Lock()
	r.lastActivity = time.Now()
	r.mu.Unlock()
}

// LastActivity 返回最近活跃时间(用于空房清扫)。
func (r *Room) LastActivity() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastActivity
}

// Players 返回按座位排序的玩家快照。
func (r *Room) Players() []Player {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Player, 0, len(r.players))
	for _, p := range r.players {
		out = append(out, *p)
	}
	// 座位排序。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Seat < out[j-1].Seat; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// PlayerCount 玩家数。
func (r *Room) PlayerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.players)
}

// HasPlayer 报告游客是否在房内(已主动离开的托管座位不算)。
func (r *Room) HasPlayer(guestID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[guestID]
	return ok && !p.Left
}

// purgeLeftLocked 清掉"已主动离开"的托管座位(调用方持锁)。
// 对局中不清: 座位是引擎的固定结构, 提前删会让手牌与轮转错位。
// 一局结束/下次入座时再彻底移除, 把位置让给新玩家。
func (r *Room) purgeLeftLocked() bool {
	if r.match != nil && r.match.Phase() != game.PhaseFinished {
		return false
	}
	changed := false
	for _, p := range r.players {
		if !p.Left {
			continue
		}
		delete(r.players, p.GuestID)
		delete(r.ready, p.GuestID)
		delete(r.actionSeq, p.GuestID)
		r.seatTaken[p.Seat] = false
		changed = true
	}
	return changed
}

// PurgeLeft 在对局之外清掉"已主动离开"的座位(注册表周期调用)。
//
// 一局结束后必须尽快让位: 否则走了的人还占着座位, 剩下的人既开不了下一局
// (人数不够), 新玩家也进不来 —— 正是"返回的人一直占着房间位置"的根因。
func (r *Room) PurgeLeft() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.purgeLeftLocked() {
		return false
	}
	if !r.hasHumanLocked() {
		r.teardownLocked()
		return true
	}
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, nil)
	return true
}

// join 加入房间(内部方法, 调用方持注册表语义)。返回分配的座位。
// password 为房间密码明文; bypassPassword 为邀请提供的免密能力。
func (r *Room) join(guestID, nickname, password string, bypassPassword bool) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 大厅与「上一局已结束、等待下一局」两种状态都允许入座:
	// 一局打完后玩家可以继续留在房间, 新玩家/补位也能进来等下一局。
	if r.Status != StatusLobby && r.Status != StatusFinished {
		return 0, ErrRoomStarted
	}
	// 上一局里中途离开的座位在此时让位(对局已结束, 删除安全)。
	r.purgeLeftLocked()
	if _, exists := r.players[guestID]; exists {
		return 0, ErrAlreadyInRoom
	}
	if len(r.players) >= r.RuleSet.Players {
		return 0, ErrRoomFull
	}
	if !bypassPassword {
		if err := r.verifyPasswordLocked(password); err != nil {
			return 0, err
		}
	}
	seat := -1
	for i, taken := range r.seatTaken {
		if !taken {
			seat = i
			break
		}
	}
	if seat < 0 {
		return 0, ErrRoomFull
	}
	r.seatTaken[seat] = true
	r.players[guestID] = &Player{GuestID: guestID, Nickname: nickname, Seat: seat, Connected: true}
	if r.ready == nil {
		r.ready = map[string]bool{}
	}
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, r.viewLocked(guestID))
	return seat, nil
}

// StartMatch 房主开局: 按当前入座顺序发牌。src 为可注入随机源(生产传 crypto 种子)。
// 座位必须坐满(可用机器人补位); 支持 2/3/4 人规则; 开局后规则锁定。
func (r *Room) StartMatch(src game.RandomSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.match != nil && r.match.Phase() != game.PhaseFinished {
		return ErrMatchInProgress
	}
	// 开新一局前把上一局中途离开的座位彻底清掉, 免得"幽灵座位"永远占位。
	r.purgeLeftLocked()
	if len(r.players) < r.RuleSet.Players {
		return ErrNotEnoughSeats
	}
	// 简化玩法: 座位坐满即可由房主开局, 不再要求逐个「准备」
	// (准备状态字段仍保留在协议里, 供后续需要时启用)。
	m, err := game.NewMatch(r.RuleSet, 0, src)
	if err != nil {
		return err
	}
	r.match = m
	r.Status = StatusPlaying
	r.RulesLocked = true
	r.lastActivity = time.Now()
	// 计时由引擎按规则(响应/出牌倒计时)自行持有; 这里只镜像当前出牌截止。
	r.turnDeadline = m.DiscardDeadline()
	// 新一局重新开始: 清空上一局的幂等序号水位, 否则重开一局时
	// 客户端从 1 重新计数的动作会被误判成重放而丢弃。
	r.actionSeq = map[string]int64{}
	// 公共事件(不含任何私有视图; 视图由传输层按接收者实时投影)。
	r.appendGameEventLocked("match.started", -1, nil)
	return nil
}

// DefaultTurnTimeout 兜底托管上限: 仅在引擎未设出牌倒计时、且玩家已掉线时
// 用于判断"该替他走一步"的宽松阈值(见 autoplay.go 的掉线兜底)。
const DefaultTurnTimeout = 30 * time.Second

// GameViewFor 返回指定游客的对局视角(不在房内或未开局返回 ok=false)。
func (r *Room) GameViewFor(guestID string) (game.SeatView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.match == nil {
		return game.SeatView{}, false
	}
	p, ok := r.players[guestID]
	if !ok {
		return game.SeatView{}, false
	}
	return r.match.ViewFor(game.Seat(p.Seat)), true
}

// ApplyAction 对局动作入口(服务端权威验证)。动作代码与线缆一致:
// draw / discard / chow / pong / kong.exposed / kong.concealed / kong.added /
// hu.self / hu.robbing / pass。
func (r *Room) ApplyAction(guestID string, action string, payload json.RawMessage) error {
	_, err := r.ApplyActionWithSeq(guestID, action, payload, 0)
	return err
}

// ApplyActionWithSeq 幂等动作入口: actionSeq > 0 时, 不高于已应用序号的请求
// 被视为网络重放, 不再执行(applied=false), 由传输层回推当前视图供客户端校准。
func (r *Room) ApplyActionWithSeq(guestID, action string, payload json.RawMessage, actionSeq int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.match == nil {
		return false, ErrNoMatch
	}
	p, ok := r.players[guestID]
	if !ok {
		return false, ErrNotInRoom
	}
	seat := game.Seat(p.Seat)
	if actionSeq > 0 {
		if last := r.actionSeq[guestID]; actionSeq <= last {
			return false, nil
		}
	}
	res, err := r.applyLocked(seat, action, payload)
	if err != nil {
		return false, err
	}
	if actionSeq > 0 {
		if r.actionSeq == nil {
			r.actionSeq = map[string]int64{}
		}
		r.actionSeq[guestID] = actionSeq
	}
	r.commitLocked(res)
	return true, nil
}

// applyLocked 执行一次权威动作(调用方持锁), 不提交事件与活跃时间。
func (r *Room) applyLocked(seat game.Seat, action string, payload json.RawMessage) (game.ActionResult, error) {
	var (
		res game.ActionResult
		err error
	)
	switch action {
	case "draw":
		res, err = r.match.Draw()
	case "discard":
		var in struct {
			Tile string `json:"tile"`
		}
		if err = json.Unmarshal(payload, &in); err == nil {
			var k game.TileKind
			if k, err = parseTile(in.Tile); err == nil {
				res, err = r.match.Discard(seat, k)
			}
		}
	case "pong":
		res, err = r.match.Respond(seat, game.RespondAction{Pong: true})
	case "kong.exposed":
		res, err = r.match.Respond(seat, game.RespondAction{Kong: true})
	case "hu.robbing":
		res, err = r.match.Respond(seat, game.RespondAction{Win: true})
	case "pass":
		res, err = r.match.Respond(seat, game.RespondAction{Pass: true})
	case "chow":
		var in struct {
			With []string `json:"with"`
		}
		if err = json.Unmarshal(payload, &in); err == nil && len(in.With) == 2 {
			var a, b game.TileKind
			if a, err = parseTile(in.With[0]); err == nil {
				if b, err = parseTile(in.With[1]); err == nil {
					res, err = r.match.Respond(seat, game.RespondAction{
						Chow: &game.ChowCombo{With: [2]game.TileKind{a, b}},
					})
				}
			}
		} else if err == nil {
			err = errors.New("chow 需要 with 两张牌")
		}
	case "kong.concealed", "kong.added":
		var in struct {
			Tile string `json:"tile"`
		}
		if err = json.Unmarshal(payload, &in); err == nil {
			var k game.TileKind
			if k, err = parseTile(in.Tile); err == nil {
				res, err = r.match.KongSelf(seat, k, action == "kong.added")
			}
		}
	case "hu.self":
		res, err = r.match.WinSelf(seat)
	case "forcedraw":
		// 关闭响应倒计时时, 出牌者的下家可用「摸牌」直接收敛响应窗口。
		res, err = r.match.ForceDraw(seat)
	default:
		err = fmt.Errorf("未知动作: %s", action)
	}
	return res, err
}

// commitLocked 提交动作结果: 状态推进、公共事件(调用方持锁)。
// 计时由引擎持有(阶段10 的可配置响应/出牌倒计时), 这里只做镜像,
// 供 CurrentTurn 与调试使用; 不再自行设定超时, 避免两套计时互相覆盖。
func (r *Room) commitLocked(res game.ActionResult) {
	if r.match.Phase() == game.PhaseFinished {
		r.Status = StatusFinished
	}
	r.turnDeadline = r.match.DiscardDeadline()
	r.lastActivity = time.Now()
	// 公共事件(不含私有视图)。若引擎未产生事件(如中间 pass), 追加一条
	// 状态事件保证客户端 seq 前进(否则响应窗口会因无反馈而死锁)。
	if len(res.Events) == 0 {
		r.appendGameEventLocked("state", -1, nil)
		return
	}
	for _, ev := range res.Events {
		r.appendGameEventLocked(ev.Kind, int(ev.Seat), tilesToCodes(ev.Tiles))
	}
}

// appendGameEventLocked 追加一条公共对局事件。
// 私有信息(手牌/可行动作)绝不进入事件日志; 下发时由传输层调用 GameViewFor
// 按接收者座位实时投影, 从结构上消除泄漏面。
func (r *Room) appendGameEventLocked(kind string, seat int, tiles []string) {
	out := protocol.GameEventOut{Kind: kind, Seat: seat, Tiles: tiles}
	r.seq++
	env, err := protocol.NewEvent(r.seq, protocol.EvGameEvent, out)
	if err != nil {
		env = protocol.Envelope{V: protocol.Version, Seq: r.seq, Type: protocol.EvError}
	}
	r.events = append(r.events, env)
}

// MatchFinished 对局是否已结束。
func (r *Room) MatchFinished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.match != nil && r.match.Phase() == game.PhaseFinished
}

// parseTile 解析牌代码。
func parseTile(code string) (game.TileKind, error) {
	k, ok := game.KindFromCode(code)
	if !ok {
		return 0, fmt.Errorf("未知牌代码: %q", code)
	}
	return k, nil
}

// tilesToCodes 种类转线缆代码。
func tilesToCodes(tiles []game.TileKind) []string {
	out := make([]string, len(tiles))
	for i, k := range tiles {
		out[i] = k.Code()
	}
	return out
}

// leave 玩家离开。
// 房主离开时把房主身份交给剩余真人(按座位号最小者), 避免房间失去
// 「能开局的人」而只能靠关网页重置; 若最后一位真人离开, 一并清掉机器人
// 与对局, 让注册表能回收房间。
func (r *Room) leave(guestID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[guestID]
	if !ok {
		return ErrNotInRoom
	}
	// 对局中离开: 不能直接删座位。引擎按固定座位组成手牌与轮转,
	// 删掉一家会让牌局卡死在这家的回合上(这正是"返回的人占着位置,
	// 导致无法继续游戏"的根因)。改为机器人托管: 其他人先把这局打完,
	// 该座位在一局结束/下次入座时会被 purgeLeftLocked 清掉。
	if r.match != nil && r.match.Phase() != game.PhaseFinished {
		p.IsBot = true
		p.Left = true
		p.Connected = false
		p.disconnectedAt = time.Now()
		delete(r.ready, guestID)
		if guestID == r.HostGuestID {
			// 主动离开: 立即交棒, 不等断线宽限(这是"预先安全移交"路径)。
			r.promoteHostLocked(MigrateHostLeave)
		}
		if !r.hasHumanLocked() {
			// 真人已经走光(剩下的都是托管/机器人), 没人会再看这局:
			// 直接收摊交还给注册表, 不让一个全机器人的房间空转。
			r.teardownLocked()
			return nil
		}
		r.lastActivity = time.Now()
		r.appendEventLocked(protocol.EvRoomState, nil)
		// 真正的推进一步交给注册表的定时 tick(它会在 1 秒内让托管座位行动),
		// 这里不能直接调 AutoStep —— 它要拿同一把锁。
		return nil
	}
	delete(r.players, guestID)
	delete(r.ready, guestID)
	delete(r.actionSeq, guestID)
	r.seatTaken[p.Seat] = false
	if guestID == r.HostGuestID {
		// 主动离开: 立即交棒, 不等断线宽限(这是"预先安全移交"路径)。
		r.promoteHostLocked(MigrateHostLeave)
	}
	if !r.hasHumanLocked() {
		r.teardownLocked()
		return nil
	}
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, r.viewLocked(guestID))
	return nil
}

// teardownLocked 清空房间并标记结束, 交给注册表回收(调用方持锁)。
func (r *Room) teardownLocked() {
	r.players = map[string]*Player{}
	r.ready = map[string]bool{}
	r.seatTaken = [MaxPlayers]bool{}
	r.match = nil
	r.Status = StatusFinished
	r.HostGuestID = ""
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, nil)
}

// hasHumanLocked 房内是否还有真人(调用方持锁)。
func (r *Room) hasHumanLocked() bool {
	for _, p := range r.players {
		if !p.IsBot {
			return true
		}
	}
	return false
}

// IsHost 是否为房主。
func (r *Room) IsHost(guestID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.HostGuestID != "" && r.HostGuestID == guestID
}

// SetConnected 更新连接状态(断线/重连), 并产生事件。
//
// 重连时若座位已被回收为托管(阶段7), 立即把座位收回给本人:
// 「重连玩家恢复其座位」是房主迁移方案的一部分, 否则掉线一分钟后
// 玩家回来会发现自己在替机器人看牌。
func (r *Room) SetConnected(guestID string, connected bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[guestID]
	if !ok {
		return
	}
	changed := false
	if p.Connected != connected {
		p.Connected = connected
		changed = true
		if connected {
			p.disconnectedAt = time.Time{}
		} else {
			p.disconnectedAt = time.Now()
		}
	}
	if connected && p.IsBot {
		// 托管座位归还本人(游客身份未变, 只翻标志位)。
		p.IsBot = false
		changed = true
	}
	if !changed {
		return
	}
	// 房主回来时若房主已迁移给别人, 不做回抢: 任期只增不减,
	// 避免出现「双房主」和权限来回抖动。
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, r.viewLocked(guestID))
}

// ReconnectGrace 大厅/已结束房间里, 玩家断线超过该时长即回收座位,
// 避免「关了网页的幽灵玩家」永久占座、让房间无法开局。
const ReconnectGrace = 60 * time.Second

// reapDisconnected 回收大厅或已结束房间里长时间断线的真人座位。
// 对局中不回收(改为托管, 见 reclaimSeatsLocked)。返回是否发生变化。
func (r *Room) reapDisconnected(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.match != nil && r.match.Phase() != game.PhaseFinished {
		return false
	}
	var gone []string
	for _, p := range r.players {
		if p.IsBot || p.Connected || p.disconnectedAt.IsZero() {
			continue
		}
		if now.Sub(p.disconnectedAt) >= ReconnectGrace {
			gone = append(gone, p.GuestID)
		}
	}
	if len(gone) == 0 {
		return false
	}
	hostGone := false
	for _, guestID := range gone {
		p, ok := r.players[guestID]
		if !ok {
			continue
		}
		delete(r.players, guestID)
		delete(r.ready, guestID)
		r.seatTaken[p.Seat] = false
		if guestID == r.HostGuestID {
			hostGone = true
		}
	}
	if hostGone {
		r.promoteHostLocked(MigrateHostReap)
	}
	if !r.hasHumanLocked() {
		r.players = map[string]*Player{}
		r.ready = map[string]bool{}
		r.seatTaken = [MaxPlayers]bool{}
		r.match = nil
		r.Status = StatusFinished
		r.HostGuestID = ""
	}
	r.lastActivity = now
	r.appendEventLocked(protocol.EvRoomState, nil)
	return true
}

// AppendEvent 追加自定义事件并分配序列号。
func (r *Room) AppendEvent(typ string, payload any) (protocol.Envelope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appendEventLocked(typ, payload), nil
}

// appendEventLocked 追加事件(调用方持锁)。失败负载直接以 error 事件落地, 不中断流程。
func (r *Room) appendEventLocked(typ string, payload any) protocol.Envelope {
	r.seq++
	env, err := protocol.NewEvent(r.seq, typ, payload)
	if err != nil {
		env = protocol.Envelope{V: protocol.Version, Seq: r.seq, Type: protocol.EvError}
	}
	r.events = append(r.events, env)
	return env
}

// EventsSince 返回序列号大于 since 的事件(断线重连增量同步)。
func (r *Room) EventsSince(since int64) []protocol.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := 0
	for idx < len(r.events) && r.events[idx].Seq <= since {
		idx++
	}
	out := make([]protocol.Envelope, len(r.events)-idx)
	copy(out, r.events[idx:])
	return out
}

// GameSeq 日志中最后一条对局事件的序列号(仅对局事件空间, 供连接水位使用)。
// 与 ReserveSeq 追加的下行序号不同域: 水位比较必须用同一事件空间。
func (r *Room) GameSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Type == protocol.EvGameEvent {
			return r.events[i].Seq
		}
	}
	return 0
}

// ReserveSeq 追加一个序列号但不入日志。
// 用于把对局事件也放进与房间事件相同的单调序列空间,
// 否则客户端会看到两个独立计数器造成的 seq 回退。
func (r *Room) ReserveSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return r.seq
}

// CurrentSeq 当前最新事件序列号。
func (r *Room) CurrentSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// ViewFor 生成指定游客视角的房间可见状态投影。
// 关键安全约束: 其他玩家永远只暴露昵称/座位/连接状态/手牌数量;
// 手牌内容仅出现在 You 字段且仅对本人下发。
func (r *Room) ViewFor(guestID string) protocol.RoomStateView {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.viewLocked(guestID)
}

func (r *Room) viewLocked(forGuest string) protocol.RoomStateView {
	view := protocol.RoomStateView{
		RoomID:      r.ID,
		Code:        r.Code,
		Mode:        string(r.Mode),
		Status:      string(r.Status),
		Theme:       string(r.Theme),
		Name:        r.Name,
		IsPublic:    r.IsPublic,
		HasPassword: r.PasswordHash != "",
		RulesLocked: r.RulesLocked,
		Capacity:    r.RuleSet.Players,
		Seq:         r.seq,
		HostEpoch:   r.hostEpoch,
		Rules: &protocol.RulesView{
			Players:        r.RuleSet.Players,
			Chow:           r.RuleSet.Chow,
			SevenPairs:     r.RuleSet.SevenPairs,
			SelfDraw:       r.RuleSet.SelfDraw,
			DiscardWin:     r.RuleSet.DiscardWin,
			BaseScore:      r.RuleSet.BaseScore,
			RespondMeldSec: r.RuleSet.RespondMeldSec,
			RespondWinSec:  r.RuleSet.RespondWinSec,
			DiscardSec:     r.RuleSet.DiscardSec,
			RobWin:         r.RuleSet.RobWin,
			RobMeld:        r.RuleSet.RobMeld,
			RobKongWin:     r.RuleSet.RobKongWin,
			KongFlower:     r.RuleSet.KongFlower,
			LastTile:       r.RuleSet.LastTile,
		},
	}
	for _, p := range r.players {
		pv := protocol.PlayerView{
			GuestID:   p.GuestID,
			Nickname:  p.Nickname,
			Seat:      p.Seat,
			Connected: p.Connected,
			Ready:     p.IsBot || r.ready[p.GuestID] || r.match != nil,
			IsBot:     p.IsBot,
			IsHost:    p.GuestID == r.HostGuestID,
		}
		// 对局中暴露手牌数量(内容永不可见: 只取投影的 Hand 长度, 不转发内容)。
		if r.match != nil {
			if sv := r.match.ViewFor(game.Seat(p.Seat)); len(sv.Hand) > 0 {
				pv.TileCount = len(sv.Hand)
				if sv.Drawn != "" {
					pv.TileCount++
				}
			}
		}
		view.Players = append(view.Players, pv)
	}
	for i := 1; i < len(view.Players); i++ {
		for j := i; j > 0 && view.Players[j].Seat < view.Players[j-1].Seat; j-- {
			view.Players[j], view.Players[j-1] = view.Players[j-1], view.Players[j]
		}
	}
	if forGuest != "" {
		if p, ok := r.players[forGuest]; ok {
			view.You = &protocol.YouView{
				GuestID: p.GuestID,
				Seat:    p.Seat,
				// Hand 阶段2起填充本人手牌代码。
			}
		}
	}
	return view
}
