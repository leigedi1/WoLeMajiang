// 阶段5: 房间大厅能力 —— 房间名/公开性/房间密码、准备状态、机器人座位、
// 邀请令牌、房主权限与解散。全部状态变更在 Room.mu 下进行, 事件日志保持单调递增。
package room

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"majiang-server/internal/game"
	"majiang-server/internal/protocol"
	"majiang-server/internal/secret"
)

// 阶段5 错误。调用方(传输层)据此映射为稳定的错误码。
var (
	ErrBadPassword        = errors.New("房间密码错误")
	ErrNotHost            = errors.New("仅房主可执行该操作")
	ErrRulesLocked        = errors.New("开局后规则已锁定")
	ErrUnsupportedPlayers = errors.New("该人数规则尚未完成, 仅支持 4 人开局")
	ErrNotEnoughSeats     = errors.New("座位未坐满, 请补入机器人或等待玩家")
	ErrNotAllReady        = errors.New("还有玩家未准备, 无法开始对局")
	ErrSeatOccupied       = errors.New("该座位已被占用")
	ErrSeatEmpty          = errors.New("该座位没有机器人")
	ErrInviteNotFound     = errors.New("邀请不存在")
	ErrInviteExpired      = errors.New("邀请已过期")
	ErrInviteRevoked      = errors.New("邀请已撤销")
	ErrInviteUsedUp       = errors.New("邀请使用次数已用尽")
	ErrInvitePassword     = errors.New("邀请密码错误")
	ErrBadRoomName        = errors.New("房间名长度需在 1~24 字符")
	ErrAlreadyStarted     = errors.New("对局已开始")
)

// MaxRoomName 房间名最大字符数(按 rune 计)。
const MaxRoomName = 24

// CreateOptions 创建房间参数。
type CreateOptions struct {
	GuestID  string // 房主游客 ID
	Nickname string
	Name     string
	Password string // 明文, 立即哈希; 空串表示无密码
	Public   bool
	RuleSet  game.RuleSet
	Theme    game.Theme
}

// Invite 邀请记录(内存权威, 持久层只存哈希)。令牌明文不落内存、不落库。
type Invite struct {
	ID                 string
	TokenHash          string
	PasswordHash       string
	BypassRoomPassword bool
	MaxUses            int
	Uses               int
	Revoked            bool
	ExpiresAt          time.Time // 零值 = 不过期
	CreatedAt          time.Time
}

// expired 是否已过期。
func (i *Invite) expired(now time.Time) bool {
	return !i.ExpiresAt.IsZero() && now.After(i.ExpiresAt)
}

// usable 是否仍可用。
func (i *Invite) usable(now time.Time) error {
	if i.Revoked {
		return ErrInviteRevoked
	}
	if i.expired(now) {
		return ErrInviteExpired
	}
	if i.MaxUses > 0 && i.Uses >= i.MaxUses {
		return ErrInviteUsedUp
	}
	return nil
}

// view 生成可见信息(不含令牌明文)。
func (i *Invite) view() protocol.InviteView {
	var exp int64
	if !i.ExpiresAt.IsZero() {
		exp = i.ExpiresAt.Unix()
	}
	return protocol.InviteView{
		ID:                 i.ID,
		HasPassword:        i.PasswordHash != "",
		BypassRoomPassword: i.BypassRoomPassword,
		MaxUses:            i.MaxUses,
		Uses:               i.Uses,
		Revoked:            i.Revoked,
		ExpiresAt:          exp,
		CreatedAt:          i.CreatedAt.Unix(),
	}
}

// CreateInviteOptions 创建邀请参数。
type CreateInviteOptions struct {
	ExpiresInSec       int64  // <=0 表示不过期
	MaxUses            int    // <=0 表示不限次数
	Password           string // 邀请独立密码(明文, 立即哈希)
	BypassRoomPassword bool   // 使用该邀请加入时免房间密码
}

// SetConfig 房主修改房间名/公开性/密码(仅大厅阶段)。
// 传 nil 表示该项不变; password 为指向字符串的指针, 指向空串表示清除密码。
func (r *Room) SetConfig(guestID string, name, password *string, public *bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.HostGuestID != guestID {
		return ErrNotHost
	}
	if r.Status != StatusLobby {
		return ErrRulesLocked
	}
	if name != nil {
		n := strings.TrimSpace(*name)
		if len([]rune(n)) < 1 || len([]rune(n)) > MaxRoomName {
			return ErrBadRoomName
		}
		r.Name = n
	}
	if password != nil {
		h, err := secret.HashPassword(*password)
		if err != nil {
			return err
		}
		r.PasswordHash = h
	}
	if public != nil {
		r.IsPublic = *public
	}
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, r.viewLocked(guestID))
	return nil
}

// SetReady 设置本人准备状态(大厅阶段)。
func (r *Room) SetReady(guestID string, ready bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[guestID]
	if !ok {
		return ErrNotInRoom
	}
	if r.Status != StatusLobby {
		return ErrRulesLocked
	}
	if p.IsBot {
		return errors.New("机器人无需准备")
	}
	r.ready[guestID] = ready
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, r.viewLocked(guestID))
	return nil
}

// botGuestID 机器人占位 guestID(不参与鉴权, 只作为座位拥有者)。
func botGuestID(roomID string, seat int) string {
	return fmt.Sprintf("bot:%s:%d", roomID, seat)
}

// SetBot 增删机器人(房主权限, 大厅或上一局结束后的待开局阶段)。
// 允许 StatusFinished: 一局打完后有人离开时, 房主可以补机器人直接开下一局。
func (r *Room) SetBot(guestID string, seat int, add bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.HostGuestID != guestID {
		return ErrNotHost
	}
	if r.Status != StatusLobby && r.Status != StatusFinished {
		return ErrRulesLocked
	}
	if seat < 0 || seat >= MaxPlayers {
		return fmt.Errorf("座位越界: %d", seat)
	}
	if add {
		if r.seatTaken[seat] {
			return ErrSeatOccupied
		}
		id := botGuestID(r.ID, seat)
		r.seatTaken[seat] = true
		r.players[id] = &Player{
			GuestID:   id,
			Nickname:  fmt.Sprintf("电脑 %d", seat+1),
			Seat:      seat,
			Connected: true,
			IsBot:     true,
			Ready:     true,
		}
	} else {
		removed := false
		for id, p := range r.players {
			if p.IsBot && p.Seat == seat {
				delete(r.players, id)
				delete(r.ready, id)
				r.seatTaken[seat] = false
				removed = true
				break
			}
		}
		if !removed {
			return ErrSeatEmpty
		}
	}
	r.lastActivity = time.Now()
	r.appendEventLocked(protocol.EvRoomState, r.viewLocked(guestID))
	return nil
}

// CreateInvite 房主创建邀请, 返回令牌明文(仅此一次)。
func (r *Room) CreateInvite(guestID string, opts CreateInviteOptions) (string, protocol.InviteView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.HostGuestID != guestID {
		return "", protocol.InviteView{}, ErrNotHost
	}
	token, hash, err := secret.NewInviteToken()
	if err != nil {
		return "", protocol.InviteView{}, err
	}
	pwHash, err := secret.HashPassword(opts.Password)
	if err != nil {
		return "", protocol.InviteView{}, err
	}
	id, err := secret.RandomID(12)
	if err != nil {
		return "", protocol.InviteView{}, err
	}
	inv := &Invite{
		ID:                 id,
		TokenHash:          hash,
		PasswordHash:       pwHash,
		BypassRoomPassword: opts.BypassRoomPassword,
		MaxUses:            opts.MaxUses,
		CreatedAt:          time.Now(),
	}
	if opts.ExpiresInSec > 0 {
		inv.ExpiresAt = time.Now().Add(time.Duration(opts.ExpiresInSec) * time.Second)
	}
	r.invites[id] = inv
	r.lastActivity = time.Now()
	return token, inv.view(), nil
}

// ListInvites 房主查看本房间邀请。
func (r *Room) ListInvites(guestID string) ([]protocol.InviteView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.HostGuestID != guestID {
		return nil, ErrNotHost
	}
	out := make([]protocol.InviteView, 0, len(r.invites))
	for _, inv := range r.invites {
		out = append(out, inv.view())
	}
	return out, nil
}

// RevokeInvite 房主撤销邀请。
func (r *Room) RevokeInvite(guestID, inviteID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.HostGuestID != guestID {
		return ErrNotHost
	}
	inv, ok := r.invites[inviteID]
	if !ok {
		return ErrInviteNotFound
	}
	inv.Revoked = true
	r.lastActivity = time.Now()
	return nil
}

// Dissolve 房主解散房间: 清空玩家与邀请, 由注册表回收。
func (r *Room) Dissolve(guestID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.HostGuestID != guestID {
		return ErrNotHost
	}
	for _, p := range r.players {
		r.seatTaken[p.Seat] = false
	}
	r.players = map[string]*Player{}
	r.ready = map[string]bool{}
	r.invites = map[string]*Invite{}
	r.match = nil
	r.Status = StatusFinished
	r.lastActivity = time.Now()
	return nil
}

// LobbyEntry 公开大厅条目(仅公开且未解散的房间)。
func (r *Room) LobbyEntry() (protocol.LobbyRoomView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.IsPublic || r.Status == StatusFinished {
		return protocol.LobbyRoomView{}, false
	}
	host := ""
	players := 0
	for _, p := range r.players {
		players++
		if p.GuestID == r.HostGuestID {
			host = p.Nickname
		}
	}
	return protocol.LobbyRoomView{
		Code:         r.Code,
		Name:         r.Name,
		Status:       string(r.Status),
		Theme:        string(r.Theme),
		Players:      players,
		Capacity:     r.RuleSet.Players,
		HasPassword:  r.PasswordHash != "",
		HostNickname: host,
		UpdatedAt:    r.lastActivity.Unix(),
	}, true
}

// Capacity 房间规则人数。
func (r *Room) Capacity() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.RuleSet.Players
}

// verifyPasswordLocked 校验房间密码(调用方持锁)。
func (r *Room) verifyPasswordLocked(password string) error {
	if r.PasswordHash == "" {
		return nil
	}
	if !secret.VerifyPassword(r.PasswordHash, password) {
		return ErrBadPassword
	}
	return nil
}

// consumeInviteToken 校验并消费一个邀请令牌。
// 返回 bypassPassword 表示该邀请携带免房间密码能力。
// 失败时返回稳定的邀请错误, 调用方不得回退到"按房间密码尝试"以免绕过次数限制。
func (r *Room) consumeInviteToken(token string) (bool, error) {
	hash := secret.HashInviteToken(strings.TrimSpace(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var inv *Invite
	for _, it := range r.invites {
		if secret.ConstantTimeEqual(it.TokenHash, hash) {
			inv = it
			break
		}
	}
	if inv == nil {
		return false, ErrInviteNotFound
	}
	if err := inv.usable(now); err != nil {
		return false, err
	}
	inv.Uses++
	r.lastActivity = now
	return inv.BypassRoomPassword, nil
}

// inviteRecord 导出邀请的持久化记录(调用方持锁或经注册表串行化)。
func (r *Room) inviteRecord(inviteID string) (InviteRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invites[inviteID]
	if !ok {
		return InviteRecord{}, false
	}
	var exp int64
	if !inv.ExpiresAt.IsZero() {
		exp = inv.ExpiresAt.Unix()
	}
	return InviteRecord{
		ID:                 inv.ID,
		RoomID:             r.ID,
		TokenHash:          inv.TokenHash,
		PasswordHash:       inv.PasswordHash,
		BypassRoomPassword: inv.BypassRoomPassword,
		MaxUses:            inv.MaxUses,
		Uses:               inv.Uses,
		Revoked:            inv.Revoked,
		ExpiresAt:          exp,
		CreatedAt:          inv.CreatedAt.Unix(),
	}, true
}

// RulesLockedState 报告规则是否已锁定(开局后不可修改)。
func (r *Room) RulesLockedState() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.RulesLocked
}

// SeatOf 返回游客座位(机器人也适用), ok=false 表示不在房内。
func (r *Room) SeatOf(guestID string) (game.Seat, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.players[guestID]
	if !ok {
		return 0, false
	}
	return game.Seat(p.Seat), true
}

// BotGuestIDs 返回房内机器人 guestID 列表(测试与驱动用)。
func (r *Room) BotGuestIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for id, p := range r.players {
		if p.IsBot {
			out = append(out, id)
		}
	}
	return out
}
