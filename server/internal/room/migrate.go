package room

// 阶段7: 房主迁移与断线恢复(服务器权威房间)。
//
// 设计要点:
//   - 房主在在线模式下只是"开局/改设置/解散"的权限, 对局状态始终由服务端
//     持有, 所以房主迁移不需要搬运任何隐藏信息, 天然满足"不泄漏他人手牌"。
//   - 主机任期(hostEpoch)每次房主变更 +1, 单调递增。客户端据此识别
//     "谁是当前房主", 旧房主回归时不会出现双房主(它拿到的 epoch 更旧)。
//   - 迁移分三类触发: 主动离开(立即)、断线超过 HostReconnectGrace(自动)、
//     座位被回收为托管时顺带迁移。
//   - 对局中掉线的玩家超过 SeatReclaimGrace 后座位转为机器人托管:
//     牌局照常推进(不再需要等人), 玩家重连后自动取回自己的座位。
//   - 每次迁移都写一条 MigrationEntry, 供测试与线上排查使用。

import (
	"time"

	"majiang-server/internal/game"
	"majiang-server/internal/protocol"
)

// HostReconnectGrace 房主断线后等待其重连的时长; 超时即自动迁移房主。
// 取值原则: 短于普通玩家的座位回收宽限期(等于让房主更早让位), 但足够覆盖
// 一次页面刷新/切后台。
const HostReconnectGrace = 15 * time.Second

// SeatReclaimGrace 对局中掉线玩家转为机器人托管的宽限期。
// 超过这个时长座位不再等人: 座位仍属于该游客, 但由机器人代打,
// 重连后立即收回。
const SeatReclaimGrace = 60 * time.Second

// 迁移日志的种类。
const (
	MigrateHostLeave      = "host.leave"      // 房主主动离开
	MigrateHostDisconnect = "host.disconnect" // 房主断线超时
	MigrateHostReap       = "host.reap"       // 房主座位被回收(大厅/已结束)
	MigrateHostSeatBot    = "host.seat_bot"   // 房主座位转托管
)

// MigrationEntry 一次房主迁移记录。
type MigrationEntry struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	Epoch int       `json:"epoch"`
	From  string    `json:"from"`
	To    string    `json:"to"`
}

// migrationLogLimit 迁移日志保留条数(单房间)。
const migrationLogLimit = 32

// HostEpoch 当前主机任期(0 表示房间创建后尚未发生迁移)。
func (r *Room) HostEpoch() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hostEpoch
}

// Migrations 返回迁移日志副本(时间正序)。
func (r *Room) Migrations() []MigrationEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MigrationEntry, len(r.migrations))
	copy(out, r.migrations)
	return out
}

// hostGraceLocked 房主断线宽限期(测试可覆盖)。
func (r *Room) hostGraceLocked() time.Duration {
	if r.hostGrace > 0 {
		return r.hostGrace
	}
	return HostReconnectGrace
}

// seatGraceLocked 对局中掉线座位转托管的宽限期(测试可覆盖)。
func (r *Room) seatGraceLocked() time.Duration {
	if r.seatGrace > 0 {
		return r.seatGrace
	}
	return SeatReclaimGrace
}

// electHostLocked 选出新房主: 优先"在线真人"里座位号最小者;
// 若没有在线的, 退化为任意真人(现在就给它权限, 它回来即可用)。
// 没有真人时返回 nil, 房主置空(让注册表回收)。
func (r *Room) electHostLocked() *Player {
	var online, anyHuman *Player
	for _, p := range r.players {
		if p.IsBot {
			continue
		}
		if anyHuman == nil || p.Seat < anyHuman.Seat {
			anyHuman = p
		}
		if !p.Connected {
			continue
		}
		if online == nil || p.Seat < online.Seat {
			online = p
		}
	}
	if online != nil {
		return online
	}
	return anyHuman
}

// promoteHostLocked 迁移房主并记录任期与日志(调用方持锁)。
// 与旧实现的关键差别: 任期 +1 且写迁移日志, 断线自动迁移与主动离开共用同一出口,
// 保证任何时刻只有一个房主、且每次变更都可追溯。
func (r *Room) promoteHostLocked(kind string) bool {
	prev := r.HostGuestID
	next := r.electHostLocked()
	nextID := ""
	if next != nil {
		nextID = next.GuestID
	}
	// 没有变化时不写任期: 避免"每次扫描都 +1"把客户端的水位搅乱。
	if nextID == prev {
		return false
	}
	r.hostEpoch++
	r.HostGuestID = nextID
	r.migrations = append(r.migrations, MigrationEntry{
		At:    time.Now(),
		Kind:  kind,
		Epoch: r.hostEpoch,
		From:  prev,
		To:    nextID,
	})
	if len(r.migrations) > migrationLogLimit {
		r.migrations = append([]MigrationEntry(nil), r.migrations[len(r.migrations)-migrationLogLimit:]...)
	}
	return true
}

// tickHostLocked 房主断线超时则自动迁移(调用方持锁)。
// 只在房主确实不在线(或已不存在)时动作, 因此正常在线房间不会被打扰。
func (r *Room) tickHostLocked(now time.Time) bool {
	host, ok := r.players[r.HostGuestID]
	if r.HostGuestID == "" || !ok {
		return r.promoteHostLocked(MigrateHostDisconnect)
	}
	if host.Connected || host.disconnectedAt.IsZero() {
		return false
	}
	if now.Sub(host.disconnectedAt) < r.hostGraceLocked() {
		return false
	}
	return r.promoteHostLocked(MigrateHostDisconnect)
}

// TickHost 供注册表周期调用: 房主断线超时自动迁移。返回是否发生变化。
func (r *Room) TickHost(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.HostGuestID == "" {
		return false
	}
	if !r.tickHostLocked(now) {
		return false
	}
	r.lastActivity = now
	r.appendEventLocked(protocol.EvRoomState, nil)
	return true
}

// ReclaimSeats 供注册表周期调用: 对局中掉线超时的座位转机器人托管。
func (r *Room) ReclaimSeats(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.reclaimSeatsLocked(now) {
		return false
	}
	r.lastActivity = now
	r.appendEventLocked(protocol.EvRoomState, nil)
	return true
}

// reclaimSeatsLocked 把对局中长时间掉线的真人座位转为机器人托管(调用方持锁)。
//
// 为什么不是直接删座位: 对局由固定数量的座位组成, 删掉一个会让引擎的座位映射
// 错位。转为 IsBot 后: 牌局照常推进; 该游客重连时 SetConnected 会把座位收回来。
func (r *Room) reclaimSeatsLocked(now time.Time) bool {
	if r.match == nil || r.match.Phase() == game.PhaseFinished {
		return false
	}
	changed := false
	for _, p := range r.players {
		if p.IsBot || p.Connected || p.disconnectedAt.IsZero() {
			continue
		}
		if now.Sub(p.disconnectedAt) < r.seatGraceLocked() {
			continue
		}
		p.IsBot = true
		changed = true
		if p.GuestID == r.HostGuestID {
			if r.promoteHostLocked(MigrateHostSeatBot) {
				r.appendEventLocked(protocol.EvRoomState, nil)
			}
		}
	}
	return changed
}
