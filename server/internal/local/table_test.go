package local

import (
	"encoding/json"
	"testing"

	"majiang-server/internal/game"
)

// seats 构造 n 个座位, 前 aiCount 个是 AI(普通难度), 其余为真人。
func seats(n, aiCount int) []SeatSpec {
	out := make([]SeatSpec, n)
	for i := range out {
		out[i] = SeatSpec{Name: "P" + string(rune('1'+i))}
		if i < aiCount {
			out[i].AI = true
			out[i].Difficulty = "normal"
		}
	}
	return out
}

func newTable(t *testing.T, n, aiCount int, seed int64) *Table {
	t.Helper()
	rules := game.DefaultRuleSet()
	rules.Players = n
	tb, err := New(Config{Rules: rules, Dealer: 0, Seed: seed, Seats: seats(n, aiCount)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tb
}

// totalTiles 统计场上全部物理牌数(牌墙 + 各家手牌 + 副露 + 河牌)。
// 任何时刻都必须等于 136, 否则说明动作实现破坏了牌数守恒。
func totalTiles(t *testing.T, tb *Table) int {
	t.Helper()
	n := tb.SeatCount()
	views := make([]game.SeatView, n)
	for i := 0; i < n; i++ {
		v, err := tb.ViewFor(i)
		if err != nil {
			t.Fatalf("ViewFor(%d): %v", i, err)
		}
		views[i] = v
	}
	total := tb.WallLeft()
	for i := 0; i < n; i++ {
		total += len(views[i].Hand)
		for _, m := range views[i].Melds {
			total += len(m.Tiles)
		}
	}
	for _, river := range views[0].Discards {
		total += len(river)
	}
	return total
}

// humanPolicy 模拟一位只做基础操作的真人: 能胡就胡, 否则摸/打第一张/过。
func humanPolicy(v game.SeatView) (string, map[string]any) {
	if hasAction(v.Actions, "hu.self") {
		return "hu.self", nil
	}
	if hasAction(v.Actions, "hu.robbing") {
		return "hu.robbing", nil
	}
	if hasAction(v.Actions, "draw") {
		return "draw", map[string]any{}
	}
	if hasAction(v.Actions, "discard") && len(v.Hand) > 0 {
		return "discard", map[string]any{"tile": v.Hand[0]}
	}
	if hasAction(v.Actions, "pass") {
		return "pass", nil
	}
	return "", nil
}

func TestNewValidation(t *testing.T) {
	rules := game.DefaultRuleSet()
	cases := []struct {
		name string
		cfg  Config
	}{
		{"座位数与人数不符", Config{Rules: rules, Seats: seats(3, 3)}},
		{"座位过多", Config{Rules: rules, Seats: seats(5, 5)}},
		{"庄家越界", Config{Rules: rules, Dealer: 4, Seats: seats(4, 4)}},
		{"庄家负值", Config{Rules: rules, Dealer: -1, Seats: seats(4, 4)}},
		{"未知难度", Config{Rules: rules, Seats: []SeatSpec{
			{Name: "a", AI: true, Difficulty: "nightmare"}, {Name: "b"}, {Name: "c"}, {Name: "d"},
		}}},
	}
	for _, c := range cases {
		if _, err := New(c.cfg); err == nil {
			t.Errorf("%s: 期望报错, 实际通过", c.name)
		}
	}
	// 冲突规则必须拒绝。
	bad := game.DefaultRuleSet()
	bad.SelfDraw = false
	bad.DiscardWin = false
	if _, err := New(Config{Rules: bad, Seats: seats(4, 4)}); err == nil {
		t.Error("冲突规则(自摸/点炮全关): 期望报错")
	}
}

func TestPrivacyAcrossSeats(t *testing.T) {
	tb := newTable(t, 4, 3, 7)
	for seat := 0; seat < 4; seat++ {
		v, err := tb.ViewFor(seat)
		if err != nil {
			t.Fatalf("ViewFor(%d): %v", seat, err)
		}
		if v.Seat != game.Seat(seat) {
			t.Errorf("座位 %d: 投影座位号 %d", seat, v.Seat)
		}
		if len(v.Discards) != 4 {
			t.Errorf("座位 %d: 河牌应为 4 组, 实际 %d", seat, len(v.Discards))
		}
		if len(v.OthersHandCounts) != 4 {
			t.Errorf("座位 %d: 他人手牌数应有 4 项", seat)
		}
		// 他人手牌只暴露数量, 自己手牌才是牌码。
		if len(v.Hand) != int(v.OthersHandCounts[seat]) {
			t.Errorf("座位 %d: 手牌张数 %d 与自身计数 %d 不一致", seat, len(v.Hand), v.OthersHandCounts[seat])
		}
	}
	v0, _ := tb.ViewFor(0)
	v1, _ := tb.ViewFor(1)
	if len(v0.Hand) == 0 || len(v1.Hand) == 0 {
		t.Fatal("开局手牌不应为空")
	}
	if v0.Hand[0] == v1.Hand[0] && len(v0.Hand) == len(v1.Hand) {
		// 可能巧合, 但整手完全相同时说明投影串了。
		same := true
		for i := range v0.Hand {
			if v0.Hand[i] != v1.Hand[i] {
				same = false
				break
			}
		}
		if same {
			t.Error("座位 0 与 1 的手牌完全相同, 疑似投影错误")
		}
	}
}

func TestAllAIMatchTerminates(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		tb := newTable(t, 4, 4, seed)
		steps := 0
		for !tb.Finished() {
			steps++
			if steps > 3000 {
				t.Fatalf("seed=%d: 超过 3000 步未终局", seed)
			}
			if _, acted, err := tb.StepAI(); err != nil {
				t.Fatalf("seed=%d 第 %d 步: %v", seed, steps, err)
			} else if !acted {
				t.Fatalf("seed=%d 第 %d 步: 全 AI 对局却无人可行动(死锁)", seed, steps)
			}
			if tt := totalTiles(t, tb); tt != 136 {
				t.Fatalf("seed=%d 第 %d 步: 牌数 %d, 期望 136", seed, steps, tt)
			}
		}
		v, _ := tb.ViewFor(0)
		if v.Result == nil {
			t.Fatalf("seed=%d: 终局缺少结算视图", seed)
		}
		sum := 0
		for _, s := range v.Result.Scores {
			sum += s
		}
		if sum != 0 {
			t.Errorf("seed=%d: 得失分不守恒, 合计 %d", seed, sum)
		}
		// 事件序号必须严格单调。
		evs := tb.EventsSince(0)
		if len(evs) == 0 {
			t.Fatalf("seed=%d: 没有事件", seed)
		}
		for i := 1; i < len(evs); i++ {
			if evs[i].Seq <= evs[i-1].Seq {
				t.Fatalf("seed=%d: 事件序号非单调", seed)
			}
		}
	}
}

func TestHumanPlusAI(t *testing.T) {
	tb := newTable(t, 4, 3, 42)
	human := 3 // 真人在末座, 覆盖非庄家起手
	steps := 0
	for !tb.Finished() {
		steps++
		if steps > 4000 {
			t.Fatal("超过 4000 步未终局")
		}
		if seat, ok := tb.PendingAI(); ok {
			if seat == human {
				t.Fatal("PendingAI 不应返回真人座位")
			}
			if _, acted, err := tb.StepAI(); err != nil || !acted {
				t.Fatalf("AI 行动失败: %v", err)
			}
			continue
		}
		v, err := tb.ViewFor(human)
		if err != nil {
			t.Fatal(err)
		}
		action, payload := humanPolicy(v)
		if action == "" {
			t.Fatalf("真人无合法动作且未终局: %+v", v)
		}
		b, _ := json.Marshal(payload)
		if err := tb.Apply(human, action, b); err != nil {
			t.Fatalf("真执行 %s 失败: %v", action, err)
		}
		if tt := totalTiles(t, tb); tt != 136 {
			t.Fatalf("第 %d 步: 牌数 %d, 期望 136", steps, tt)
		}
	}
	if _, ok := tb.PendingAI(); ok {
		t.Error("终局后不应再有待行动 AI")
	}
	v, _ := tb.ViewFor(human)
	if v.Phase != string(game.PhaseFinished) {
		t.Errorf("终局阶段应为 finished, 实际 %s", v.Phase)
	}
}

func TestIllegalActionRejected(t *testing.T) {
	tb := newTable(t, 4, 3, 3)
	// 非当前行动者不能摸牌。
	if err := tb.Apply(2, "draw", []byte("{}")); err == nil {
		t.Error("非行动者摸牌: 期望报错")
	}
	// 未知动作。
	v, _ := tb.ViewFor(0)
	if len(v.Actions) == 0 {
		t.Fatal("庄家开局应有动作")
	}
	if err := tb.Apply(0, "teleport", []byte("{}")); err == nil {
		t.Error("未知动作: 期望报错")
	}
	// 座位越界。
	if err := tb.Apply(9, "draw", []byte("{}")); err == nil {
		t.Error("座位越界: 期望报错")
	}
	if _, err := tb.ViewFor(9); err == nil {
		t.Error("座位越界视图: 期望报错")
	}
}

func TestApplyRejectsBadPayload(t *testing.T) {
	// 庄家开局即处于打牌阶段(已摸第 14 张), 无需再摸。
	tb := newTable(t, 4, 3, 5)
	if err := tb.Apply(0, "discard", []byte(`{"tile":"zzz"}`)); err == nil {
		t.Error("非法牌代码: 期望报错")
	}
	if err := tb.Apply(0, "chow", []byte(`{"with":[]}`)); err == nil {
		t.Error("chow 张数不足: 期望报错")
	}
}

func TestStepAIHumanOnly(t *testing.T) {
	// 4 个真人座位: 任何时刻都不应有 AI 可行动。
	tb := newTable(t, 4, 0, 9)
	if _, ok := tb.PendingAI(); ok {
		t.Fatal("全真人对局不应有 PendingAI")
	}
	if _, acted, err := tb.StepAI(); err != nil || acted {
		t.Errorf("全真人对局 StepAI 应无动作, acted=%v err=%v", acted, err)
	}
}

func TestEventsSinceWindow(t *testing.T) {
	tb := newTable(t, 4, 4, 11)
	_, _, _ = tb.StepAI()
	seq := tb.Seq()
	if seq == 0 {
		t.Fatal("至少应有一条事件")
	}
	if got := tb.EventsSince(seq); len(got) != 0 {
		t.Errorf("EventsSince(当前水位) 应为空, 实际 %d 条", len(got))
	}
	if got := tb.EventsSince(0); len(got) != seq {
		t.Errorf("EventsSince(0) 应为 %d 条, 实际 %d", seq, len(got))
	}
}
