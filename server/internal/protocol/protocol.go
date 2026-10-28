// Package protocol 固定前后端通信协议: 统一信封(Envelope)、消息类型常量与视图模型。
// 所有 WS 消息均为 JSON 信封: {"v":1,"seq":N,"ref":"x","type":"...","payload":{...}}
// - seq: 房间事件单调递增序列号(仅服务端事件), 用于断线重连增量同步。
// - ref: 客户端请求引用(仅客户端上行), 服务端在错误事件中回显以便关联。
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version 协议版本。不兼容变更时递增。
const Version = 1

// Envelope 统一消息信封。
type Envelope struct {
	V       int             `json:"v"`           // 协议版本, 必须等于 Version
	Seq     int64           `json:"seq,omitempty"` // 服务端事件序列号(房间内单调递增)
	Ref     string          `json:"ref,omitempty"` // 客户端请求引用(错误事件回显)
	Type    string          `json:"type"`          // 消息类型
	Payload json.RawMessage `json:"payload,omitempty"`
}

// 客户端上行操作类型。
const (
	OpHello      = "guest.hello"  // 握手认证 {guestId, token}
	OpPing       = "ping"         // 保活
	OpResync     = "sync.resync"  // 断线重连请求增量事件 {lastSeq}
	OpRoomCreate = "room.create"  // 创建房间
	OpRoomJoin   = "room.join"    // 通过邀请码加入房间 {code}
	OpRoomLeave  = "room.leave"   // 离开房间
	OpGameStart  = "game.start"   // 房主开局
	OpGameAction = "game.action"  // 游戏动作(摸/打/吃碰杠胡/过)

	// 阶段5: 房间大厅、座位与邀请。
	OpRoomList     = "room.list"     // 拉取公开房间大厅
	OpRoomUpdate   = "room.update"   // 修改房间名/公开性/密码(仅大厅阶段, 仅房主)
	OpRoomDissolve = "room.dissolve" // 房主解散房间
	OpSeatReady    = "seat.ready"    // 本人准备/取消准备 {ready}
	OpSeatBot      = "seat.bot"      // 增删机器人 {seat, add}
	OpInviteCreate = "invite.create" // 创建邀请 {expiresInSec, maxUses, password, bypassRoomPassword}
	OpInviteList   = "invite.list"   // 列出本房间邀请(无令牌明文)
	OpInviteRevoke = "invite.revoke" // 撤销邀请 {id}
	OpInviteJoin   = "invite.join"   // 用邀请令牌加入 {token, password}
)

// 服务端下行事件类型。
const (
	EvHello     = "hello"      // 握手成功 {guestId, nickname}
	EvPong      = "pong"       // 保活回复
	EvRoomState = "room.state" // 房间可见状态投影(隐藏他人手牌)
	EvGameEvent = "game.event" // 游戏事件(含本人私有视图)
	EvError     = "error"      // 错误 {code, message}, 可回显 ref

	EvRoomList      = "room.list"      // 公开房间大厅快照
	EvInviteCreated = "invite.created" // 邀请创建成功(唯一一次返回令牌明文)
	EvInviteList    = "invite.list"    // 邀请列表
)

// HelloIn 握手请求。
type HelloIn struct {
	GuestID string `json:"guestId"`
	Token   string `json:"token"`
}

// HelloOut 握手响应。
type HelloOut struct {
	GuestID  string `json:"guestId"`
	Nickname string `json:"nickname"`
}

// ErrorOut 错误事件负载。
type ErrorOut struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResyncIn 重连同步请求。
type ResyncIn struct {
	LastSeq int64 `json:"lastSeq"`
}

// PlayerView 房间内其他玩家的可见投影: 永远不含手牌内容。
type PlayerView struct {
	GuestID   string `json:"guestId"`
	Nickname  string `json:"nickname"`
	Seat      int    `json:"seat"`
	Connected bool   `json:"connected"`
	TileCount int    `json:"tileCount"`        // 手牌数量(可见), 内容不可见
	Ready     bool   `json:"ready"`            // 准备状态(仅大厅阶段有意义)
	IsBot     bool   `json:"isBot"`            // 服务端 AI 座位
	IsHost    bool   `json:"isHost"`           // 房主
}

// YouView 当前观察者自己的私有视图: 仅发给该客户端, 含手牌。
type YouView struct {
	GuestID string   `json:"guestId"`
	Seat    int      `json:"seat"`
	Hand    []string `json:"hand,omitempty"` // 手牌代码列表(阶段2起填充)
}

// RoomStateView 房间状态投影: 权威状态机按座位裁剪后的可见部分。
type RoomStateView struct {
	RoomID       string       `json:"roomId"`
	Code         string       `json:"code"`
	Mode         string       `json:"mode"`
	Status       string       `json:"status"`
	Theme        string       `json:"theme"`
	Name         string       `json:"name"`
	IsPublic     bool         `json:"isPublic"`
	HasPassword  bool         `json:"hasPassword"`  // 只暴露"是否设密码", 永不回传哈希
	RulesLocked  bool         `json:"rulesLocked"`  // 开局后规则锁定
	Capacity     int          `json:"capacity"`     // 规则人数(2~4)
	Seq          int64        `json:"seq"`          // 当前最新事件序列号
	// HostEpoch 主机任期: 每次房主变更 +1(阶段7)。客户端据此判断
	// "谁是当前房主", 旧房主回归时按更小的 epoch 识别自己已不是房主。
	HostEpoch    int          `json:"hostEpoch"`
	Players      []PlayerView `json:"players"`
	You          *YouView     `json:"you,omitempty"` // 仅对请求者本人存在
	// Rules 房间规则快照(阶段10 起下发, 供房间界面展示计时/截牌设置)。
	Rules *RulesView `json:"rules,omitempty"`
}

// RulesView 规则的公开投影(不含牌集明细, 只给前端展示需要的项)。
type RulesView struct {
	Players        int  `json:"players"`
	Chow           bool `json:"chow"`
	SevenPairs     bool `json:"seven_pairs"`
	SelfDraw       bool `json:"self_draw"`
	DiscardWin     bool `json:"discard_win"`
	BaseScore      int  `json:"base_score"`
	RespondMeldSec int  `json:"respond_meld_sec"`
	RespondWinSec  int  `json:"respond_win_sec"`
	DiscardSec     int  `json:"discard_sec"`
	RobWin         bool `json:"rob_win"`
	RobMeld        bool `json:"rob_meld"`
	RobKongWin     bool `json:"rob_kong_win"`
	KongFlower     bool `json:"kong_flower"`
	LastTile       bool `json:"last_tile"`
}

// LobbyRoomView 公开房间大厅条目: 只暴露可公开的房间元数据。
type LobbyRoomView struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Theme        string `json:"theme"`
	Players      int    `json:"players"`
	Capacity     int    `json:"capacity"`
	HasPassword  bool   `json:"hasPassword"`
	HostNickname string `json:"hostNickname"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// InviteView 邀请的可见信息(不含令牌明文与密码哈希)。
type InviteView struct {
	ID                 string `json:"id"`
	HasPassword        bool   `json:"hasPassword"`
	BypassRoomPassword bool   `json:"bypassRoomPassword"`
	MaxUses            int    `json:"maxUses"`  // 0 = 不限
	Uses               int    `json:"uses"`
	Revoked            bool   `json:"revoked"`
	ExpiresAt          int64  `json:"expiresAt"` // Unix 秒, 0 = 不过期
	CreatedAt          int64  `json:"createdAt"`
}

// InviteCreatedView 邀请创建结果: token 明文仅此一次下发。
type InviteCreatedView struct {
	Token string     `json:"token"`
	Invite InviteView `json:"invite"`
}

// GameEventOut 对局事件负载: 公共事件描述 + 每座位私有视图。
// 传输层按座位拆发: 同一事件的 View 字段以接收者视角重新填充。
type GameEventOut struct {
	Kind  string          `json:"kind"`           // 引擎事件类型(dealt/draw/discard/...)
	Seat  int             `json:"seat,omitempty"` // 动作座位
	Tiles []string        `json:"tiles,omitempty"`
	View  json.RawMessage `json:"view,omitempty"` // 接收者视角的对局状态(传输层填充)
}

// Decode 解码并校验一条上行消息。
func Decode(data []byte, maxBytes int) (Envelope, error) {
	if maxBytes > 0 && len(data) > maxBytes {
		return Envelope{}, fmt.Errorf("消息过大: %d 字节(上限 %d)", len(data), maxBytes)
	}
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Envelope{}, fmt.Errorf("消息不是合法 JSON: %w", err)
	}
	if e.V != Version {
		return Envelope{}, fmt.Errorf("协议版本不支持: %d(当前 %d)", e.V, Version)
	}
	if e.Type == "" {
		return Envelope{}, errors.New("消息 type 不能为空")
	}
	return e, nil
}

// Encode 编码一条下行事件。
func Encode(e Envelope) ([]byte, error) {
	if e.V == 0 {
		e.V = Version
	}
	return json.Marshal(e)
}

// NewEvent 构造带序列号的服务端事件。
func NewEvent(seq int64, typ string, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("序列化事件负载 %s: %w", typ, err)
	}
	return Envelope{V: Version, Seq: seq, Type: typ, Payload: raw}, nil
}
