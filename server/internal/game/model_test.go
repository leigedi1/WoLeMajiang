package game

import "testing"

func TestKindCodeTableRoundtrip(t *testing.T) {
	for k := TileKind(0); int(k) < int(KindCount); k++ {
		code := k.Code()
		if code == "" {
			t.Fatalf("种类 %d 代码为空", k)
		}
		back, ok := KindFromCode(code)
		if !ok || back != k {
			t.Fatalf("代码 %s 反查失败: %v %d", code, back, k)
		}
	}
}

func TestKindCodeKnownValues(t *testing.T) {
	cases := map[TileKind]string{
		KindWan1: "wan1", KindWan9: "wan9",
		KindTiao1: "tiao1", KindTiao9: "tiao9",
		KindTong1: "tong1", KindTong9: "tong9",
		KindEast: "dong", KindSouth: "nan", KindWest: "xi", KindNorth: "bei",
		KindRed: "zhong", KindGreen: "fa", KindWhite: "bai",
	}
	for k, want := range cases {
		if got := k.Code(); got != want {
			t.Errorf("种类 %d 代码应为 %s, 实际 %s", k, want, got)
		}
	}
	if _, ok := KindFromCode("nope"); ok {
		t.Error("未知代码不应反查成功")
	}
}

func TestTileIDKind(t *testing.T) {
	// id 0..3 均为万1; id 12..15 为万4; id 108..111 为"东"; id 124..127 为"中"。
	for _, id := range []TileID{0, 1, 2, 3} {
		if id.Kind() != KindWan1 {
			t.Errorf("id %d 应为 wan1", id)
		}
	}
	for _, id := range []TileID{12, 13, 14, 15} {
		if id.Kind() != KindWan4 {
			t.Errorf("id %d 应为 wan4", id)
		}
	}
	for _, id := range []TileID{108, 109, 110, 111} {
		if id.Kind() != KindEast {
			t.Errorf("id %d 应为 dong", id)
		}
	}
	for _, id := range []TileID{124, 125, 126, 127} {
		if id.Kind() != KindRed {
			t.Errorf("id %d 应为 zhong", id)
		}
	}
	if TileID(108).Kind() != KindEast {
		t.Errorf("id 108 应为 dong")
	}
	if !TileID(135).Valid() || TileID(136).Valid() {
		t.Error("TileID 有效性边界错误")
	}
}

func TestOrderedWall(t *testing.T) {
	w := NewOrderedWall()
	if w.Remaining() != TileTotal {
		t.Fatalf("牌墙应有 %d 张, 实际 %d", TileTotal, w.Remaining())
	}
}

func TestSeatWind(t *testing.T) {
	want := []string{WindEast, WindSouth, WindWest, WindNorth}
	for seat := 0; seat < 4; seat++ {
		if got := Seat(seat).Wind(); got != want[seat] {
			t.Errorf("座位 %d 风位应为 %s, 实际 %s", seat, want[seat], got)
		}
	}
	if Seat(-1).Wind() != "" || Seat(4).Wind() != "" {
		t.Error("越界座位不应有风位")
	}
}

func TestRuleSetValidate(t *testing.T) {
	// 完整合法规则下玩家数 2~4 合法。
	for _, n := range []int{2, 3, 4} {
		r := DefaultRuleSet()
		r.Players = n
		if err := r.Validate(); err != nil {
			t.Errorf("players=%d 应合法: %v", n, err)
		}
	}
	for _, n := range []int{0, 1, 5, -1} {
		r := DefaultRuleSet()
		r.Players = n
		if err := r.Validate(); err == nil {
			t.Errorf("players=%d 应非法", n)
		}
	}
	if err := DefaultRuleSet().Validate(); err != nil {
		t.Errorf("默认规则应合法: %v", err)
	}
}

func TestThemeValid(t *testing.T) {
	if !ThemeClassic.Valid() || !ThemeInk.Valid() {
		t.Error("两种主题应合法")
	}
	if Theme("gold").Valid() {
		t.Error("未知主题应非法")
	}
}
