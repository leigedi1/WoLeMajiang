package room

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"majiang-server/internal/protocol"
	"majiang-server/internal/secret"
)

// 房间操作错误。
var (
	ErrRoomFull       = errors.New("房间已满")
	ErrRoomStarted    = errors.New("房间已开局, 无法加入")
	ErrAlreadyInRoom  = errors.New("已在房间内")
	ErrNotInRoom      = errors.New("不在该房间内")
	ErrRoomNotFound   = errors.New("房间不存在")
	ErrMaxOnlineRooms = errors.New("在线房间数量已达上限")
	ErrMatchInProgress = errors.New("对局已在进行中")
	ErrNeedPlayers    = errors.New("开局至少需要 2 名玩家")
	ErrNoMatch        = errors.New("房间内没有进行中的对局")
)

// RoomRecord 房间元数据持久化记录(与 store.RoomRow 解耦, 由适配层转换)。
type RoomRecord struct {
	ID           string
	Code         string
	HostGuestID  string
	Mode         string
	RuleSet      string
	Theme        string
	Status       string
	Name         string
	IsPublic     bool
	PasswordHash string
	CreatedAt    int64
	UpdatedAt    int64
}

// Persister 房间元数据持久化接口(可选; nil 表示纯内存, 便于测试)。
type Persister interface {
	CreateRoom(ctx context.Context, rec RoomRecord) error
	DeleteRoom(ctx context.Context, id string) error
	UpdateRoomActivity(ctx context.Context, id, status string) error
	CreateInvite(ctx context.Context, rec InviteRecord) error
	RevokeInvite(ctx context.Context, id string) error
	DeleteInvitesByRoom(ctx context.Context, roomID string) error
}

// InviteRecord 邀请持久化记录(令牌与密码只有哈希)。
type InviteRecord struct {
	ID                 string
	RoomID             string
	TokenHash          string
	PasswordHash       string
	BypassRoomPassword bool
	MaxUses            int
	Uses               int
	Revoked            bool
	ExpiresAt          int64
	CreatedAt          int64
}

// Registry 在线房间注册表(服务器权威)。P2P/LAN 房间不进入本注册表。
type Registry struct {
	mu      sync.Mutex
	rooms   map[string]*Room // by id
	byCode  map[string]*Room // by invite code
	maxOnl  int
	idleTTL time.Duration
	persist Persister
}

// NewRegistry 创建注册表。maxOnline 为在线房间上限(默认 3); idleTTL 为空房清理时长。
func NewRegistry(maxOnline int, idleTTL time.Duration, persist Persister) *Registry {
	return &Registry{
		rooms:   map[string]*Room{},
		byCode:  map[string]*Room{},
		maxOnl:  maxOnline,
		idleTTL: idleTTL,
		persist: persist,
	}
}

// roomAlphabet 邀请码字符集: 去除易混淆的 0/O/1/I/L。
const roomAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// randomCode 生成 6 位邀请码。
func randomCode() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成邀请码: %w", err)
	}
	out := make([]byte, 6)
	for i, v := range b {
		out[i] = roomAlphabet[int(v)%len(roomAlphabet)]
	}
	return string(out), nil
}

// randomHexID 生成 16 字节随机 hex(32 字符)。
func randomHexID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成房间 ID: %w", err)
	}
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 32)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0xf]
	}
	return string(out), nil
}

// Create 创建在线房间, 房主立即入座 0 号位。
func (r *Registry) Create(ctx context.Context, opts CreateOptions) (*Room, error) {
	ruleSet := opts.RuleSet
	if err := ruleSet.Validate(); err != nil {
		return nil, err
	}
	if !opts.Theme.Valid() {
		return nil, fmt.Errorf("非法主题: %q", opts.Theme)
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "未命名房间"
	}
	if len([]rune(name)) > MaxRoomName {
		return nil, ErrBadRoomName
	}
	pwHash, err := secret.HashPassword(opts.Password)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if len(r.rooms) >= r.maxOnl {
		r.mu.Unlock()
		return nil, ErrMaxOnlineRooms
	}
	// 生成不冲突的 ID 与邀请码。
	var id, code string
	for {
		var err error
		if id, err = randomHexID(); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		if _, dup := r.rooms[id]; dup {
			continue
		}
		if code, err = randomCode(); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		if _, dup := r.byCode[code]; dup {
			continue
		}
		break
	}
	now := time.Now()
	room := &Room{
		ID:           id,
		Code:         code,
		Name:         name,
		IsPublic:     opts.Public,
		PasswordHash: pwHash,
		HostGuestID:  opts.GuestID,
		Mode:         ModeOnline,
		Status:       StatusLobby,
		Theme:        opts.Theme,
		RuleSet:      ruleSet,
		CreatedAt:    now,
		lastActivity: now,
		players:      map[string]*Player{},
		ready:        map[string]bool{},
		invites:      map[string]*Invite{},
	}
	r.rooms[id] = room
	r.byCode[code] = room
	r.mu.Unlock()

	if _, err := room.join(opts.GuestID, opts.Nickname, "", true); err != nil {
		// 理论不可达: 新房直接加入房主。
		r.mu.Lock()
		delete(r.rooms, id)
		delete(r.byCode, code)
		r.mu.Unlock()
		return nil, fmt.Errorf("房主入座: %w", err)
	}
	if r.persist != nil {
		if err := r.persist.CreateRoom(ctx, RoomRecord{
			ID: id, Code: code, HostGuestID: opts.GuestID, Mode: string(ModeOnline),
			RuleSet: `{"players":` + itoa(ruleSet.Players) + `}`,
			Theme:   string(opts.Theme), Status: string(StatusLobby),
			Name: name, IsPublic: opts.Public, PasswordHash: pwHash,
			CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		}); err != nil {
			r.mu.Lock()
			delete(r.rooms, id)
			delete(r.byCode, code)
			r.mu.Unlock()
			return nil, fmt.Errorf("持久化房间: %w", err)
		}
	}
	return room, nil
}

// JoinOptions 加入房间参数。
type JoinOptions struct {
	GuestID     string
	Nickname    string
	IDOrCode    string
	Password    string // 房间密码明文
	InviteToken string // 邀请令牌明文(可选; 优先级高于房间密码)
}

// Join 通过房间 ID / 邀请码加入; 支持房间密码与邀请令牌。
func (r *Registry) Join(ctx context.Context, opts JoinOptions) (*Room, error) {
	code := strings.ToUpper(strings.TrimSpace(opts.IDOrCode))
	r.mu.Lock()
	room := r.rooms[opts.IDOrCode]
	if room == nil {
		room = r.byCode[code]
	}
	if room == nil {
		room = r.byCode[opts.IDOrCode]
	}
	r.mu.Unlock()
	if room == nil {
		return nil, ErrRoomNotFound
	}
	bypass := false
	if opts.InviteToken != "" {
		ok, err := room.consumeInviteToken(opts.InviteToken)
		if err != nil {
			return nil, err
		}
		bypass = ok
	}
	if _, err := room.join(opts.GuestID, opts.Nickname, opts.Password, bypass); err != nil {
		return nil, err
	}
	if r.persist != nil {
		_ = r.persist.UpdateRoomActivity(ctx, room.ID, string(room.Status))
	}
	return room, nil
}

// PublicRooms 公开大厅列表(仅公开房间)。
func (r *Registry) PublicRooms() []protocol.LobbyRoomView {
	r.mu.Lock()
	rooms := make([]*Room, 0, len(r.rooms))
	for _, room := range r.rooms {
		rooms = append(rooms, room)
	}
	r.mu.Unlock()
	out := make([]protocol.LobbyRoomView, 0, len(rooms))
	for _, room := range rooms {
		if entry, ok := room.LobbyEntry(); ok {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// Dissolve 房主解散房间(立即从索引移除并清理持久化)。
func (r *Registry) Dissolve(ctx context.Context, guestID, roomID string) error {
	r.mu.Lock()
	room := r.rooms[roomID]
	r.mu.Unlock()
	if room == nil {
		return ErrRoomNotFound
	}
	if err := room.Dissolve(guestID); err != nil {
		return err
	}
	r.remove(room)
	if r.persist != nil {
		_ = r.persist.DeleteRoom(ctx, room.ID)
	}
	return nil
}

// CreateInvite 房主创建邀请并落库, 返回令牌明文。
func (r *Registry) CreateInvite(ctx context.Context, guestID, roomID string, opts CreateInviteOptions) (string, protocol.InviteView, error) {
	r.mu.Lock()
	room := r.rooms[roomID]
	r.mu.Unlock()
	if room == nil {
		return "", protocol.InviteView{}, ErrRoomNotFound
	}
	token, view, err := room.CreateInvite(guestID, opts)
	if err != nil {
		return "", view, err
	}
	if r.persist != nil {
		// 以房间内的权威邀请记录为准落库(令牌只有哈希)。
		if rec, ok := room.inviteRecord(view.ID); ok {
			_ = r.persist.CreateInvite(ctx, rec)
		}
	}
	return token, view, nil
}

// Leave 离开房间; 房间变空时立即回收。
func (r *Registry) Leave(ctx context.Context, guestID, roomID string) error {
	r.mu.Lock()
	room := r.rooms[roomID]
	r.mu.Unlock()
	if room == nil {
		return ErrRoomNotFound
	}
	if err := room.leave(guestID); err != nil {
		return err
	}
	if room.PlayerCount() == 0 {
		r.remove(room)
		if r.persist != nil {
			_ = r.persist.DeleteRoom(ctx, room.ID)
		}
	}
	return nil
}

// Get 按 ID 或邀请码查询。
func (r *Registry) Get(idOrCode string) (*Room, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if room, ok := r.rooms[idOrCode]; ok {
		return room, true
	}
	room, ok := r.byCode[idOrCode]
	return room, ok
}

// FindByGuest 返回游客当前所在的在线房间(断线重连时恢复会话用)。
// 不变量: 同一游客同一时刻至多存在于一个房间(加入前强制离房)。
func (r *Registry) FindByGuest(guestID string) (*Room, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, room := range r.rooms {
		if room.HasPlayer(guestID) {
			return room, true
		}
	}
	return nil, false
}

// Tick 推进所有对局中的房间(机器人立即行动, 人类超时托管)。
// 返回发生变化的房间, 调用方据此广播新事件。
func (r *Registry) Tick(now time.Time) []*Room {
	r.mu.Lock()
	rooms := make([]*Room, 0, len(r.rooms))
	for _, rm := range r.rooms {
		rooms = append(rooms, rm)
	}
	r.mu.Unlock()
	var changed []*Room
	for _, rm := range rooms {
		moved := rm.AutoStep(now)
		// 阶段7: 房主断线超时自动迁移 + 对局中掉线座位转托管。
		// 顺序有意如此: 先把"没人在"的座位交给机器人, 再判断房主是否还在,
		// 这样房主刚被转托管时也能立刻选出接任者。
		reclaimed := rm.ReclaimSeats(now)
		migrated := rm.TickHost(now)
		// 大厅/已结束房间回收长时间断线的幽灵座位(对局中留座重连)。
		reaped := rm.reapDisconnected(now)
		// 一局结束后让"中途离开"的座位真正空出来, 否则剩下的人开不了下一局。
		purged := rm.PurgeLeft()
		if moved || reaped || reclaimed || migrated || purged {
			changed = append(changed, rm)
		}
	}
	return changed
}

// Count 当前在线房间数。
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rooms)
}

// remove 从索引移除(调用方确保语义正确)。
func (r *Registry) remove(room *Room) {
	r.mu.Lock()
	delete(r.rooms, room.ID)
	delete(r.byCode, room.Code)
	r.mu.Unlock()
}

// Sweep 清理超时空房, 返回被移除的房间 ID 列表。
// 空房判定: 距最近活跃超过 idleTTL, 或 房内无人。
func (r *Registry) Sweep(ctx context.Context, now time.Time) []string {
	r.mu.Lock()
	var idle []*Room
	for _, room := range r.rooms {
		if room.PlayerCount() == 0 || now.Sub(room.LastActivity()) > r.idleTTL {
			idle = append(idle, room)
		}
	}
	r.mu.Unlock()
	removed := make([]string, 0, len(idle))
	for _, room := range idle {
		r.remove(room)
		removed = append(removed, room.ID)
		if r.persist != nil {
			_ = r.persist.DeleteRoom(ctx, room.ID)
		}
	}
	return removed
}

// SweepLoop 周期清扫, 直到 ctx 取消。interval <= 0 时立即返回。
func (r *Registry) SweepLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sweep(ctx, time.Now())
		}
	}
}

// itoa 极简整数转字符串(避免仅为两处调用引入依赖差异)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// 引用 protocol 包以固化事件类型常量依赖(编译期校验)。
var _ = protocol.EvRoomState
