// Package sig 提供 WebRTC 信令中继层: SDP offer/answer/ICE 候选交换。
// 设计目标: 极薄, 不承载麻将业务, 只帮两端交换候选。
// - 独立房间注册表 (P2P 房间), 不计入在线托管房间上限 (MaxOnlineRooms=3)
// - 短房间码(本包自带 shortRoomCode), 可选密码 (仅 HMAC-SHA256 比对)
// - 限流: 单房间 maxSigPeers (默认 8)、单 peer maxSigMsgsPerMinute (默认 120)
// - 过期清理: 默认 10 分钟无信令即解散房间
package sig

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultSigRandomBytes 用于生成密码盐等临时随机。
func defaultSigRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

// 默认值
const (
	DefaultMaxSigPeers        = 8
	DefaultSigIdleTTL         = 10 * time.Minute
	DefaultSigPeersPerServer  = 256
	DefaultSigMsgsPerMinute   = 120
	DefaultSigMessageBytes    = 256 * 1024
	DefaultSigRoomCleanupTick = 30 * time.Second
	DefaultRoomCodeLen        = 6
)

// Config 信令服务配置。
type Config struct {
	MaxPeersPerServer int           `json:"maxPeersPerServer"`
	MaxPeersPerRoom   int           `json:"maxPeersPerRoom"`
	IdleRoomTTL       time.Duration `json:"idleRoomTTL"`
	MsgsPerMinute     int           `json:"msgsPerMinute"`
	MaxMessageBytes   int           `json:"maxMessageBytes"`
	RoomCodeLen       int           `json:"roomCodeLen"`
	// AllowedOrigins 额外可信 Origin(与主配置 CORS 白名单共用);
	// 用于反向代理改写 Host、导致「同源」判断不成立时兜底。
	AllowedOrigins []string `json:"allowedOrigins"`
}

// DefaultConfig 返回默认配置。
func DefaultConfig() Config {
	return Config{
		MaxPeersPerServer: DefaultSigPeersPerServer,
		MaxPeersPerRoom:   DefaultMaxSigPeers,
		IdleRoomTTL:       DefaultSigIdleTTL,
		MsgsPerMinute:     DefaultSigMsgsPerMinute,
		MaxMessageBytes:   DefaultSigMessageBytes,
		RoomCodeLen:       DefaultRoomCodeLen,
	}
}

// Peer WebRTC 端点身份 + 底层传输。
type Peer struct {
	ID       string     `json:"id"`
	Room     string     `json:"room"`
	IsHost   bool       `json:"isHost"`
	JoinedAt time.Time  `json:"joinedAt"`
	LastSeen time.Time  `json:"lastSeen"`

	// Transport 注入层 — ws/HTTP handler 负责桥接。
	Send func(payload []byte) error `json:"-"`
}

// Room P2P 信令房间注册表 — 不含麻将业务状态, 只存 peer 列表与密码哈希。
type Room struct {
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	PasswordOK bool      `json:"-"` // false → 无密码
	PWHMAC     []byte    `json:"-"`
	PWSalt     []byte    `json:"-"`
	HostID     string    `json:"hostId"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeen   time.Time `json:"lastSeen"`
	// HostToken 房主重连令牌: 房主信令断线后凭它用原房间码「重新占位」,
	// 避免任何人拿到房间码就能冒充房主。不发往任意 peer, 只在 create/ rehost 应答里回给房主本人。
	HostToken string `json:"-"`

	mu    sync.Mutex
	peers map[string]*Peer
}

// Peers 返回安全拷贝。
func (r *Room) Peers() []*Peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p)
	}
	return out
}

// AddPeer 原子加入; 返回 (peer, 旧peer被踢出信号)。
func (r *Room) AddPeer(p *Peer) (*Peer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kicked *Peer
	if old, ok := r.peers[p.ID]; ok {
		kicked = old
	}
	r.peers[p.ID] = p
	p.Room = r.Code
	r.LastSeen = time.Now()
	return kicked, kicked != nil
}

// RemovePeer 原子移除, 若不在则返回 false。
func (r *Room) RemovePeer(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.peers[id]; ok {
		delete(r.peers, id)
		return true
	}
	return false
}

// PeerCount 返回当前 peer 数。
func (r *Room) PeerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}

// Touch 更新 LastSeen。
func (r *Room) Touch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.LastSeen = time.Now()
}

// Broadcast 将 payload 发给除 exclude 外所有 peer。
func (r *Room) Broadcast(payload []byte, excludeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for id, p := range r.peers {
		if id == excludeID {
			continue
		}
		if err := p.Send(payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SendTo 定向发送。
func (r *Room) SendTo(peerID string, payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.peers[peerID]
	if !ok {
		return ErrPeerNotFound
	}
	return p.Send(payload)
}

// Registry 信令房间注册表 (P2P 专用, 与在线托管 Registry 解耦)。
type Registry struct {
	mu     sync.Mutex
	cfg    Config
	rooms  map[string]*Room
	peers  map[string]*Peer // peerID → Peer
	create map[string]bool  // 正在创建中 code → true (防重入)
}

// NewRegistry 新建注册表。
func NewRegistry(cfg Config) *Registry {
	return &Registry{
		cfg:    cfg,
		rooms:  make(map[string]*Room),
		peers:  make(map[string]*Peer),
		create: make(map[string]bool),
	}
}

// Errors
var (
	ErrRoomNotFound     = errors.New("sig: room not found")
	ErrRoomFull         = errors.New("sig: room full")
	ErrRoomExists       = errors.New("sig: room already creating")
	ErrInvalidPassword  = errors.New("sig: invalid password")
	ErrPeerNotFound     = errors.New("sig: peer not found")
	ErrServerFull       = errors.New("sig: server full")
	ErrInvalidCode      = errors.New("sig: invalid room code")
	ErrInvalidMessage   = errors.New("sig: invalid message")
	ErrRateLimited      = errors.New("sig: rate limited")
	ErrForbidden        = errors.New("sig: forbidden")
)

// CreateRoom 创建 P2P 信令房间; 生成短房间码, 可选密码哈希。
func (r *Registry) CreateRoom(name, password, hostPeerID string) (*Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.peers) >= r.cfg.MaxPeersPerServer {
		return nil, ErrServerFull
	}
	if name == "" {
		name = "P2P 房间"
	}

	code, err := shortRoomCode(r.cfg.RoomCodeLen)
	if err != nil {
		return nil, fmt.Errorf("shortcode: %w", err)
	}
	for r.rooms[code] != nil {
		code, err = shortRoomCode(r.cfg.RoomCodeLen)
		if err != nil {
			return nil, err
		}
	}

	now := time.Now()
	token, err := shortRoomCode(24)
	if err != nil {
		return nil, fmt.Errorf("host token: %w", err)
	}
	rm := &Room{
		Code:      code,
		Name:      name,
		HostID:    hostPeerID,
		CreatedAt: now,
		LastSeen:  now,
		HostToken: token,
		peers:     make(map[string]*Peer),
	}
	if password != "" {
		salt, err := defaultSigRandomBytes(16)
		if err != nil {
			return nil, fmt.Errorf("salt: %w", err)
		}
		rm.PWSalt = salt
		rm.PWHMAC = hmacSHA256([]byte(password), salt)
		rm.PasswordOK = true
	}
	r.rooms[code] = rm
	return rm, nil
}

// JoinRoom 按房间码加入, 校验密码。创建 Peer 并加入 room。
func (r *Registry) JoinRoom(code, password, peerID string) (*Room, *Peer, error) {
	r.mu.Lock()
	rm := r.rooms[code]
	if rm == nil {
		r.mu.Unlock()
		return nil, nil, ErrRoomNotFound
	}
	if rm.PasswordOK {
		if !hmac.Equal(hmacSHA256([]byte(password), rm.PWSalt), rm.PWHMAC) {
			r.mu.Unlock()
			return nil, nil, ErrInvalidPassword
		}
	}
	if rm.PeerCount() >= r.cfg.MaxPeersPerRoom {
		r.mu.Unlock()
		return nil, nil, ErrRoomFull
	}
	if len(r.peers) >= r.cfg.MaxPeersPerServer {
		r.mu.Unlock()
		return nil, nil, ErrServerFull
	}
	// 分配/复用 peer
	p, ok := r.peers[peerID]
	if !ok {
		p = &Peer{ID: peerID, JoinedAt: time.Now(), LastSeen: time.Now()}
	} else {
		p.LastSeen = time.Now()
	}
	p.Room = code
	r.peers[peerID] = p
	r.mu.Unlock()

	kicked, _ := rm.AddPeer(p)
	if kicked != nil {
		// 被踢者 Send 置空 (ws handler 负责 close), 我们这里只通知踢掉方。
	}
	return rm, p, nil
}

// LookupRoom 返回房间 (只读).
func (r *Registry) LookupRoom(code string) *Room {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rooms[code]
}

// Rehost 房主信令断线后的「重新占位」: 凭 HostToken 用原房间码回来。
// 手机切后台/锁屏时 WebSocket 常被系统断开, 若房间立即消失, 加入端会看到
// 「房间不存在」, 而房主回到页面后也拿不回原来的码 —— rehost 解决这个断链。
func (r *Registry) Rehost(code, hostToken, peerID string) (*Room, *Peer, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" || hostToken == "" {
		return nil, nil, ErrInvalidCode
	}
	r.mu.Lock()
	rm := r.rooms[code]
	if rm == nil {
		r.mu.Unlock()
		return nil, nil, ErrRoomNotFound
	}
	if subtle.ConstantTimeCompare([]byte(rm.HostToken), []byte(hostToken)) != 1 {
		r.mu.Unlock()
		return nil, nil, ErrForbidden
	}
	p, ok := r.peers[peerID]
	if !ok {
		p = &Peer{ID: peerID, JoinedAt: time.Now()}
	}
	p.LastSeen = time.Now()
	p.Room = code
	r.peers[peerID] = p
	rm.HostID = peerID
	r.mu.Unlock()

	rm.AddPeer(p)
	rm.Touch()
	return rm, p, nil
}

// RemovePeer 清理 peer (ws 关闭时调用)。
//
// 关键: 最后一个 peer 断线时**不立刻解散房间**。手机浏览器切后台/锁屏会
// 让信令 WebSocket 被系统断开, 若这里立即删房, 朋友在加入端只会看到
// 「房间不存在或已结束」, 房主回到页面也只能拿到新码。房间留给
// CleanupRooms 按 IdleRoomTTL 回收, 期间房主可 Rehost、加入端仍能解析到房间。
func (r *Registry) RemovePeer(peerID string) {
	r.mu.Lock()
	p, ok := r.peers[peerID]
	if !ok {
		r.mu.Unlock()
		return
	}
	delete(r.peers, peerID)
	rm := r.rooms[p.Room]
	r.mu.Unlock()

	if rm != nil {
		rm.RemovePeer(peerID)
		rm.Touch()
	}
}

// DeleteRoom 解散房间.
func (r *Registry) DeleteRoom(code string) {
	r.mu.Lock()
	rm := r.rooms[code]
	if rm == nil {
		r.mu.Unlock()
		return
	}
	delete(r.rooms, code)
	// 移出 peers 表
	for _, p := range rm.peers {
		delete(r.peers, p.ID)
	}
	r.mu.Unlock()
}

// CleanupRooms 清理超时空闲房间; 返回清理数。
func (r *Registry) CleanupRooms() int {
	cutoff := time.Now().Add(-r.cfg.IdleRoomTTL)
	r.mu.Lock()
	cleaned := 0
	for code, rm := range r.rooms {
		if rm.LastSeen.Before(cutoff) && rm.PeerCount() == 0 {
			delete(r.rooms, code)
			cleaned++
		}
	}
	r.mu.Unlock()
	return cleaned
}

// RoomCodes 返回所有活跃房间码 (用于列表).
func (r *Registry) RoomCodes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.rooms))
	for c := range r.rooms {
		out = append(out, c)
	}
	return out
}

// Stats 聚合快照。
type Stats struct {
	Rooms      int
	Peers      int
	MaxPeers   int
	MaxRooms   int
}

// Stats 返回当前统计。
func (r *Registry) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{
		Rooms:    len(r.rooms),
		Peers:    len(r.peers),
		MaxPeers: r.cfg.MaxPeersPerServer,
	}
}

// OnPeerSeen 更新 peer LastSeen (处理任何信令消息时调用).
func (r *Registry) OnPeerSeen(peerID string) {
	r.mu.Lock()
	p, ok := r.peers[peerID]
	if ok {
		p.LastSeen = time.Now()
	}
	r.mu.Unlock()
	if p != nil {
		if rm := r.LookupRoom(p.Room); rm != nil {
			rm.Touch()
		}
	}
}

// ---- 消息类型 (薄 envelope, 仅信令层需要) ----

// Op 信令 op 类型。
type Op string

const (
	OpCreate  Op = "room.create"   // 房主: 创建房间
	OpRehost  Op = "room.rehost"   // 房主: 信令断线后凭 hostToken 用原码重新占位
	OpJoin    Op = "room.join"     // 加入者: 按房间码加入
	OpLeave   Op = "room.leave"    // 主动离开
	OpList    Op = "room.list"     // 列出公开 P2P 房间
	OpOffer   Op = "signal.offer"  // SDP offer
	OpAnswer  Op = "signal.answer" // SDP answer
	OpICE     Op = "signal.ice"    // ICE candidate
	OpPeers   Op = "room.peers"    // 服务器返回房间 peer 列表 (join成功后)
	OpJoinOK  Op = "room.join.ok"  // join 成功
	OpCreateOK Op = "room.create.ok"
	OpRehostOK Op = "room.rehost.ok"
	OpPing    Op = "room.ping"     // 保活探测 (移动端网络切换后确认信令仍在线)
	OpPong    Op = "room.pong"
	OpError   Op = "room.error"
	OpClose   Op = "peer.close"    // 通知被踢
)

// Envelope 信令层消息信封。
type Envelope struct {
	Type   Op              `json:"type"`
	Peer   string          `json:"peer,omitempty"`   // 发送方 peerID
	To     string          `json:"to,omitempty"`     // 定向 peerID (offer/answer/ice)
	Room   string          `json:"room,omitempty"`   // 房间码
	Data   json.RawMessage `json:"data,omitempty"`
	Err    *ErrorPayload   `json:"err,omitempty"`
	Seq    int64           `json:"seq,omitempty"`
}

// ErrorPayload 错误负载。
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// hmacSHA256 密码哈希辅助 (与 secret 包兼容, 但此处独立, 避免耦合).
func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// HexEncodeBytes helper.
func HexEncodeBytes(b []byte) string { return hex.EncodeToString(b) }
