package game

// 胡牌判定: 标准形(4 面子 + 1 对子)与七对子(开关)。
// 算法: 计数表 + 回溯消解; 136 张内规模极小, 无需剪枝优化。

// tilesToStrings 种类切片转线缆代码切片。
func tilesToStrings(tiles []TileKind) []string {
	out := make([]string, len(tiles))
	for i, k := range tiles {
		out[i] = k.Code()
	}
	return out
}

// canWin 判定手牌(暗牌) + 已亮副露能否和牌。
// concealed: 手牌(不含正在判定的第 14 张); melds: 已亮副露。
// 返回可赢的分解描述(用于展示)与类型。
type WinKind string

const (
	WinStandard    WinKind = "standard"     // 标准形
	WinSevenPairs  WinKind = "seven_pairs"  // 七对子
)

// WinShape 一次合法和牌的分解。
type WinShape struct {
	Kind  WinKind
	Melds [][3]TileKind // 标准形暗牌部分的面子分解(仅暗牌, 不含已亮副露)
	Pair  TileKind      // 雀头
}

// canFormStandardShape 在暗牌计数表上回溯消解 4-meldCount 个面子 + 1 对。
func canFormStandardShape(counts *[KindCount]int, meldsNeeded int) ([][3]TileKind, TileKind, bool) {
	// 找雀头: 任选 count>=2 的种类。
	for p := 0; p < int(KindCount); p++ {
		if counts[p] >= 2 {
			counts[p] -= 2
			if m, ok := solveMelds(counts, meldsNeeded, nil); ok {
				counts[p] += 2
				return m, TileKind(p), true
			}
			counts[p] += 2
		}
	}
	return nil, 0, false
}

// solveMelds 递归消解 n 个面子。
func solveMelds(counts *[KindCount]int, n int, acc [][3]TileKind) ([][3]TileKind, bool) {
	if n == 0 {
		// 剩余必须全为 0。
		for _, c := range counts {
			if c != 0 {
				return acc, false
			}
		}
		return acc, true
	}
	// 取最小非零种类 k, 尝试刻子或以 k 为首的顺子(保证分解唯一遍历)。
	k := -1
	for i, c := range counts {
		if c > 0 {
			k = i
			break
		}
	}
	if k < 0 {
		return acc, false
	}
	// 刻子。
	if counts[k] >= 3 {
		counts[k] -= 3
		if m, ok := solveMelds(counts, n-1, append(acc, [3]TileKind{TileKind(k), TileKind(k), TileKind(k)})); ok {
			counts[k] += 3
			return m, true
		}
		counts[k] += 3
	}
	// 顺子(k, k+1, k+2; 同花色内)。
	if isSuitKind(TileKind(k)) && k+2 < int(KindCount) && isSuitKind(TileKind(k+2)) &&
		counts[k+1] > 0 && counts[k+2] > 0 && sameSuit(TileKind(k), TileKind(k+2)) {
		counts[k]--
		counts[k+1]--
		counts[k+2]--
		if m, ok := solveMelds(counts, n-1, append(acc, [3]TileKind{TileKind(k), TileKind(k) + 1, TileKind(k) + 2})); ok {
			counts[k]++
			counts[k+1]++
			counts[k+2]++
			return m, true
		}
		counts[k]++
		counts[k+1]++
		counts[k+2]++
	}
	return acc, false
}

// canFormSevenPairs 严格七对: 恰好 7 种各 2 张(14 张, 无任何杠)。
func canFormSevenPairs(counts *[KindCount]int) bool {
	pairs := 0
	for _, c := range counts {
		switch c {
		case 0:
		case 2:
			pairs++
		default:
			return false
		}
	}
	return pairs == 7
}

// CanWinHand 判定一副完整手牌(暗牌 + 亮出的副露)能否和牌。
// 返回首个可行的分解; 七对子要求全部 14 张为暗牌(任何副露均破坏)。
func CanWinHand(concealed []TileKind, melds []Meld) (WinShape, bool) {
	var counts [KindCount]int
	for _, k := range concealed {
		if int(k) < int(KindCount) {
			counts[k]++
		}
	}
	// 标准形: 暗牌需满足 (4 - len(melds)) 个面子 + 1 对。
	meldsNeeded := 4 - len(melds)
	if meldsNeeded >= 0 {
		if m, pair, ok := canFormStandardShape(&counts, meldsNeeded); ok {
			return WinShape{Kind: WinStandard, Melds: m, Pair: pair}, true
		}
	}
	// 七对子: 仅当无任何副露。
	if len(melds) == 0 && canFormSevenPairs(&counts) {
		return WinShape{Kind: WinSevenPairs}, true
	}
	return WinShape{}, false
}

// TingHint 听牌提示计算: 枚举每一种牌作为第 14 张能否和。
// 用于流局赔付/提示; 返回可和牌的种类集合。
func TingHint(concealed []TileKind, melds []Meld, seenCounts *[KindCount]int) []TileKind {
	var out []TileKind
	for k := 0; k < int(KindCount); k++ {
		if seenCounts[k] >= 4 {
			continue // 该种 4 张已全部可见, 不可能再摸到。
		}
		_, ok := CanWinHand(append(append([]TileKind{}, concealed...), TileKind(k)), melds)
		if ok {
			out = append(out, TileKind(k))
		}
	}
	return out
}

// isSuitKind 报告种类是否为数字牌(万/条/饼)。
func isSuitKind(k TileKind) bool { return k < KindTiao1+18 } // 0..26

// sameSuit 报告两个种类是否同花色。
func sameSuit(a, b TileKind) bool { return a/9 == b/9 && isSuitKind(a) && isSuitKind(b) }

// WinShapeView 和牌分解投影。
type WinShapeView struct {
	Kind  string   `json:"kind"`
	Melds [][]string `json:"melds,omitempty"` // 标准形暗牌面子
	Pair  string   `json:"pair,omitempty"`    // 标准形雀头
}

// view 生成和牌分解投影。
func (w WinShape) view() WinShapeView {
	v := WinShapeView{Kind: string(w.Kind)}
	if w.Kind == WinStandard {
		for _, m := range w.Melds {
			v.Melds = append(v.Melds, tilesToStrings(m[:]))
		}
		v.Pair = w.Pair.Code()
	}
	return v
}
