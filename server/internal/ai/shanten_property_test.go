package ai

import (
	"math/rand"
	"testing"

	"majiang-server/internal/game"
)

// fullWall 返回标准 136 张(每类 4 张)。
func fullWall() []game.TileKind {
	wall := make([]game.TileKind, 0, game.TileTotal)
	for k := 0; k < int(game.KindCount); k++ {
		for i := 0; i < 4; i++ {
			wall = append(wall, game.TileKind(k))
		}
	}
	return wall
}

func shuffledWall(rng *rand.Rand) []game.TileKind {
	wall := fullWall()
	rng.Shuffle(len(wall), func(i, j int) { wall[i], wall[j] = wall[j], wall[i] })
	return wall
}

// TestShantenMatchesCanWin 交叉校验: Shanten == -1 当且仅当权威和牌判定通过。
// 这里用固定种子, 失败可复现。
func TestShantenMatchesCanWin(t *testing.T) {
	rng := rand.New(rand.NewSource(20261021))
	for i := 0; i < 400; i++ {
		wall := shuffledWall(rng)
		hand := append([]game.TileKind(nil), wall[:14]...)
		_, canWin := game.CanWinHand(hand, nil)
		sh := Shanten(hand, 0, true)
		if canWin != (sh == -1) {
			t.Fatalf("第 %d 局不一致: canWin=%v shanten=%d hand=%v", i, canWin, sh, hand)
		}
	}
}

// TestShantenTenpaiMatchesTingHint 交叉校验听牌集合:
// 对每一种牌 k, 插入后 Shanten == -1 必须与权威 TingHint 完全相同。
func TestShantenTenpaiMatchesTingHint(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	var seen [game.KindCount]int
	for i := 0; i < 120; i++ {
		wall := shuffledWall(rng)
		hand := append([]game.TileKind(nil), wall[:13]...)
		hints := game.TingHint(hand, nil, &seen)
		want := make(map[game.TileKind]bool, len(hints))
		for _, h := range hints {
			want[h] = true
		}
		for k := 0; k < int(game.KindCount); k++ {
			probe := append(append([]game.TileKind(nil), hand...), game.TileKind(k))
			got := Shanten(probe, 0, true) == -1
			if got != want[game.TileKind(k)] {
				t.Fatalf("第 %d 局 牌 %s 不一致: shantenWin=%v tingHint=%v hand=%v",
					i, game.TileKind(k).Code(), got, want[game.TileKind(k)], hand)
			}
		}
	}
}

// TestShantenMonotonic 校验不变量: 13 张手牌补一张牌后向听数不会变大。
func TestShantenMonotonic(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 60; i++ {
		wall := shuffledWall(rng)
		hand := append([]game.TileKind(nil), wall[:13]...)
		base := Shanten(hand, 0, true)
		if base < 0 || base > 8 {
			t.Fatalf("13 张手牌向听数越界: %d hand=%v", base, hand)
		}
		cnt := countsOfKinds(hand)
		for k := 0; k < int(game.KindCount); k++ {
			if cnt[k] >= 4 {
				continue
			}
			probe := append(append([]game.TileKind(nil), hand...), game.TileKind(k))
			if got := Shanten(probe, 0, true); got > base {
				t.Fatalf("补牌后向听数变大: %d -> %d (补 %s) hand=%v",
					base, got, game.TileKind(k).Code(), hand)
			}
		}
	}
}

// TestShantenWithMeldsMatchesCanWin 副露手也做一次交叉校验。
func TestShantenWithMeldsMatchesCanWin(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	for i := 0; i < 150; i++ {
		// 固定两组互不相同的刻子作为副露, 再从剩余牌中抽手牌,
		// 避免同一类牌超过 4 张而构造出非法手牌。
		meldKinds := []game.TileKind{game.KindWan1, game.KindTong5}
		wall := make([]game.TileKind, 0, game.TileTotal-6)
		for k := 0; k < int(game.KindCount); k++ {
			n := 4
			for _, mk := range meldKinds {
				if game.TileKind(k) == mk {
					n = 1 // 每组刻子用掉 3 张, 只留 1 张进牌墙
				}
			}
			for c := 0; c < n; c++ {
				wall = append(wall, game.TileKind(k))
			}
		}
		rng.Shuffle(len(wall), func(a, b int) { wall[a], wall[b] = wall[b], wall[a] })
		melds := make([]game.Meld, 0, len(meldKinds))
		for _, mk := range meldKinds {
			melds = append(melds, game.Meld{
				Kind:  game.MeldPong,
				Tiles: [4]game.TileKind{mk, mk, mk, 0},
			})
		}
		// 2 副露 => 和牌时需要 14-6 = 8 张暗牌。
		hand := append([]game.TileKind(nil), wall[:8]...)
		_, canWin := game.CanWinHand(hand, melds)
		sh := Shanten(hand, len(melds), true)
		if canWin != (sh == -1) {
			t.Fatalf("第 %d 局副露不一致: canWin=%v shanten=%d hand=%v", i, canWin, sh, hand)
		}
	}
}
