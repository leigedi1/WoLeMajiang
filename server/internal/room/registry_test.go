package room

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"majiang-server/internal/game"
	"majiang-server/internal/protocol"
)

func mustCreate(t *testing.T, r *Registry, host, nick string) *Room {
	t.Helper()
	room, err := r.Create(context.Background(), CreateOptions{GuestID: host, Nickname: nick, RuleSet: game.DefaultRuleSet(), Theme: game.ThemeClassic})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return room
}

func TestCreateRoomBasics(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	room := mustCreate(t, r, "g1", "房主")
	if len(room.ID) != 32 {
		t.Errorf("房间 ID 应 32 字符, 实际 %d", len(room.ID))
	}
	if len(room.Code) != 6 {
		t.Errorf("邀请码应 6 字符, 实际 %q", room.Code)
	}
	for _, c := range room.Code {
		if !strings.ContainsRune(roomAlphabet, c) {
			t.Errorf("邀请码含非法字符 %q", c)
		}
	}
	if room.Mode != ModeOnline || room.Status != StatusLobby {
		t.Errorf("初始状态错误: %s/%s", room.Mode, room.Status)
	}
	if room.PlayerCount() != 1 {
		t.Fatalf("房主应已入座, 玩家数 %d", room.PlayerCount())
	}
	players := room.Players()
	if players[0].Seat != 0 || players[0].GuestID != "g1" {
		t.Errorf("房主应在 0 号位: %+v", players[0])
	}
	if room.CurrentSeq() < 1 {
		t.Error("创建后应至少有 1 个事件")
	}
}

func TestCreateRoomUniqueCodes(t *testing.T) {
	r := NewRegistry(50, time.Hour, nil)
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		room := mustCreate(t, r, "g", "玩家")
		if seen[room.Code] {
			t.Fatalf("邀请码重复: %s", room.Code)
		}
		seen[room.Code] = true
	}
}

func allRooms(t *testing.T, r *Registry) []*Room {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Room, 0, len(r.rooms))
	for _, rm := range r.rooms {
		out = append(out, rm)
	}
	return out
}

func TestMaxOnlineRoomsCap(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	for i := 0; i < 3; i++ {
		mustCreate(t, r, string(rune('a'+i)), "玩家")
	}
	if _, err := r.Create(context.Background(), CreateOptions{GuestID: "g9", Nickname: "第九人", RuleSet: game.DefaultRuleSet(), Theme: game.ThemeClassic}); !errors.Is(err, ErrMaxOnlineRooms) {
		t.Fatalf("超出上限应返回 ErrMaxOnlineRooms, 实际 %v", err)
	}
	// 清空一间后可再建。
	rooms := allRooms(t, r)
	if len(rooms) != 3 {
		t.Fatalf("应恰有 3 个房间, 实际 %d", len(rooms))
	}
	if err := r.Leave(context.Background(), rooms[0].HostGuestID, rooms[0].ID); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if r.Count() != 2 {
		t.Fatalf("空房应立即回收, 剩余 %d", r.Count())
	}
	mustCreate(t, r, "g9", "第九人")
	if r.Count() != 3 {
		t.Fatalf("清理后应可再建, 当前 %d", r.Count())
	}
}

func TestJoinByCodeAndErrors(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	room := mustCreate(t, r, "host", "房主")
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: room.Code}); err != nil {
		t.Fatalf("按邀请码加入: %v", err)
	}
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: room.Code}); !errors.Is(err, ErrAlreadyInRoom) {
		t.Fatalf("重复加入应报 ErrAlreadyInRoom, 实际 %v", err)
	}
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "pX", Nickname: "路人", IDOrCode: "ZZZZZZ"}); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("未知邀请码应报 ErrRoomNotFound, 实际 %v", err)
	}
	// 填满房间。
	for i := 3; i <= 4; i++ {
		if _, err := r.Join(context.Background(), JoinOptions{GuestID: string(rune('0' + i)), Nickname: "玩家", IDOrCode: room.ID}); err != nil {
			t.Fatalf("第 %d 名玩家加入: %v", i, err)
		}
	}
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p5", Nickname: "第五人", IDOrCode: room.ID}); !errors.Is(err, ErrRoomFull) {
		t.Fatalf("第五人应报 ErrRoomFull, 实际 %v", err)
	}
	players := room.Players()
	if len(players) != 4 {
		t.Fatalf("应有 4 名玩家, 实际 %d", len(players))
	}
	for i, p := range players {
		if p.Seat != i {
			t.Errorf("玩家应按入座顺序占 0..3, 第 %d 名座位 %d", i, p.Seat)
		}
	}
}

func TestLeaveReleasesSeat(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	room := mustCreate(t, r, "h", "房主")
	r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: room.ID})
	r.Join(context.Background(), JoinOptions{GuestID: "p3", Nickname: "三号", IDOrCode: room.ID})
	if err := r.Leave(context.Background(), "p2", room.ID); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	// 座位 1 释放, 新玩家应复用最小空位。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p4", Nickname: "四号", IDOrCode: room.ID}); err != nil {
		t.Fatalf("重新加入: %v", err)
	}
	players := room.Players()
	if players[1].GuestID != "p4" {
		t.Errorf("座位 1 应由 p4 占据: %+v", players[1])
	}
	if err := r.Leave(context.Background(), "ghost", room.ID); !errors.Is(err, ErrNotInRoom) {
		t.Fatalf("不在房内应报 ErrNotInRoom, 实际 %v", err)
	}
}

func TestEventSeqMonotonicAndIncremental(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	room := mustCreate(t, r, "h", "房主")
	r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: room.ID})
	seqs := []int64{}
	for _, e := range room.EventsSince(0) {
		seqs = append(seqs, e.Seq)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Fatalf("序列号应严格连续递增: %v", seqs)
		}
	}
	// 增量: 只取第一项之后的全部事件。
	half := seqs[0]
	tail := room.EventsSince(half)
	if len(tail) != len(seqs)-1 {
		t.Fatalf("增量事件数量错误: 期望 %d, 实际 %d (%v)", len(seqs)-1, len(tail), tail)
	}
	if tail[0].Seq != half+1 {
		t.Fatalf("增量事件应从 %d 之后开始, 实际 %d", half, tail[0].Seq)
	}
	// 事件负载可解析回房间状态视图。
	var view protocol.RoomStateView
	if err := json.Unmarshal(tail[len(tail)-1].Payload, &view); err != nil {
		t.Fatalf("事件负载应为 RoomStateView: %v", err)
	}
}

func TestViewHidesOtherHands(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	room := mustCreate(t, r, "h", "房主")
	r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: room.ID})
	view := room.ViewFor("p2")
	if view.You == nil || view.You.GuestID != "p2" || view.You.Seat != 1 {
		t.Fatalf("You 视图错误: %+v", view.You)
	}
	if view.You.Hand != nil {
		t.Errorf("大厅阶段手牌应为空, 实际 %v", view.You.Hand)
	}
	for _, p := range view.Players {
		raw, _ := json.Marshal(p)
		if strings.Contains(string(raw), "hand") {
			t.Errorf("其他玩家投影不得含 hand 字段: %s", raw)
		}
		if p.TileCount != 0 {
			t.Errorf("大厅阶段手牌数量应为 0, 实际 %d", p.TileCount)
		}
	}
	// 未在房间内的观察者没有 You。
	anon := room.ViewFor("stranger")
	if anon.You != nil {
		t.Error("非房间成员不应有 You 视图")
	}
}

func TestSweepIdleRooms(t *testing.T) {
	r := NewRegistry(3, 30*time.Minute, nil)
	room := mustCreate(t, r, "h", "房主")
	now := time.Now()
	// 未超时不清扫。
	if got := r.Sweep(context.Background(), now.Add(10*time.Minute)); len(got) != 0 {
		t.Fatalf("未超时不应清扫, 实际 %v", got)
	}
	// 超时后清扫。
	removed := r.Sweep(context.Background(), now.Add(31*time.Minute))
	if len(removed) != 1 || removed[0] != room.ID {
		t.Fatalf("应清扫超时房间, 实际 %v", removed)
	}
	if r.Count() != 0 {
		t.Fatalf("清扫后房间数应为 0, 实际 %d", r.Count())
	}
	if _, ok := r.Get(room.Code); ok {
		t.Error("邀请码索引也应清理")
	}
}

type fakePersister struct {
	created []RoomRecord
	deleted []string
	invites []InviteRecord
}

func (f *fakePersister) CreateRoom(ctx context.Context, rec RoomRecord) error {
	f.created = append(f.created, rec)
	return nil
}
func (f *fakePersister) DeleteRoom(ctx context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakePersister) UpdateRoomActivity(ctx context.Context, id, status string) error {
	return nil
}
func (f *fakePersister) CreateInvite(ctx context.Context, rec InviteRecord) error {
	f.invites = append(f.invites, rec)
	return nil
}
func (f *fakePersister) RevokeInvite(ctx context.Context, id string) error { return nil }
func (f *fakePersister) DeleteInvitesByRoom(ctx context.Context, roomID string) error {
	return nil
}

func TestPersisterWiring(t *testing.T) {
	fp := &fakePersister{}
	r := NewRegistry(3, time.Minute, fp)
	room := mustCreate(t, r, "h", "房主")
	if len(fp.created) != 1 || fp.created[0].ID != room.ID {
		t.Fatalf("创建应持久化: %+v", fp.created)
	}
	if fp.created[0].Code != room.Code || fp.created[0].Mode != "online" {
		t.Errorf("持久化字段错误: %+v", fp.created[0])
	}
	r.Sweep(context.Background(), time.Now().Add(2*time.Minute))
	if len(fp.deleted) != 1 || fp.deleted[0] != room.ID {
		t.Fatalf("清扫应删除持久化记录: %v", fp.deleted)
	}
}

func TestFindByGuest(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	room := mustCreate(t, r, "h", "房主")
	r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: room.ID})
	r.Join(context.Background(), JoinOptions{GuestID: "p3", Nickname: "三号", IDOrCode: room.Code})

	if got, ok := r.FindByGuest("p2"); !ok || got.ID != room.ID {
		t.Fatalf("应按游客找到房间: %v %v", ok, got)
	}
	if _, ok := r.FindByGuest("stranger"); ok {
		t.Fatal("不在任何房间的游客不应有结果")
	}
	// 离开后不再可查。
	r.Leave(context.Background(), "p2", room.ID)
	if _, ok := r.FindByGuest("p2"); ok {
		t.Fatal("离房后不应再找到")
	}
}

func TestCreateValidatesRuleAndTheme(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	if _, err := r.Create(context.Background(), CreateOptions{GuestID: "h", Nickname: "房主", RuleSet: game.RuleSet{Players: 5}, Theme: game.ThemeClassic}); err == nil {
		t.Fatal("players=5 应被拒绝")
	}
	if _, err := r.Create(context.Background(), CreateOptions{GuestID: "h", Nickname: "房主", RuleSet: game.DefaultRuleSet(), Theme: game.Theme("gold")}); err == nil {
		t.Fatal("非法主题应被拒绝")
	}
}
