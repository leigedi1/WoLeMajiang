package ai

import (
	"strings"
	"testing"

	"majiang-server/internal/game"
)

// parse 把 "wan1 tiao2 dong" 形式的代码串解析为种类切片。
func parse(t *testing.T, s string) []game.TileKind {
	t.Helper()
	fields := strings.Fields(s)
	out := make([]game.TileKind, 0, len(fields))
	for _, f := range fields {
		k, ok := game.KindFromCode(f)
		if !ok {
			t.Fatalf("未知牌代码 %q", f)
		}
		out = append(out, k)
	}
	return out
}

func TestShantenStandard(t *testing.T) {
	cases := []struct {
		name  string
		hand  string
		melds int
		want  int
	}{
		{
			name: "和牌-四面子一雀头",
			hand: "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 wan9 tiao1 tiao2 tiao3 dong dong",
			want: -1,
		},
		{
			name: "听牌-单骑",
			hand: "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 wan9 tiao1 tiao2 tiao3 dong",
			want: 0,
		},
		{
			name: "听牌-两面",
			hand: "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 wan9 tiao1 tiao2 dong dong",
			want: 0,
		},
		{
			name: "一向听",
			hand: "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 tiao1 tiao2 tiao4 tiao5 dong dong",
			want: 1,
		},
		{
			name: "四面子无雀头-听牌",
			hand: "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 wan9 tiao1 tiao2 tiao3 tiao5",
			want: 0,
		},
		{
			name:  "两副露-听牌",
			hand:  "wan1 wan2 wan3 dong dong tiao1 tiao2",
			melds: 2,
			want:  0,
		},
		{
			name:  "两副露-已和牌",
			hand:  "wan1 wan2 wan3 dong dong",
			melds: 3,
			want:  -1,
		},
		{
			name: "全部孤张-最大向听",
			hand: "wan1 wan4 wan7 tiao1 tiao4 tiao7 tong1 tong4 tong7 dong nan xi bei",
			want: 8,
		},
		{
			name: "四条顺子",
			hand: "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 wan9 tiao1 tiao2 tiao3 tiao4 tiao5",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Shanten(parse(t, tc.hand), tc.melds, false)
			if got != tc.want {
				t.Fatalf("向听数不符: got %d want %d", got, tc.want)
			}
		})
	}
}

func TestShantenSevenPairs(t *testing.T) {
	cases := []struct {
		name string
		hand string
		want int
	}{
		{
			name: "六对子单张-听牌",
			hand: "wan1 wan1 wan2 wan2 wan3 wan3 tiao1 tiao1 tiao2 tiao2 dong dong tong5",
			want: 0,
		},
		{
			name: "和牌-七对子",
			hand: "wan1 wan1 wan2 wan2 wan3 wan3 tiao1 tiao1 tiao2 tiao2 dong dong tong5 tong5",
			want: -1,
		},
		{
			name: "四对子五单张",
			hand: "wan1 wan1 wan2 wan2 wan3 wan3 tiao1 tiao1 tiao2 tiao4 dong nan tong5",
			want: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Shanten(parse(t, tc.hand), 0, true)
			if got != tc.want {
				t.Fatalf("七对子向听不符: got %d want %d", got, tc.want)
			}
		})
	}
}

func TestShantenSevenPairsBeatsStandard(t *testing.T) {
	// 六对子单张: 标准型 3 向听, 七对子 0 向听, 应取较小值。
	hand := parse(t, "wan1 wan1 wan2 wan2 wan3 wan3 tiao1 tiao1 tiao2 tiao2 dong dong tong5")
	if got := Shanten(hand, 0, false); got == 0 {
		t.Fatalf("关闭七对子时标准型不应为 0 向听, 实际 %d", got)
	}
	if got := Shanten(hand, 0, true); got != 0 {
		t.Fatalf("开启七对子后应为 0 向听, 实际 %d", got)
	}
}

func TestUkeire(t *testing.T) {
	// 两面听 3wan/6wan, 共 4 张可见 0 张 => 8 张进张。
	hand := parse(t, "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 wan9 tiao3 tiao4 dong dong")
	total, kinds := Ukeire(hand, 0, false, nil)
	if total != 8 {
		t.Fatalf("进张数不符: got %d want 8", total)
	}
	want := map[game.TileKind]bool{game.KindTiao2: true, game.KindTiao5: true}
	if len(kinds) != len(want) {
		t.Fatalf("进张种类数不符: got %v", kinds)
	}
	for _, k := range kinds {
		if !want[k] {
			t.Fatalf("意外进张种类 %s", k.Code())
		}
	}
}

func TestUkeireRespectsVisible(t *testing.T) {
	hand := parse(t, "wan1 wan2 wan3 wan4 wan5 wan6 wan7 wan8 wan9 tiao3 tiao4 dong dong")
	var vis tileCounts
	vis = countsOfKinds(hand)
	// 假设桌上已见 3 张 tiao2, 则 tiao2 只剩 1 张可进。
	vis[int(game.KindTiao2)] = 4
	total, _ := Ukeire(hand, 0, false, &vis)
	if total != 4 {
		t.Fatalf("扣除可见牌后进张数不符: got %d want 4", total)
	}
}

func BenchmarkShanten14(b *testing.B) {
	hand := []game.TileKind{
		game.KindWan1, game.KindWan4, game.KindWan7,
		game.KindTiao1, game.KindTiao4, game.KindTiao7,
		game.KindTong1, game.KindTong4, game.KindTong7,
		game.KindEast, game.KindSouth, game.KindWest, game.KindNorth, game.KindRed,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Shanten(hand, 0, true)
	}
}

func BenchmarkUkeire13(b *testing.B) {
	hand := []game.TileKind{
		game.KindWan1, game.KindWan2, game.KindWan3,
		game.KindTiao2, game.KindTiao3, game.KindTiao4,
		game.KindTong5, game.KindTong6, game.KindTong7,
		game.KindEast, game.KindEast, game.KindSouth, game.KindWest,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Ukeire(hand, 0, false, nil)
	}
}
