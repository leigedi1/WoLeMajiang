package ai

import (
	"fmt"
	"sort"

	"majiang-server/internal/game"
)

// Difficulty AI 难度。
type Difficulty string

const (
	// Easy 简单: 基础规则策略(优先打孤张/字牌, 少量随机)。
	Easy Difficulty = "easy"
	// Normal 普通: 牌效率策略(向听 + 进张)。
	Normal Difficulty = "normal"
	// Hard 困难: 牌效率 + 风险规避(有界启发式, 不使用任何云端模型)。
	Hard Difficulty = "hard"
)

// Valid 报告难度标识是否合法。
func (d Difficulty) Valid() bool {
	return d == Easy || d == Normal || d == Hard
}

// ParseDifficulty 解析难度字符串, 空串取默认(普通)。
func ParseDifficulty(s string) (Difficulty, error) {
	switch Difficulty(s) {
	case Easy, Normal, Hard:
		return Difficulty(s), nil
	case "":
		return Normal, nil
	default:
		return "", fmt.Errorf("未知 AI 难度: %q", s)
	}
}

// Request 一次 AI 决策请求。
//
// 安全边界: View 是权威端按座位裁剪后的投影, 在结构上只含合法可见信息
// (自家暗牌/本回合摸牌/全部河牌/全部副露/牌墙剩余数/待响应牌),
// 不含牌墙内容与他人暗牌。
type Request struct {
	View       game.SeatView
	Rules      game.RuleSet
	Difficulty Difficulty
}

// Decision AI 决策结果; Action 使用与线缆一致的动作代码。
type Decision struct {
	Action string   `json:"action"`
	Tile   string   `json:"tile,omitempty"`
	With   []string `json:"with,omitempty"`
}

// Legal 报告决策是否落在给定合法动作集合内。
func (d Decision) Legal(actions []string) bool {
	if d.Action == "" {
		return false
	}
	for _, a := range actions {
		if a == d.Action {
			return true
		}
	}
	return false
}

// Decide 根据可见信息与难度产生一个决策。rng 用于简单难度的随机性与
// 平局抽样; 传入 nil 时退化为确定性行为。
func Decide(req Request, rng game.RandomSource) Decision {
	v := req.View
	acts := make(map[string]bool, len(v.Actions))
	for _, a := range v.Actions {
		acts[a] = true
	}
	if len(acts) == 0 {
		return Decision{}
	}
	// 和牌永远优先(所有难度)。
	if acts["hu.self"] {
		return Decision{Action: "hu.self"}
	}
	if acts["hu.robbing"] {
		return Decision{Action: "hu.robbing"}
	}
	// 响应窗口: 出现 pass 即表示正在等待本人响应。
	if acts["pass"] {
		return decideRespond(req, acts, rng)
	}
	// 自家回合: 先看杠, 再打牌。
	if acts["kong.concealed"] || acts["kong.added"] {
		if d := decideKong(req, acts, rng); d.Action != "" {
			return d
		}
	}
	if acts["discard"] {
		return decideDiscard(req, rng)
	}
	if acts["draw"] {
		return Decision{Action: "draw"}
	}
	return Decision{}
}

// decideRespond 响应窗口(荣和/碰/吃/明杠/过)的决策。
func decideRespond(req Request, acts map[string]bool, rng game.RandomSource) Decision {
	v := req.View
	hand := handOf(v)
	melds := len(v.Melds)
	sp := req.Rules.SevenPairs
	pend, ok := game.KindFromCode(v.PendingTile)
	if !ok {
		return Decision{Action: "pass"}
	}

	// 荣和永远优先: 能胡就胡(简单难度也不例外)。
	if acts["hu.robbing"] {
		return Decision{Action: "hu.robbing"}
	}

	if req.Difficulty == Easy {
		// 简单: 多数情况过牌, 偶尔随机鸣牌。
		if rng == nil || rng.Int63n(100) < 65 {
			return Decision{Action: "pass"}
		}
		if acts["kong.exposed"] {
			return Decision{Action: "kong.exposed"}
		}
		if acts["pong"] {
			return Decision{Action: "pong"}
		}
		if acts["chow"] {
			if combos := chowCombos(hand, pend); len(combos) > 0 {
				pick := combos[0]
				if rng != nil {
					pick = combos[int(rng.Int63n(int64(len(combos))))]
				}
				return Decision{Action: "chow", With: []string{pick[0].Code(), pick[1].Code()}}
			}
		}
		return Decision{Action: "pass"}
	}

	// 普通/困难: 仅在鸣牌能降低向听数时鸣牌。
	base := Shanten(hand, melds, sp)
	bestSh := base
	var best *Decision
	consider := func(action string, with []string, concealed []game.TileKind) {
		sh := Shanten(concealed, melds+1, sp)
		if sh < bestSh {
			bestSh = sh
			best = &Decision{Action: action, With: with}
		}
	}
	if acts["kong.exposed"] {
		consider("kong.exposed", nil, removeKinds(hand, pend, 3))
	}
	if acts["pong"] {
		consider("pong", nil, removeKinds(hand, pend, 2))
	}
	if acts["chow"] {
		for _, c := range chowCombos(hand, pend) {
			h2 := removeKinds(removeKinds(hand, c[0], 1), c[1], 1)
			consider("chow", []string{c[0].Code(), c[1].Code()}, h2)
		}
	}
	if best != nil {
		return *best
	}
	return Decision{Action: "pass"}
}

// decideKong 暗杠/补杠决策(不改善向听则不打杠)。
func decideKong(req Request, acts map[string]bool, rng game.RandomSource) Decision {
	v := req.View
	hand := handOf(v)
	melds := len(v.Melds)
	sp := req.Rules.SevenPairs
	base := Shanten(hand, melds, sp)

	if acts["kong.concealed"] {
		cnt := countsOfKinds(hand)
		for k := 0; k < KindCount; k++ {
			if cnt[k] != 4 {
				continue
			}
			if req.Difficulty == Easy && rng != nil && rng.Int63n(100) < 50 {
				continue
			}
			h2 := removeKinds(hand, game.TileKind(k), 4)
			if Shanten(h2, melds+1, sp) <= base {
				return Decision{Action: "kong.concealed", Tile: game.TileKind(k).Code()}
			}
		}
	}
	if acts["kong.added"] {
		for _, m := range v.Melds {
			if m.Kind != string(game.MeldPong) || len(m.Tiles) == 0 {
				continue
			}
			k, ok := game.KindFromCode(m.Tiles[0])
			if !ok || countKind(hand, k) < 1 {
				continue
			}
			if req.Difficulty == Easy && rng != nil && rng.Int63n(100) < 50 {
				continue
			}
			h2 := removeKinds(hand, k, 1)
			if Shanten(h2, melds, sp) <= base {
				return Decision{Action: "kong.added", Tile: k.Code()}
			}
		}
	}
	return Decision{}
}

// decideDiscard 打牌决策。
func decideDiscard(req Request, rng game.RandomSource) Decision {
	v := req.View
	hand := handOf(v)
	melds := len(v.Melds)
	sp := req.Rules.SevenPairs
	uniq := uniqueKinds(hand)
	if len(uniq) == 0 {
		return Decision{}
	}
	if req.Difficulty == Easy {
		return easyDiscard(hand, uniq, rng)
	}

	vis := visibleOf(v)

	type cand struct {
		kind   game.TileKind
		sh     int
		ukeire int
		danger int
		iso    int // 孤立度: 手牌中同距 2 内的同伴数, 越大越有联络, 越不该打
	}
	cands := make([]cand, 0, len(uniq))
	for _, k := range uniq {
		h2 := removeKinds(hand, k, 1)
		cands = append(cands, cand{kind: k, sh: Shanten(h2, melds, sp), iso: neighborCount(hand, k)})
	}
	// 只对最优向听的候选计算进张(有界: 最多 6 个候选)。
	bestSh := cands[0].sh
	for _, c := range cands {
		if c.sh < bestSh {
			bestSh = c.sh
		}
	}
	considered := 0
	for i := range cands {
		if cands[i].sh != bestSh {
			continue
		}
		if considered >= 6 {
			break
		}
		considered++
		h2 := removeKinds(hand, cands[i].kind, 1)
		vis2 := vis
		if vis2[int(cands[i].kind)] > 0 {
			vis2[int(cands[i].kind)]--
		}
		cands[i].ukeire, _ = Ukeire(h2, melds, sp, &vis2)
	}
	// 困难模式: 对手有威胁时加入危险度评估。
	threat := 0
	if req.Difficulty == Hard {
		threat = threatLevel(v)
		if threat > 0 {
			for i := range cands {
				cands[i].danger = dangerOf(cands[i].kind, v)
			}
		}
	}

	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.sh != b.sh {
			return a.sh < b.sh
		}
		if a.ukeire != b.ukeire {
			return a.ukeire > b.ukeire
		}
		if threat > 0 {
			aw, bw := a.danger*threat, b.danger*threat
			if aw != bw {
				return aw < bw
			}
		}
		if a.iso != b.iso {
			return a.iso < b.iso
		}
		return a.kind < b.kind
	})
	return Decision{Action: "discard", Tile: cands[0].kind.Code()}
}

// easyDiscard 简单策略: 优先打孤张, 字牌与幺九优先, 其次在前三名中随机。
func easyDiscard(hand []game.TileKind, uniq []game.TileKind, rng game.RandomSource) Decision {
	type pick struct {
		kind  game.TileKind
		iso   int
		honor bool
	}
	ps := make([]pick, 0, len(uniq))
	for _, k := range uniq {
		ps = append(ps, pick{kind: k, iso: neighborCount(hand, k), honor: int(k) >= 27})
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].iso != ps[j].iso {
			return ps[i].iso < ps[j].iso
		}
		if ps[i].honor != ps[j].honor {
			return ps[i].honor
		}
		return ps[i].kind < ps[j].kind
	})
	n := len(ps)
	if n > 3 {
		n = 3
	}
	idx := 0
	if rng != nil && n > 1 {
		idx = int(rng.Int63n(int64(n)))
	}
	return Decision{Action: "discard", Tile: ps[idx].kind.Code()}
}

// handOf 汇总本人当前可打出的全部暗牌: 手牌 + 本回合摸到的牌。
func handOf(v game.SeatView) []game.TileKind {
	h := kindsOf(v.Hand)
	if v.Drawn != "" {
		if k, ok := game.KindFromCode(v.Drawn); ok {
			h = append(h, k)
		}
	}
	return h
}

// visibleOf 统计全桌已可见牌(自家手牌/摸牌 + 自家副露 + 全部河牌 + 他人副露)。
func visibleOf(v game.SeatView) tileCounts {
	var vis tileCounts
	add := func(code string) {
		if k, ok := game.KindFromCode(code); ok {
			vis[int(k)]++
		}
	}
	for _, code := range v.Hand {
		add(code)
	}
	if v.Drawn != "" {
		add(v.Drawn)
	}
	for _, m := range v.Melds {
		for _, code := range m.Tiles {
			add(code)
		}
	}
	for seat, group := range v.OthersMelds {
		if seat == int(v.Seat) {
			continue // 自家副露已在 v.Melds 计过
		}
		for _, m := range group {
			for _, code := range m.Tiles {
				add(code)
			}
		}
	}
	for _, river := range v.Discards {
		for _, code := range river {
			add(code)
		}
	}
	return vis
}

// threatLevel 评估其他玩家的威胁度(副露数与牌墙进度)。
func threatLevel(v game.SeatView) int {
	t := 0
	for seat, melds := range v.OthersMelds {
		if seat == int(v.Seat) {
			continue
		}
		t += len(melds)
	}
	if t >= 2 || v.WallLeft < 40 {
		if t < 1 {
			t = 1
		}
		return t
	}
	return 0
}

// dangerOf 估算打出该牌的危险度(数值越大越危险)。启发式, 有界。
func dangerOf(k game.TileKind, v game.SeatView) int {
	code := k.Code()
	// 现物: 已出现在任一对手河中 => 最安全。
	for seat, river := range v.Discards {
		if seat == int(v.Seat) {
			continue
		}
		for _, t := range river {
			if t == code {
				return 0
			}
		}
	}
	if int(k) >= 27 {
		return 2 // 字牌
	}
	switch suitRank(int(k)) {
	case 0, 8:
		return 3 // 幺九
	case 1, 7:
		return 5
	default:
		return 7 // 中张最危险
	}
}

// neighborCount 手牌中与该牌有联络的同伴数(同种、或同花色距离 1~2)。
func neighborCount(hand []game.TileKind, k game.TileKind) int {
	n := 0
	for _, h := range hand {
		if h == k {
			n++
			continue
		}
		if isSuit(int(h)) && isSuit(int(k)) && int(h)/9 == int(k)/9 {
			dist := suitRank(int(h)) - suitRank(int(k))
			if dist < 0 {
				dist = -dist
			}
			if dist <= 2 {
				n++
			}
		}
	}
	return n
}

// kindsOf 线缆代码切片转种类切片(忽略非法代码)。
func kindsOf(codes []string) []game.TileKind {
	out := make([]game.TileKind, 0, len(codes))
	for _, c := range codes {
		if k, ok := game.KindFromCode(c); ok {
			out = append(out, k)
		}
	}
	return out
}

// removeKinds 移除 n 张指定种类(不足时移除全部)。
func removeKinds(h []game.TileKind, k game.TileKind, n int) []game.TileKind {
	out := make([]game.TileKind, 0, len(h))
	removed := 0
	for _, t := range h {
		if t == k && removed < n {
			removed++
			continue
		}
		out = append(out, t)
	}
	return out
}

// uniqueKinds 去重并按点数升序返回。
func uniqueKinds(h []game.TileKind) []game.TileKind {
	seen := make(map[game.TileKind]bool, len(h))
	out := make([]game.TileKind, 0, len(h))
	for _, t := range h {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// countKind 统计手牌中某种类张数。
func countKind(h []game.TileKind, k game.TileKind) int {
	n := 0
	for _, t := range h {
		if t == k {
			n++
		}
	}
	return n
}

// chowCombos 列出用手牌与被打牌组成顺子的搭配(去重, 顺序稳定)。
func chowCombos(hand []game.TileKind, tile game.TileKind) [][2]game.TileKind {
	uniq := uniqueKinds(hand)
	var out [][2]game.TileKind
	for i := 0; i < len(uniq); i++ {
		for j := i + 1; j < len(uniq); j++ {
			a, b := uniq[i], uniq[j]
			if a == b {
				continue
			}
			if isChow(tile, a, b) {
				out = append(out, [2]game.TileKind{a, b})
			}
		}
	}
	return out
}

// isChow 判断三张牌是否构成同花色顺子。
func isChow(a, b, c game.TileKind) bool {
	if !isSuit(int(a)) || !isSuit(int(b)) || !isSuit(int(c)) {
		return false
	}
	if int(a)/9 != int(b)/9 || int(b)/9 != int(c)/9 {
		return false
	}
	r := []int{suitRank(int(a)), suitRank(int(b)), suitRank(int(c))}
	sort.Ints(r)
	return r[1] == r[0]+1 && r[2] == r[1]+1
}
