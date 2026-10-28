package game

import "testing"

// 手牌构造工具。
func kinds(codes ...string) []TileKind {
	out := make([]TileKind, len(codes))
	for i, c := range codes {
		k, ok := KindFromCode(c)
		if !ok {
			panic("未知牌代码: " + c)
		}
		out[i] = k
	}
	return out
}

// 胡牌牌型样例测试。
func TestWinShapes(t *testing.T) {
	cases := []struct {
		name     string
		hand     []TileKind
		melds    []Meld
		want     bool
		wantKind WinKind
	}{
		{
			name: "标准形: 4 顺子 + 雀头",
			hand: kinds("wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
				"tiao1", "tiao2", "tiao3", "tong1", "tong1"),
			want: true, wantKind: WinStandard,
		},
		{
			name: "标准形: 刻子混合",
			hand: kinds("wan1", "wan1", "wan1", "wan5", "wan6", "wan7",
				"tiao2", "tiao2", "tiao2", "dong", "dong", "dong", "zhong", "zhong"),
			want: true, wantKind: WinStandard,
		},
		{
			name: "标准形: 一副副露 + 3 面子暗牌",
			// 11 张暗牌 = 3 面子 + 雀头: wan456 + tiao789 + tong123 + dong×2。
			hand: kinds("wan4", "wan5", "wan6", "tiao7", "tiao8", "tiao9",
				"tong1", "tong2", "tong3", "dong", "dong"),
			melds: []Meld{{Kind: MeldPong, Tiles: [4]TileKind{KindEast, KindEast, KindEast}}},
			want: true, wantKind: WinStandard,
		},
		{
			name: "差一张不成和",
			hand: kinds("wan1", "wan2", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
				"tiao1", "tiao2", "tiao3", "tong1", "tong1", "tong2"),
			want: false,
		},
		{
			name: "七对子",
			hand: kinds("wan1", "wan1", "wan3", "wan3", "wan5", "wan5",
				"tiao7", "tiao7", "tong2", "tong2", "dong", "dong", "zhong", "zhong"),
			want: true, wantKind: WinSevenPairs,
		},
		{
			name: "伪七对(含刻子)",
			hand: kinds("wan1", "wan1", "wan1", "wan3", "wan3", "wan5", "wan5",
				"tiao7", "tiao7", "tong2", "tong2", "dong", "dong", "zhong"),
			want: false,
		},
		{
			name: "七对 + 副露不成七对",
			hand: kinds("wan1", "wan1", "wan3", "wan3", "wan5", "wan5", "tiao7", "tiao7",
				"tong2", "tong2", "dong", "dong"),
			melds: []Meld{{Kind: MeldPong, Tiles: [4]TileKind{KindRed, KindRed, KindRed}}},
			want: false,
		},
		{
			name: "字牌刻子 + 顺子",
			hand: kinds("fa", "fa", "fa", "zhong", "zhong", "zhong",
				"wan1", "wan2", "wan3", "tiao4", "tiao5", "tiao6", "tong9", "tong9"),
			want: true, wantKind: WinStandard,
		},
		{
			name: "跨花色不成顺",
			hand: kinds("wan8", "wan9", "tiao1", "tiao2", "tiao3", "tiao4", "tiao5", "tiao6",
				"tong7", "tong8", "tong9", "dong", "dong", "nan"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shape, ok := CanWinHand(tc.hand, tc.melds)
			if ok != tc.want {
				t.Fatalf("CanWinHand = %v, want %v", ok, tc.want)
			}
			if ok && shape.Kind != tc.wantKind {
				t.Fatalf("和牌类型 = %s, want %s", shape.Kind, tc.wantKind)
			}
		})
	}
}

// 听牌提示测试。
func TestTingHint(t *testing.T) {
	// 13 张听牌: 万123 万456 万789 条123 饼123 + 东(雀头挂单) -> 听东。
	// 改为双面搭子: 万123万456万789条123饼123饼4? 不对; 用标准听牌型:
	// wan1-9 三顺 + tiao1-3 顺 + tong1-3 顺 + dong 单 -> 听 dong 成雀头。
	hand := kinds("wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
		"tiao1", "tiao2", "tiao3", "dong")
	var seen [KindCount]int
	seen[KindEast] = 1 // 场上可见 1 张东, 仍可听。
	ting := TingHint(hand, nil, &seen)
	if len(ting) != 1 || ting[0] != KindEast {
		t.Fatalf("应只听东, 实际 %v", ting)
	}
	// 场上东 4 张全见时不再提示。
	seen[KindEast] = 4
	ting = TingHint(hand, nil, &seen)
	if len(ting) != 0 {
		t.Fatalf("东已全见, 应无听牌, 实际 %v", ting)
	}
}

func TestRuleSetValidation(t *testing.T) {
	// 默认规则合法。
	if err := DefaultRuleSet().Validate(); err != nil {
		t.Fatalf("默认规则应合法: %v", err)
	}
	// 冲突: 禁自摸且禁点炮。
	bad := DefaultRuleSet()
	bad.SelfDraw = false
	bad.DiscardWin = false
	if err := bad.Validate(); err == nil {
		t.Fatal("禁自摸+禁点炮应拒绝")
	}
	// 冲突: 门清限定 + 禁自摸。
	bad2 := DefaultRuleSet()
	bad2.SelfDraw = false
	bad2.ConcealedHandOnly = true
	if err := bad2.Validate(); err == nil {
		t.Fatal("门清限定+禁自摸应拒绝")
	}
	// 玩家数越界。
	for _, n := range []int{1, 5} {
		r := DefaultRuleSet()
		r.Players = n
		if err := r.Validate(); err == nil {
			t.Fatalf("players=%d 应拒绝", n)
		}
	}
	// 底分越界。
	r := DefaultRuleSet()
	r.BaseScore = 100001
	if err := r.Validate(); err == nil {
		t.Fatal("底分超限应拒绝")
	}
}

// 洗牌随机源测试。
func TestShuffledWall(t *testing.T) {
	w := NewShuffledWall(NewSeededSource(42))
	if w.Remaining() != TileTotal {
		t.Fatalf("洗后应有 %d 张", TileTotal)
	}
	// 每种恰好 4 张。
	var counts [KindCount]int
	for {
		id, ok := w.Draw()
		if !ok {
			break
		}
		counts[id.Kind()]++
	}
	for k, c := range counts {
		if c != 4 {
			t.Fatalf("种类 %s 应恰 4 张, 实际 %d", TileKind(k).Code(), c)
		}
	}
	// 同种子同牌序。
	w1 := NewShuffledWall(NewSeededSource(7))
	w2 := NewShuffledWall(NewSeededSource(7))
	for i := 0; i < TileTotal; i++ {
		a, _ := w1.Draw()
		b, _ := w2.Draw()
		if a != b {
			t.Fatalf("同种子第 %d 张应一致: %d vs %d", i, a, b)
		}
	}
	// 不同种子大概率不同序(允许极小概率相等)。
	w3 := NewShuffledWall(NewSeededSource(8))
	w4 := NewShuffledWall(NewSeededSource(9))
	diff := 0
	for i := 0; i < TileTotal; i++ {
		a, _ := w3.Draw()
		b, _ := w4.Draw()
		if a != b {
			diff++
		}
	}
	if diff < 100 {
		t.Fatalf("不同种子应几乎全不同, 仅 %d 处差异", diff)
	}
}
