package room

import (
	"context"
	"errors"
	"testing"
	"time"

	"majiang-server/internal/game"
)

// newTestRoom 创建 4 人规则房间(房主已入座 0 号位)。
func newTestRoom(t *testing.T, password string, public bool) (*Registry, *Room) {
	t.Helper()
	r := NewRegistry(3, time.Hour, nil)
	rm, err := r.Create(context.Background(), CreateOptions{
		GuestID: "host", Nickname: "房主", Name: "测试房",
		Password: password, Public: public,
		RuleSet: game.DefaultRuleSet(), Theme: game.ThemeClassic,
	})
	if err != nil {
		t.Fatalf("创建房间: %v", err)
	}
	return r, rm
}

// joinAs 加入房间并断言成功。
func joinAs(t *testing.T, r *Registry, rm *Room, guest, nick string) {
	t.Helper()
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: guest, Nickname: nick, IDOrCode: rm.Code}); err != nil {
		t.Fatalf("%s 加入: %v", guest, err)
	}
}

func TestRoomPasswordRequired(t *testing.T) {
	r, rm := newTestRoom(t, "secret", true)

	// 无密码加入被拒绝, 且不得占用座位。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code}); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("无密码加入应报 ErrBadPassword, 实际 %v", err)
	}
	if rm.PlayerCount() != 1 {
		t.Fatalf("失败加入不得占座, 当前 %d 人", rm.PlayerCount())
	}
	// 错误密码同样拒绝。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code, Password: "nope"}); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("错误密码应报 ErrBadPassword, 实际 %v", err)
	}
	// 正确密码可加入。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code, Password: "secret"}); err != nil {
		t.Fatalf("正确密码应可加入: %v", err)
	}
	// 公开大厅只暴露"有密码"标记, 不含任何哈希。
	entries := r.PublicRooms()
	if len(entries) != 1 {
		t.Fatalf("公开房间数应为 1, 实际 %d", len(entries))
	}
	if !entries[0].HasPassword {
		t.Fatal("大厅条目应标记有密码")
	}
	if entries[0].Players != 2 || entries[0].Capacity != 4 {
		t.Fatalf("大厅人数/容量错误: %+v", entries[0])
	}
}

func TestRoomPasswordlessJoin(t *testing.T) {
	r, rm := newTestRoom(t, "", true)
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code}); err != nil {
		t.Fatalf("无密码房间应直接可加入: %v", err)
	}
	if r.PublicRooms()[0].HasPassword {
		t.Fatal("无密码房间不应标记密码")
	}
}

func TestPrivateRoomHiddenFromLobby(t *testing.T) {
	r, _ := newTestRoom(t, "", false)
	if n := len(r.PublicRooms()); n != 0 {
		t.Fatalf("私有房间不应出现在大厅, 实际 %d 条", n)
	}
}

func TestInviteLifecycle(t *testing.T) {
	r, rm := newTestRoom(t, "roompw", true)

	// 房主创建邀请(免房间密码 + 限 1 次)。
	token, view, err := r.CreateInvite(context.Background(), "host", rm.ID, CreateInviteOptions{
		MaxUses: 1, BypassRoomPassword: true,
	})
	if err != nil {
		t.Fatalf("创建邀请: %v", err)
	}
	if token == "" || view.ID == "" {
		t.Fatal("邀请令牌与 ID 均不应为空")
	}
	if view.Uses != 0 || view.MaxUses != 1 || !view.BypassRoomPassword {
		t.Fatalf("邀请视图错误: %+v", view)
	}

	// 非房主不能创建邀请。
	if _, _, err := r.CreateInvite(context.Background(), "p2", rm.ID, CreateInviteOptions{}); !errors.Is(err, ErrNotHost) {
		t.Fatalf("非房主创建邀请应报 ErrNotHost, 实际 %v", err)
	}

	// 用令牌加入: 无需房间密码。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code, InviteToken: token}); err != nil {
		t.Fatalf("凭邀请加入: %v", err)
	}

	// 次数用尽后同一令牌失效(不得回退到房间密码逻辑)。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p3", Nickname: "三号", IDOrCode: rm.Code, InviteToken: token}); !errors.Is(err, ErrInviteUsedUp) {
		t.Fatalf("令牌用尽应报 ErrInviteUsedUp, 实际 %v", err)
	}

	// 撤销后不可用。
	token2, view2, err := r.CreateInvite(context.Background(), "host", rm.ID, CreateInviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rm.RevokeInvite("host", view2.ID); err != nil {
		t.Fatalf("撤销邀请: %v", err)
	}
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p3", Nickname: "三号", IDOrCode: rm.Code, InviteToken: token2}); !errors.Is(err, ErrInviteRevoked) {
		t.Fatalf("已撤销令牌应报 ErrInviteRevoked, 实际 %v", err)
	}

	// 未知令牌。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p3", Nickname: "三号", IDOrCode: rm.Code, InviteToken: "bogus"}); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("未知令牌应报 ErrInviteNotFound, 实际 %v", err)
	}
}

func TestInviteExpiry(t *testing.T) {
	r, rm := newTestRoom(t, "", true)
	token, view, err := r.CreateInvite(context.Background(), "host", rm.ID, CreateInviteOptions{ExpiresInSec: 1})
	if err != nil {
		t.Fatal(err)
	}
	if view.ExpiresAt == 0 {
		t.Fatal("应记录过期时间")
	}
	// 直接把过期时间推到过去, 模拟时间流逝(避免 sleep)。
	rm.mu.Lock()
	for _, inv := range rm.invites {
		inv.ExpiresAt = time.Now().Add(-time.Second)
	}
	rm.mu.Unlock()
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code, InviteToken: token}); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("过期令牌应报 ErrInviteExpired, 实际 %v", err)
	}
}

func TestInviteWithoutBypassStillNeedsRoomPassword(t *testing.T) {
	r, rm := newTestRoom(t, "roompw", true)
	token, _, err := r.CreateInvite(context.Background(), "host", rm.ID, CreateInviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code, InviteToken: token}); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("邀请未声明免密时应要求房间密码, 实际 %v", err)
	}
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code, InviteToken: token, Password: "roompw"}); err != nil {
		t.Fatalf("带正确房间密码应可加入: %v", err)
	}
}

func TestReadyAndBots(t *testing.T) {
	r, rm := newTestRoom(t, "", true)
	joinAs(t, r, rm, "p2", "二号")

	if err := rm.SetReady("p2", true); err != nil {
		t.Fatalf("设置准备: %v", err)
	}
	view := rm.ViewFor("host")
	var readySeen bool
	for _, p := range view.Players {
		if p.GuestID == "p2" {
			readySeen = p.Ready
			if p.IsHost {
				t.Error("p2 不是房主")
			}
		}
	}
	if !readySeen {
		t.Fatal("房主应看到 p2 已准备")
	}

	// 机器人补位: 座位占用检查。
	if err := rm.SetBot("host", 3, true); err != nil {
		t.Fatalf("补入机器人: %v", err)
	}
	if err := rm.SetBot("host", 3, true); !errors.Is(err, ErrSeatOccupied) {
		t.Fatalf("重复占用座位应报 ErrSeatOccupied, 实际 %v", err)
	}
	if err := rm.SetBot("p2", 2, true); !errors.Is(err, ErrNotHost) {
		t.Fatalf("非房主补机器人应报 ErrNotHost, 实际 %v", err)
	}
	// 移除机器人。
	if err := rm.SetBot("host", 3, false); err != nil {
		t.Fatalf("移除机器人: %v", err)
	}
	if err := rm.SetBot("host", 3, false); !errors.Is(err, ErrSeatEmpty) {
		t.Fatalf("空座位移除应报 ErrSeatEmpty, 实际 %v", err)
	}
}

func TestSetConfigByNameAndPassword(t *testing.T) {
	r, rm := newTestRoom(t, "", true)

	name := "新名字"
	pw := "newpw"
	public := false
	if err := rm.SetConfig("host", &name, &pw, &public); err != nil {
		t.Fatalf("房主修改房间: %v", err)
	}
	view := rm.ViewFor("host")
	if view.Name != "新名字" || !view.HasPassword || view.IsPublic {
		t.Fatalf("房间配置未生效: %+v", view)
	}
	// 现在需要密码。
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code}); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("设置密码后无密码加入应被拒绝, 实际 %v", err)
	}
	// 清除密码。
	empty := ""
	if err := rm.SetConfig("host", nil, &empty, nil); err != nil {
		t.Fatalf("清除密码: %v", err)
	}
	if _, err := r.Join(context.Background(), JoinOptions{GuestID: "p2", Nickname: "二号", IDOrCode: rm.Code}); err != nil {
		t.Fatalf("清除密码后应可加入: %v", err)
	}
	// 非房主无权修改。
	if err := rm.SetConfig("p2", &name, nil, nil); !errors.Is(err, ErrNotHost) {
		t.Fatalf("非房主修改应报 ErrNotHost, 实际 %v", err)
	}
}

func TestLobbyLockedAfterStart(t *testing.T) {
	_, rm := newTestRoom(t, "", true)
	for i := 1; i <= 3; i++ {
		if err := rm.SetBot("host", i, true); err != nil {
			t.Fatal(err)
		}
	}
	readyAll(t, rm)
	if err := rm.StartMatch(game.NewSeededSource(7)); err != nil {
		t.Fatalf("满座开局: %v", err)
	}
	if err := rm.SetReady("host", true); !errors.Is(err, ErrRulesLocked) {
		t.Fatalf("开局后准备应报 ErrRulesLocked, 实际 %v", err)
	}
	name := "改名"
	if err := rm.SetConfig("host", &name, nil, nil); !errors.Is(err, ErrRulesLocked) {
		t.Fatalf("开局后改配置应报 ErrRulesLocked, 实际 %v", err)
	}
	if err := rm.SetBot("host", 1, false); !errors.Is(err, ErrRulesLocked) {
		t.Fatalf("开局后增删机器人应报 ErrRulesLocked, 实际 %v", err)
	}
}

func TestDissolveRemovesRoom(t *testing.T) {
	r, rm := newTestRoom(t, "", true)
	joinAs(t, r, rm, "p2", "二号")

	if err := r.Dissolve(context.Background(), "p2", rm.ID); !errors.Is(err, ErrNotHost) {
		t.Fatalf("非房主解散应报 ErrNotHost, 实际 %v", err)
	}
	if err := r.Dissolve(context.Background(), "host", rm.ID); err != nil {
		t.Fatalf("房主解散: %v", err)
	}
	if _, ok := r.Get(rm.ID); ok {
		t.Fatal("解散后房间应被移除")
	}
	if _, ok := r.Get(rm.Code); ok {
		t.Fatal("解散后邀请码索引也应移除")
	}
	if n := len(r.PublicRooms()); n != 0 {
		t.Fatalf("解散后大厅应为空, 实际 %d", n)
	}
}

func TestRoomNameValidation(t *testing.T) {
	r := NewRegistry(3, time.Hour, nil)
	long := ""
	for i := 0; i < MaxRoomName+1; i++ {
		long += "长"
	}
	if _, err := r.Create(context.Background(), CreateOptions{
		GuestID: "h", Nickname: "房主", Name: long,
		RuleSet: game.DefaultRuleSet(), Theme: game.ThemeClassic,
	}); !errors.Is(err, ErrBadRoomName) {
		t.Fatalf("超长房间名应报 ErrBadRoomName, 实际 %v", err)
	}
	// 空名使用默认名而非报错。
	rm, err := r.Create(context.Background(), CreateOptions{
		GuestID: "h2", Nickname: "房主2", RuleSet: game.DefaultRuleSet(), Theme: game.ThemeClassic,
	})
	if err != nil {
		t.Fatalf("空名创建: %v", err)
	}
	if rm.ViewFor("h2").Name == "" {
		t.Fatal("空名应填默认名")
	}
}
