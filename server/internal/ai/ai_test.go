package ai

import (
	"math/rand"
	"testing"

	"majiang-server/internal/game"
)

// seatView 构造一个用于决策的最小视图。
func seatView(hand []string, drawn string, actions []string) game.SeatView {
	return game.SeatView{
		Seat:             0,
		Phase:            "playing",
		TurnSeat:         0,
		TurnStage:        "discard",
		WallLeft:         60,
		Hand:             hand,
		Drawn:            drawn,
		Actions:          actions,
		Discards:         [][]string{{}, {}, {}, {}},
		OthersHandCounts: []int{13, 13, 13, 13},
		OthersMelds:      [][]game.MeldView{{}, {}, {}, {}},
	}
}

func rules4() game.RuleSet { return game.RuleSet{Players: 4, SevenPairs: true} }

func TestParseDifficulty(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Difficulty
		bad  bool
	}{
		{"", Normal, false},
		{"easy", Easy, false},
		{"normal", Normal, false},
		{"hard", Hard, false},
		{"nightmare", "", true},
	} {
		got, err := ParseDifficulty(tc.in)
		if tc.bad {
			if err == nil {
				t.Fatalf("%q 应被拒绝", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 解析失败: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q 期望 %q 实际 %q", tc.in, tc.want, got)
		}
		if !got.Valid() {
			t.Fatalf("%q 应为合法难度", got)
		}
	}
}

func TestDecidePrefersHu(t *testing.T) {
	for _, d := range []Difficulty{Easy, Normal, Hard} {
		v := seatView(
			[]string{"wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9", "tiao1", "tiao2", "tiao3", "dong"},
			"dong",
			[]string{"hu.self", "discard"},
		)
		got := Decide(Request{View: v, Rules: rules4(), Difficulty: d}, game.NewSeededSource(1))
		if got.Action != "hu.self" {
			t.Fatalf("%s 难度应优先和牌, 实际 %+v", d, got)
		}
	}
}

func TestDecideKeepsTenpaiOnDiscard(t *testing.T) {
	// 听 tiao2/tiao5, 摸到 xi 应打 xi 保持听牌。
	hand := []string{"wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9", "tiao3", "tiao4", "dong", "dong"}
	v := seatView(hand, "xi", []string{"discard"})
	for _, d := range []Difficulty{Normal, Hard} {
		got := Decide(Request{View: v, Rules: rules4(), Difficulty: d}, game.NewSeededSource(2))
		if got.Action != "discard" {
			t.Fatalf("%s 应打牌, 实际 %+v", d, got)
		}
		if got.Tile != "xi" {
			t.Fatalf("%s 应打掉孤张 xi 以保持听牌, 实际打了 %s", d, got.Tile)
		}
	}
}

func TestDecideRespondPassWhenNoGain(t *testing.T) {
	// 已听牌, 碰 dong 不会降低向听数(只是把手牌变成副露), 应过。
	hand := []string{"wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9", "tiao3", "tiao4", "dong", "dong"}
	v := seatView(hand, "", []string{"pass", "pong"})
	v.PendingTile = "dong"
	v.PendingBy = 1
	for _, d := range []Difficulty{Normal, Hard} {
		got := Decide(Request{View: v, Rules: rules4(), Difficulty: d}, game.NewSeededSource(3))
		if got.Action != "pass" {
			t.Fatalf("%s 不应鸣牌破坏听牌, 实际 %+v", d, got)
		}
	}
}

func TestDecideConcealedKong(t *testing.T) {
	// 这手牌暗杠 wan1 不会恶化向听(2 -> 2), 应选择暗杠。
	hand := []string{"wan1", "wan1", "wan1", "wan1", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "tiao1", "tiao4", "tong5"}
	v := seatView(hand, "", []string{"discard", "kong.concealed"})
	got := Decide(Request{View: v, Rules: rules4(), Difficulty: Normal}, game.NewSeededSource(4))
	if got.Action != "kong.concealed" || got.Tile != "wan1" {
		t.Fatalf("应暗杠 wan1, 实际 %+v", got)
	}
}

func TestDecideSkipsKongThatBreaksTenpai(t *testing.T) {
	// 四副面子 + 单张: 暗杠会拆掉一组面子而破坏听牌, 应改为打牌。
	hand := []string{"wan1", "wan1", "wan1", "wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9", "tiao1"}
	v := seatView(hand, "", []string{"discard", "kong.concealed"})
	got := Decide(Request{View: v, Rules: rules4(), Difficulty: Normal}, game.NewSeededSource(4))
	if got.Action != "discard" {
		t.Fatalf("不该用暗杠破坏听牌, 实际 %+v", got)
	}
}

func TestDecideChowIsLegal(t *testing.T) {
	hand := []string{"wan1", "wan2", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9", "tiao3", "tiao4", "dong", "dong", "tong1"}
	v := seatView(hand, "", []string{"pass", "chow"})
	v.PendingTile = "wan3"
	v.PendingBy = 1
	got := Decide(Request{View: v, Rules: rules4(), Difficulty: Normal}, game.NewSeededSource(5))
	if !got.Legal(v.Actions) {
		t.Fatalf("决策不在合法动作内: %+v", got)
	}
	if got.Action == "chow" {
		if len(got.With) != 2 {
			t.Fatalf("吃需要两张手牌, 实际 %+v", got)
		}
		ids := map[string]bool{}
		for _, c := range hand {
			ids[c] = true
		}
		for _, c := range got.With {
			if !ids[c] {
				t.Fatalf("吃用了不在手里的牌 %s", c)
			}
		}
		if !isChow(mustKind(t, got.With[0]), mustKind(t, got.With[1]), game.KindWan3) {
			t.Fatalf("吃的组合不构成顺子: %+v", got.With)
		}
	}
}

func TestDecideChowWhenItImproves(t *testing.T) {
	// 需要 wan3 才能成组的两面, 吃进后向听数下降。
	hand := []string{"wan1", "wan2", "wan4", "wan5", "wan7", "wan8", "tiao1", "tiao4", "tiao7", "tong2", "tong5", "dong", "xi"}
	v := seatView(hand, "", []string{"pass", "chow"})
	v.PendingTile = "wan3"
	v.PendingBy = 1
	base := Shanten(kindsOf(hand), 0, true)
	afterChow := Shanten(removeKinds(removeKinds(kindsOf(hand), game.KindWan1, 1), game.KindWan2, 1), 1, true)
	got := Decide(Request{View: v, Rules: rules4(), Difficulty: Normal}, game.NewSeededSource(6))
	if afterChow < base && got.Action != "chow" {
		t.Fatalf("吃可降低向听(%d->%d)时应吃, 实际 %+v", base, afterChow, got)
	}
}

func TestDecideDeterministic(t *testing.T) {
	hand := []string{"wan1", "wan2", "wan4", "wan5", "wan7", "wan8", "tiao1", "tiao4", "tiao7", "tong2", "tong5", "dong", "xi"}
	v := seatView(hand, "bei", []string{"discard"})
	for _, d := range []Difficulty{Normal, Hard} {
		a := Decide(Request{View: v, Rules: rules4(), Difficulty: d}, game.NewSeededSource(7))
		b := Decide(Request{View: v, Rules: rules4(), Difficulty: d}, game.NewSeededSource(7))
		if a.Action != b.Action || a.Tile != b.Tile {
			t.Fatalf("%s 同输入应得到同决策: %+v vs %+v", d, a, b)
		}
	}
}

// TestDecideLegalOnRandomHands 随机手牌上必须始终给出合法且可执行的决策。
func TestDecideLegalOnRandomHands(t *testing.T) {
	rng := rand.New(rand.NewSource(20261022))
	for i := 0; i < 150; i++ {
		wall := fullWall()
		rng.Shuffle(len(wall), func(a, b int) { wall[a], wall[b] = wall[b], wall[a] })
		hand := wall[:13]
		drawn := wall[13]
		codes := make([]string, 0, 13)
		for _, k := range hand {
			codes = append(codes, k.Code())
		}
		_, isWin := game.CanWinHand(append(append([]game.TileKind(nil), hand...), drawn), nil)
		actions := []string{"discard"}
		if isWin {
			actions = append(actions, "hu.self")
		}
		v := seatView(codes, drawn.Code(), actions)
		for _, d := range []Difficulty{Easy, Normal, Hard} {
			got := Decide(Request{View: v, Rules: rules4(), Difficulty: d}, game.NewSeededSource(int64(i)))
			if !got.Legal(actions) {
				t.Fatalf("%s 第 %d 局决策非法: %+v (actions=%v)", d, i, got, actions)
			}
			if got.Action == "discard" {
				all := map[string]bool{}
				for _, c := range codes {
					all[c] = true
				}
				all[drawn.Code()] = true
				if !all[got.Tile] {
					t.Fatalf("%s 第 %d 局打了不在手里的牌 %s", d, i, got.Tile)
				}
			}
		}
	}
}

func mustKind(t *testing.T, code string) game.TileKind {
	t.Helper()
	k, ok := game.KindFromCode(code)
	if !ok {
		t.Fatalf("未知牌代码 %s", code)
	}
	return k
}
