package ai

import (
	"majiang-server/internal/game"
)

// KindCount 牌种类数(万/条/筒各 9 种 + 7 种字牌)。
const KindCount = 34

const (
	shantenMaxCap = 1 << 20
	// suitSize 单一花色的点数个数。
	suitSize = 9
)

// tileCounts 按种类计数的牌张表, 下标即 TileKind。
type tileCounts [KindCount]int

// isSuit 报告种类是否为数牌(万条筒)。
func isSuit(k int) bool { return k >= 0 && k < 27 }

// suitRank 返回数牌在花色内的点数(0~8); 字牌返回 -1。
func suitRank(k int) int {
	if !isSuit(k) {
		return -1
	}
	return k % suitSize
}

// countsOfKinds 统计各类牌张数。
func countsOfKinds(hand []game.TileKind) tileCounts {
	var c tileCounts
	for _, t := range hand {
		if int(t) >= 0 && int(t) < KindCount {
			c[int(t)]++
		}
	}
	return c
}

// Shanten 返回向听数(-1 表示已和牌)。sevenPairs 为真时同时考虑七对子型,
// 取二者的较小值。melds 为已有副露组数(每组 3 张)。
//
// 该函数是纯函数: 结果只取决于入参, 不读任何全局状态, 因此可安全并发调用。
func Shanten(hand []game.TileKind, melds int, sevenPairs bool) int {
	if melds < 0 {
		melds = 0
	}
	if melds > 4 {
		melds = 4
	}
	c := countsOfKinds(hand)
	for i := range c {
		if c[i] > 4 {
			c[i] = 4
		}
	}
	best := shantenStandard(&c, melds)
	if sevenPairs && melds == 0 && len(hand) >= 13 {
		if s := shantenSevenPairs(&c); s < best {
			best = s
		}
	}
	return best
}

// shantenSevenPairs 七对子向听数。
func shantenSevenPairs(c *tileCounts) int {
	pairs, kinds := 0, 0
	for i := 0; i < KindCount; i++ {
		if c[i] > 0 {
			kinds++
			if c[i] >= 2 {
				pairs++
			}
		}
	}
	s := 6 - pairs
	if kinds < 7 {
		s += 7 - kinds
	}
	return s
}

// shantenKey 记忆化键: 剩余牌张表 + 已选面子/搭子/雀头数。
type shantenKey struct {
	lo, hi                  uint64
	sets, partials, hasPair uint8
}

// packCounts 把 34 个计数(各 0~4)打包进两个 uint64(每项 3 bit)。
func packCounts(c *tileCounts) (uint64, uint64) {
	var lo, hi uint64
	for i := 0; i < 21; i++ {
		lo |= uint64(c[i]) << (uint(i) * 3)
	}
	for i := 21; i < KindCount; i++ {
		hi |= uint64(c[i]) << (uint(i-21) * 3)
	}
	return lo, hi
}

// shantenStandard 标准型(4 面子 + 1 雀头)向听数。
func shantenStandard(c *tileCounts, melds int) int {
	s := searcher{best: shantenMaxCap, memo: make(map[shantenKey]struct{}, 256)}
	s.dfs(c, melds, 0, 0)
	return s.best
}

type searcher struct {
	best int
	memo map[shantenKey]struct{}
}

// dfs 枚举"面子/搭子/雀头"的全部拆分方式并收敛到最小向听数。
//
// 计分公式: 向听 = 8 - 2*面子 - 搭子 - 雀头。
// 约束: 面子 <= 4, 面子+搭子 <= 4, 雀头 <= 1(即总块数 <= 5)。
// 未被选入任何块的牌视为无用张, 不影响计分。
// shantenScore 由面子/搭子/雀头数计算向听数。
//
// 块数约束: 面子 + 搭子 <= 4 (搭子是用来补足面子的, 面子每多一个, 可用搭子位就少一个),
// 雀头 <= 1。超出该约束的"多余搭子"实际是无用张, 不能参与计分——
// 否则会出现 4 面子 + 1 搭子被算成 -1(已和牌) 的假和牌。
func shantenScore(sets, partials, hasPair int) int {
	p := partials
	if sets+p > 4 {
		p = 4 - sets
	}
	return 8 - 2*sets - p - hasPair
}

func (s *searcher) dfs(c *tileCounts, sets, partials, hasPair int) {
	score := shantenScore(sets, partials, hasPair)
	if score < s.best {
		s.best = score
	}
	if s.best <= -1 {
		return // 已和牌, 不可能更优
	}
	lo, hi := packCounts(c)
	key := shantenKey{lo: lo, hi: hi, sets: uint8(sets), partials: uint8(partials), hasPair: uint8(hasPair)}
	if _, seen := s.memo[key]; seen {
		return
	}
	s.memo[key] = struct{}{}

	idx := -1
	for i := 0; i < KindCount; i++ {
		if c[i] > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}

	// 1) 刻子
	if sets < 4 && c[idx] >= 3 {
		c[idx] -= 3
		s.dfs(c, sets+1, partials, hasPair)
		c[idx] += 3
	}
	// 2) 顺子
	if sets < 4 && isSuit(idx) && idx%9 <= 6 && c[idx+1] > 0 && c[idx+2] > 0 {
		c[idx]--
		c[idx+1]--
		c[idx+2]--
		s.dfs(c, sets+1, partials, hasPair)
		c[idx]++
		c[idx+1]++
		c[idx+2]++
	}
	// 3) 对子作为雀头
	if hasPair == 0 && c[idx] >= 2 {
		c[idx] -= 2
		s.dfs(c, sets, partials, 1)
		c[idx] += 2
	}
	if sets+partials < 4 {
		// 4) 对子作为搭子
		if c[idx] >= 2 {
			c[idx] -= 2
			s.dfs(c, sets, partials+1, hasPair)
			c[idx] += 2
		}
		// 5) 两面/边张搭子 (idx, idx+1)
		if isSuit(idx) && idx%9 <= 7 && c[idx+1] > 0 {
			c[idx]--
			c[idx+1]--
			s.dfs(c, sets, partials+1, hasPair)
			c[idx]++
			c[idx+1]++
		}
		// 6) 嵌张搭子 (idx, idx+2)
		if isSuit(idx) && idx%9 <= 6 && c[idx+2] > 0 {
			c[idx]--
			c[idx+2]--
			s.dfs(c, sets, partials+1, hasPair)
			c[idx]++
			c[idx+2]++
		}
	}
	// 7) 该牌不参与任何块
	c[idx]--
	s.dfs(c, sets, partials, hasPair)
	c[idx]++
}

// Ukeire 返回使向听数下降的进张总数与进张种类。
//
// visible 为全桌已可见牌(含自家手牌); 为 nil 时仅按自家手牌推断剩余张数。
// 结果的第二个返回值为去重后的进张种类(升序)。
func Ukeire(hand []game.TileKind, melds int, sevenPairs bool, visible *tileCounts) (int, []game.TileKind) {
	base := Shanten(hand, melds, sevenPairs)
	if base < 0 {
		return 0, nil
	}
	own := countsOfKinds(hand)
	total := 0
	var kinds []game.TileKind
	probe := make([]game.TileKind, 0, len(hand)+1)
	for k := 0; k < KindCount; k++ {
		seen := own[k]
		if visible != nil {
			seen = visible[k]
		}
		avail := 4 - seen
		if avail <= 0 {
			continue
		}
		probe = append(probe[:0], hand...)
		probe = append(probe, game.TileKind(k))
		if Shanten(probe, melds, sevenPairs) < base {
			total += avail
			kinds = append(kinds, game.TileKind(k))
		}
	}
	return total, kinds
}
