package room

import (
	"context"
	"testing"
	"time"

	"majiang-server/internal/game"
)

// 阶段7: 房主迁移与断线恢复的故障注入测试。
//
// 覆盖需求里的验收项:
//   - 主动退房可迁移并继续(不出现双房主、不重复结算)
//   - 异常断线在宽限期后自动迁移
//   - 旧房主回归不会抢回任期(避免双主机)
//   - 对局中掉线座位转机器人托管, 重连后收回座位
//   - 每次迁移都留下可追溯的日志

func newMigrateRoom(t *testing.T, players int) (*Registry, *Room, []string) {
	t.Helper()
	reg := NewRegistry(3, time.Hour, nil)
	rules := game.DefaultRuleSet()
	rules.Players = players
	rm, err := reg.Create(context.Background(), CreateOptions{
		GuestID:  "host",
		Nickname: "房主",
		RuleSet:  rules,
		Theme:    game.ThemeClassic,
	})
	if err != nil {
		t.Fatalf("创建房间失败: %v", err)
	}
	ids := []string{"host"}
	for i := 2; i <= players; i++ {
		id := "p" + string(rune('1'+i-1))
		if _, err := reg.Join(context.Background(), JoinOptions{GuestID: id, Nickname: id, IDOrCode: rm.ID}); err != nil {
			t.Fatalf("加入房间失败(%s): %v", id, err)
		}
		ids = append(ids, id)
	}
	return reg, rm, ids
}

// 主动退房: 立即交棒(不等宽限期), 任期 +1, 留一条 host.leave 日志。
func TestHostMigratesImmediatelyOnLeave(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 3)
	if got := rm.HostEpoch(); got != 0 {
		t.Fatalf("新建房间任期应为 0, 实际 %d", got)
	}
	if err := reg.Leave(context.Background(), "host", rm.ID); err != nil {
		t.Fatalf("房主离开失败: %v", err)
	}
	if got := rm.HostEpoch(); got != 1 {
		t.Fatalf("房主离开后任期应为 1, 实际 %d", got)
	}
	if rm.IsHost("host") {
		t.Fatal("离开的房主不应仍然拥有房主权限")
	}
	// 座位号最小的在线真人接手。
	if !rm.IsHost("p2") {
		t.Fatal("房主应转交给座位号最小的真人 p2")
	}
	log := rm.Migrations()
	if len(log) != 1 || log[0].Kind != MigrateHostLeave || log[0].From != "host" || log[0].To != "p2" {
		t.Fatalf("迁移日志不符合预期: %+v", log)
	}
	// 任意时刻只有一个房主(不出现双主机)。
	hosts := 0
	for _, p := range rm.Players() {
		if p.GuestID == rm.HostGuestID {
			hosts++
		}
	}
	if hosts != 1 {
		t.Fatalf("应恰好有一个房主, 实际 %d", hosts)
	}
}

// 异常断线: 宽限期内不动, 超时后自动迁移。
func TestHostMigratesAfterDisconnectGrace(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 3)
	rm.hostGrace = 20 * time.Millisecond
	rm.SetConnected("host", false)

	// 宽限期内: 不迁移(否则一次刷新就会被抢走房主)。
	if reg.Tick(time.Now()); len(rm.Migrations()) != 0 {
		t.Fatal("宽限期内不应发生房主迁移")
	}
	if !rm.IsHost("host") {
		t.Fatal("宽限期内房主身份应保留")
	}

	time.Sleep(30 * time.Millisecond)
	changed := reg.Tick(time.Now())
	if len(changed) == 0 {
		t.Fatal("房主断线超时应被判定为房间发生变化(需要广播新状态)")
	}
	if got := rm.HostEpoch(); got != 1 {
		t.Fatalf("超时后任期应为 1, 实际 %d", got)
	}
	if !rm.IsHost("p2") {
		t.Fatal("超时后房主应转给在线真人 p2")
	}
	log := rm.Migrations()
	if len(log) != 1 || log[0].Kind != MigrateHostDisconnect {
		t.Fatalf("迁移日志应记录 host.disconnect: %+v", log)
	}
}

// 旧房主回归: 不抢回任期(否则会出现"双房主"的错觉, 权限来回抖动)。
func TestOldHostDoesNotRegainHost(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 3)
	rm.hostGrace = 10 * time.Millisecond
	rm.SetConnected("host", false)
	time.Sleep(20 * time.Millisecond)
	reg.Tick(time.Now())
	if !rm.IsHost("p2") {
		t.Fatal("房主应已转给 p2")
	}
	epoch := rm.HostEpoch()

	// 旧房主回来(重连握手会走 SetConnected(true))。
	rm.SetConnected("host", true)
	if rm.IsHost("host") {
		t.Fatal("旧房主回归后不应重新获得房主权限")
	}
	if got := rm.HostEpoch(); got != epoch {
		t.Fatalf("旧房主回归不应改变任期: 期望 %d, 实际 %d", epoch, got)
	}
	// 它仍然是房间成员, 座位与手牌照旧(只是没有房主权限)。
	if !rm.HasPlayer("host") {
		t.Fatal("回归的旧房主应仍在房间内")
	}
}

// 对局中掉线: 宽限期后座位转机器人托管, 牌局继续; 重连后收回座位。
func TestSeatReclaimedToBotDuringMatch(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 4)
	rm.seatGrace = 20 * time.Millisecond
	if err := rm.StartMatch(game.NewSeededSource(7)); err != nil {
		t.Fatalf("开局失败: %v", err)
	}
	rm.SetConnected("p3", false)

	// 宽限期内不托管。
	reg.Tick(time.Now())
	if se, ok := seatOf(rm, "p3"); !ok || playerIsBot(rm, "p3") {
		t.Fatalf("宽限期内不应转托管(seat=%d)", se)
	}

	time.Sleep(30 * time.Millisecond)
	reg.Tick(time.Now())
	if !playerIsBot(rm, "p3") {
		t.Fatal("超时后 p3 的座位应转为机器人托管")
	}
	// 座位不能消失: 引擎按固定座位组成手牌, 删座位会让牌局错位。
	if _, ok := seatOf(rm, "p3"); !ok {
		t.Fatal("托管座位不应被删除")
	}
	// 房主没有变(掉的不是房主)。
	if !rm.IsHost("host") {
		t.Fatal("掉的不是房主, 房主身份不应变化")
	}

	// 重连: 座位收回。
	rm.SetConnected("p3", true)
	if playerIsBot(rm, "p3") {
		t.Fatal("重连后应自动收回托管座位")
	}
}

// 房主在对局中掉线并被托管: 先托管, 再迁移房主, 顺序固定。
func TestHostSeatBotTriggersHostMigration(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 4)
	rm.seatGrace = 20 * time.Millisecond
	rm.hostGrace = 20 * time.Millisecond
	if err := rm.StartMatch(game.NewSeededSource(3)); err != nil {
		t.Fatalf("开局失败: %v", err)
	}
	rm.SetConnected("host", false)
	time.Sleep(30 * time.Millisecond)
	reg.Tick(time.Now())

	if !playerIsBot(rm, "host") {
		t.Fatal("房主座位应被托管")
	}
	if rm.IsHost("host") {
		t.Fatal("被托管的房主不应保留房主权限")
	}
	if !rm.IsHost("p2") {
		t.Fatal("房主应转给在线真人 p2")
	}
	log := rm.Migrations()
	if len(log) != 1 || log[0].Kind != MigrateHostSeatBot {
		t.Fatalf("迁移日志应记录 host.seat_bot: %+v", log)
	}
}

// 对局中主动离开: 座位转托管而不是删除(否则牌局会卡在该座位)。
func TestLeaveDuringMatchKeepsSeatAsBot(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 4)
	if err := rm.StartMatch(game.NewSeededSource(11)); err != nil {
		t.Fatalf("开局失败: %v", err)
	}
	if err := reg.Leave(context.Background(), "p3", rm.ID); err != nil {
		t.Fatalf("对局中离开失败: %v", err)
	}
	if !playerIsBot(rm, "p3") {
		t.Fatal("对局中离开的座位应转机器人托管")
	}
	if rm.HasPlayer("p3") {
		t.Fatal("主动离开的玩家不应再被 FindByGuest 找回(否则重连会强行塞回牌局)")
	}
	if _, ok := reg.FindByGuest("p3"); ok {
		t.Fatal("离开的玩家不应再被认作房间里的人")
	}
	// 其他人还能继续打: 托管座位在注册表 tick 后会自动行动。
	reg.Tick(time.Now())
	if rm.Status != StatusPlaying {
		t.Fatalf("牌局应继续(状态应为 playing), 实际 %v", rm.Status)
	}
}

// 一局结束后, 中途离开的座位必须让位, 新玩家才能进来(房间容量不被幽灵占住)。
func TestLeftSeatFreedAfterMatch(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 4)
	if err := rm.StartMatch(game.NewSeededSource(5)); err != nil {
		t.Fatalf("开局失败: %v", err)
	}
	if err := reg.Leave(context.Background(), "p4", rm.ID); err != nil {
		t.Fatalf("对局中离开失败: %v", err)
	}
	// 把对局直接判到结束(测试里不真打完一局): 用引擎的结果投影状态即可。
	rm.mu.Lock()
	rm.match = nil
	rm.Status = StatusFinished
	rm.mu.Unlock()

	if _, err := reg.Join(context.Background(), JoinOptions{GuestID: "p5", Nickname: "新人", IDOrCode: rm.ID}); err != nil {
		t.Fatalf("一局结束后新玩家应能补位: %v", err)
	}
	if _, ok := seatOf(rm, "p4"); ok {
		t.Fatal("离开的座位应在下一局开始前被清掉")
	}
}

// 一局结束后注册表 tick 就会让位(不必等下一次 join), 房间人数与座位立刻更新。
func TestRegistryTickPurgesLeftSeatAfterMatch(t *testing.T) {
	reg, rm, _ := newMigrateRoom(t, 4)
	if err := rm.StartMatch(game.NewSeededSource(9)); err != nil {
		t.Fatalf("开局失败: %v", err)
	}
	if err := reg.Leave(context.Background(), "p4", rm.ID); err != nil {
		t.Fatalf("对局中离开失败: %v", err)
	}
	// 对局还没结束: 座位必须保留(否则引擎座位错位、牌局卡死)。
	reg.Tick(time.Now())
	if _, ok := seatOf(rm, "p4"); !ok {
		t.Fatal("对局进行中不应清掉托管座位")
	}
	// 对局结束: 下一次 tick 就该让位。
	rm.mu.Lock()
	rm.match = nil
	rm.Status = StatusFinished
	rm.mu.Unlock()
	if changed := reg.Tick(time.Now()); len(changed) == 0 {
		t.Fatal("让位应被判定为房间变化(需要广播新名单)")
	}
	if _, ok := seatOf(rm, "p4"); ok {
		t.Fatal("一局结束后离开的座位应被清掉")
	}
}

func seatOf(rm *Room, guestID string) (int, bool) {
	for _, p := range rm.Players() {
		if p.GuestID == guestID {
			return p.Seat, true
		}
	}
	return 0, false
}

func playerIsBot(rm *Room, guestID string) bool {
	for _, p := range rm.Players() {
		if p.GuestID == guestID {
			return p.IsBot
		}
	}
	return false
}
