package game

import (
	"errors"
	"testing"
	"time"
)

// matchWithHands 用给定手牌构造可控对局(注入确定性墙序)。
// hands: 座位 0..n-1 各 13 张; 庄家另给 1 张(第 14 张, hands[dealer] 末尾即第13张后的首摸)。
// dealerExtra: 庄家第 14 张(单独传入, 避免与 13 张混淆)。
func matchWithHands(t *testing.T, rules RuleSet, dealer Seat, hands [4][]TileKind, dealerExtra TileKind) *Match {
	t.Helper()
	// 墙序必须严格匹配 deal 的取牌顺序:
	// 13 轮 × 每轮按 (dealer+i)%n 家各 1 张, 然后庄家 1 张。
	var order []TileID
	used := map[TileID]bool{}
	take := func(k TileKind) TileID {
		for id := TileID(0); id < TileTotal; id++ {
			if id.Kind() == k && !used[id] {
				used[id] = true
				return id
			}
		}
		t.Fatalf("牌 %s 超过 4 张, 无法构造", k.Code())
		return 0
	}
	n := rules.Players
	for round := 0; round < 13; round++ {
		for i := 0; i < n; i++ {
			seat := (int(dealer) + i) % n
			order = append(order, take(hands[seat][round]))
		}
	}
	order = append(order, take(dealerExtra))
	m := &Match{
		rules: rules,
		wall:  &Wall{tiles: append(append([]TileID{}, order...), remainingTiles(order)...)},
		phase:           PhaseDealing,
		lastDiscardSeat: -1,
	}
	for i := 0; i < rules.Players; i++ {
		m.players[i] = &matchPlayer{}
	}
	m.deal(dealer)
	return m
}

func containsID(ids []TileID, id TileID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// remainingTiles 补齐 136 张(保持总牌数正确)。
func remainingTiles(used []TileID) []TileID {
	var out []TileID
	for id := TileID(0); id < TileTotal; id++ {
		if !containsID(used, id) {
			out = append(out, id)
		}
	}
	return out
}

// TestTurnFlowDiscardDraw 基本回合: 庄家打牌 -> 下家摸牌 -> 打牌。
func TestTurnFlowDiscardDraw(t *testing.T) {
	rules := DefaultRuleSet()
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong"),
		kinds("wan1", "wan3", "wan5", "wan7", "wan9", "tiao2", "tiao4", "tiao6", "tiao8",
			"tong1", "tong3", "tong5", "dong"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "xi"),
	}
	m := matchWithHands(t, rules, 0, hands, KindEast) // 庄家第 14 张摸东。
	if m.Phase() != PhaseTurn {
		t.Fatalf("发牌后应进入行牌, 实际 %s", m.Phase())
	}
	if m.TurnSeat() != 0 {
		t.Fatalf("庄家应先行动")
	}
	// 庄家手牌 14 张。
	v0 := m.ViewFor(0)
	if len(v0.Hand) != 14 {
		t.Fatalf("庄家应 14 张, 实际 %d", len(v0.Hand))
	}
	// 庄家打牌。其余三家对 wan1 都没有胡/碰/杠/吃的真实选择,
	// 因此响应窗口在引擎内自动收敛(自动过牌), 直接轮转到下家摸牌。
	if _, err := m.Discard(0, KindWan1); err != nil {
		t.Fatalf("打牌失败: %v", err)
	}
	if m.turnStage != stageDraw || m.TurnSeat() != 1 {
		t.Fatalf("无人可响应时应自动轮转: stage=%s turn=%d", m.turnStage, m.TurnSeat())
	}
	if m.respPending != nil {
		t.Fatal("自动过牌后不应残留响应窗口")
	}
	// 非轮到座位打牌应被拒(此时轮到 1 号摸牌)。
	if _, err := m.Discard(2, KindWan2); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("非当前座位打牌应报阶段/轮次错误: %v", err)
	}
	// 下家(1号)进入摸牌。
	if m.TurnSeat() != 1 {
		t.Fatalf("应轮到 1 号, 实际 %d", m.TurnSeat())
	}
	v1 := m.ViewFor(1)
	if v1.TurnStage != "draw" {
		t.Fatalf("1 号应处于摸牌阶段: %s", v1.TurnStage)
	}
	// 非轮到座位在他人摸牌阶段打牌应被拒。
	if _, err := m.Discard(2, KindWan2); err == nil {
		t.Fatal("非当前座位打牌应报错")
	}
	// 1 号摸牌。
	if _, err := m.Draw(); err != nil {
		t.Fatalf("摸牌失败: %v", err)
	}
	v1 = m.ViewFor(1)
	if len(v1.Hand) != 14 {
		t.Fatalf("摸后应 14 张: %d", len(v1.Hand))
	}
	// 他人不可见手牌。
	v2 := m.ViewFor(2)
	if v2.OthersHandCounts[1] != 14 {
		t.Fatalf("2 号视角 1 号手牌数应为 14: %d", v2.OthersHandCounts[1])
	}
	if v2.Hand != nil && len(v2.Hand) != 13 {
		t.Fatalf("2 号自己手牌 13 张: %d", len(v2.Hand))
	}
}

// TestPongFlow 碰流程与优先级。
func TestPongFlow(t *testing.T) {
	rules := DefaultRuleSet()
	// 1 号手牌两张万5, 0 号打出万5。
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("wan5", "wan5", "wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5",
			"tong1", "tong3", "tong5", "dong"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "xi"),
	}
	m := matchWithHands(t, rules, 0, hands, KindWan5) // 庄家第 14 张摸 wan5
	// 0 号打 wan5。
	m.Discard(0, KindWan5)
	// 1 号碰。
	res, err := m.Respond(1, RespondAction{Pong: true})
	if err != nil {
		t.Fatalf("碰失败: %v", err)
	}
	if len(res.Events) == 0 || res.Events[0].Kind != EvPong {
		t.Fatalf("应产生碰事件")
	}
	// 碰后轮到 1 号打牌。
	if m.TurnSeat() != 1 {
		t.Fatalf("碰后应轮到 1 号: %d", m.TurnSeat())
	}
	v1 := m.ViewFor(1)
	if len(v1.Hand) != 11 { // 13 - 2 = 11
		t.Fatalf("碰后手牌应 11 张: %d", len(v1.Hand))
	}
	if len(v1.Melds) != 1 || v1.Melds[0].Kind != "pong" {
		t.Fatalf("应有碰副露: %+v", v1.Melds)
	}
	// 他人视角同样可见副露。
	v2 := m.ViewFor(2)
	if len(v2.OthersMelds[1]) != 1 {
		t.Fatalf("2 号应见 1 号副露")
	}
}

// TestPriorityWinOverPong 优先级仲裁: 胡 > 碰/杠 > 吃。
// 1 号先声明碰, 但 2 号能胡且尚未表态 → 碰进入"判定中"等待; 2 号胡后
// 碰被判负, 和牌成立。这正是"截胡"的默认行为。
func TestPriorityWinOverPong(t *testing.T) {
	rules := DefaultRuleSet()
	// 0 号打 tiao5; 1 号两张 tiao5(共3张)可碰; 2 号 tiao456 缺 5, +tiao5 成顺。
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("tiao5", "tiao5", "wan2", "wan4", "wan6", "wan8", "tong1", "tong3", "tong5",
			"tong7", "tong9", "nan", "xi"),
		// 2 号 13 张: wan123 tiao4 tiao6 tiao789 tong123 dong刻 -> + tiao5 = tiao456 和牌。
		kinds("wan1", "wan2", "wan3", "tiao4", "tiao6", "tiao7", "tiao8", "tiao9",
			"tong1", "tong2", "tong3", "dong", "dong"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao2", "tiao9",
			"tong2", "tong4", "tong6", "bei", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindTiao5) // 庄家第 14 张摸 tiao5 -> 打出
	m.Discard(0, KindTiao5)
	// 1 号先声明碰: 2 号还能胡(更高优先级)且未表态, 所以碰只能"判定中"。
	res, err := m.Respond(1, RespondAction{Pong: true})
	if err != nil {
		t.Fatalf("碰失败: %v", err)
	}
	if len(res.Events) != 1 || res.Events[0].Kind != EvJudge {
		t.Fatalf("碰应先进入判定中, 实际事件 %+v", res.Events)
	}
	if m.TurnSeat() != 0 {
		t.Fatalf("判定期间不应轮转座位, 实际 %d", m.TurnSeat())
	}
	if m.ClaimState(1) != "pending" {
		t.Fatalf("1 号应为判定中, 实际 %q", m.ClaimState(1))
	}
	// 2 号胡: 胡优先级更高, 碰被判负, 和牌成立。
	res, err = m.Respond(2, RespondAction{Win: true})
	if err != nil {
		t.Fatalf("胡失败: %v", err)
	}
	if m.Phase() != PhaseFinished || m.Winner() != 2 {
		t.Fatalf("2 号应和牌, winner=%d phase=%s", m.Winner(), m.Phase())
	}
	if m.ClaimState(1) != "failed" {
		t.Fatalf("1 号碰应判负, 实际 %q", m.ClaimState(1))
	}
	var hasWin bool
	for _, ev := range res.Events {
		if ev.Kind == EvWin {
			hasWin = true
		}
	}
	if !hasWin {
		t.Fatalf("应产生和牌事件, 实际 %+v", res.Events)
	}
}

// TestRobWinOrder 截胡开启时按出牌顺序(离出牌者最近者)优先; 关闭时先按者得。
func TestRobWinOrder(t *testing.T) {
	rules := DefaultRuleSet()
	// 0 号打 tiao5; 1 号与 2 号都能胡(不同牌型各缺 tiao5)。
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		// 1 号: tiao4 tiao6 + 其余成刻/顺 -> +tiao5 成 tiao456。
		kinds("wan1", "wan1", "wan1", "wan2", "wan2", "wan2", "wan3", "wan3", "wan3",
			"tiao4", "tiao6", "dong", "dong"),
		// 2 号: 同型, 也能 +tiao5 和牌。
		kinds("tong1", "tong1", "tong1", "tong2", "tong2", "tong2", "tong3", "tong3", "tong3",
			"tiao4", "tiao6", "nan", "nan"),
		kinds("wan4", "wan5", "wan6", "tiao1", "tiao2", "tiao3", "tong4", "tong5", "tong6",
			"bei", "bei", "zhong", "zhong"),
	}
	for _, robWin := range []bool{true, false} {
		rules.RobWin = robWin
		m := matchWithHands(t, rules, 0, hands, KindTiao5)
		if _, err := m.Discard(0, KindTiao5); err != nil {
			t.Fatalf("打牌失败: %v", err)
		}
		// 2 号先声明胡(顺位更远), 1 号后声明。
		if _, err := m.Respond(2, RespondAction{Win: true}); err != nil {
			t.Fatalf("2 号胡失败: %v", err)
		}
		if robWin {
			// 截胡开启: 更靠前的 1 号还有机会, 2 号的胡只能"判定中"。
			if m.Phase() == PhaseFinished {
				t.Fatal("截胡开启时 2 号不应立即和牌")
			}
			if _, err := m.Respond(1, RespondAction{Win: true}); err != nil {
				t.Fatalf("1 号胡失败: %v", err)
			}
			if m.Winner() != 1 {
				t.Fatalf("截胡开启时应由离出牌者最近的 1 号和牌, 实际 %d", m.Winner())
			}
		} else {
			// 截胡关闭: 先按者得, 2 号当场和牌。
			if m.Phase() != PhaseFinished || m.Winner() != 2 {
				t.Fatalf("截胡关闭时应由先声明的 2 号和牌, 实际 winner=%d phase=%s", m.Winner(), m.Phase())
			}
		}
	}
}

// TestWinSelfDraw 自摸流程与计分。
func TestWinSelfDraw(t *testing.T) {
	rules := DefaultRuleSet()
	// 0 号(庄家)第 14 张摸 wan4 自摸:
	// 手牌 13: wan1×3 wan2×3 wan3×3 wan4×2 tiao9 -> 摸 wan4 = wan123×3 + wan44 雀头 + tiao9 挂单? 不成和。
	// 正确构造: wan111 wan222 wan333 + wan4 wan4 + tiao9 tiao9? 需 13 张:
	// wan1×3 wan2×3 wan3×3 wan4×2 tiao9×1 = 12? 3+3+3+2+1=12 不够。
	// 13 张: wan1×3 wan2×3 wan3×3 wan4 tiao9×3? 3+3+3+1+3=13, 摸 wan4:
	// = wan111 wan222 wan333 wan44(雀头) tiao999(刻子) -> 和! 门清。
	hands := [4][]TileKind{
		kinds("wan1", "wan1", "wan1", "wan2", "wan2", "wan2", "wan3", "wan3", "wan3",
			"wan4", "tiao9", "tiao9", "tiao9"),
		kinds("wan5", "wan5", "wan5", "wan6", "wan6", "wan6", "tiao1", "tiao3", "tiao5",
			"tong1", "tong3", "tong5", "dong"),
		kinds("wan7", "wan7", "wan7", "wan8", "wan8", "wan8", "tiao2", "tiao4", "tiao6",
			"tong2", "tong4", "tong6", "nan"),
		kinds("wan9", "wan9", "wan9", "tiao7", "tiao8", "tiao9", "tong7", "tong8", "tong9",
			"tiao1", "tiao2", "dong", "xi"),
	}
	m := matchWithHands(t, rules, 0, hands, KindWan4) // 庄家第 14 张摸 wan4 -> 自摸
	res, err := m.WinSelf(0)
	if err != nil {
		t.Fatalf("自摸失败: %v", err)
	}
	if len(res.Events) == 0 || res.Events[0].Kind != EvWin {
		t.Fatal("应产生和牌事件")
	}
	if m.Phase() != PhaseFinished || m.Winner() != 0 {
		t.Fatalf("应结算: %s winner=%d", m.Phase(), m.Winner())
	}
	// 计分: 基础1番 + 自摸1番 + 门清1番 = 3番 = 8分; 三家各付8, 赢24。
	scores := m.Scores()
	if scores[0] != 24 {
		t.Fatalf("赢家应 +24: %v", scores)
	}
	for i := 1; i < 4; i++ {
		if scores[i] != -8 {
			t.Fatalf("%d 号应 -8: %v", i, scores)
		}
	}
	// 对局结束后任何动作应被拒。
	if _, err := m.Discard(0, KindWan1); !errors.Is(err, ErrMatchOver) {
		t.Fatalf("结束后打牌应报 ErrMatchOver: %v", err)
	}
}

// TestWinByDiscard 点炮与放炮者赔付。
func TestWinByDiscard(t *testing.T) {
	rules := DefaultRuleSet()
	// 0 号打出 bei(北), 2 号荣和: 手牌 13 + bei。
	// 2 号: wan123 tiao456 tong123 nan刻 + bei(雀头挂单) -> +bei 和。
	// 全局计数: bei 2 张(0号打出+2号雀头), nan 3 张(2号刻子), dong 2 张 OK。
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong"),
		kinds("wan5", "wan5", "wan5", "wan6", "wan6", "wan6", "tiao1", "tiao3", "tiao5",
			"tong1", "tong3", "tong5", "xi"),
		kinds("wan1", "wan2", "wan3", "tiao4", "tiao5", "tiao6",
			"tong1", "tong2", "tong3", "nan", "nan", "nan", "bei"), // +bei = 14张和牌
		kinds("wan9", "wan9", "wan9", "tiao7", "tiao8", "tiao9", "tong7", "tong8", "tong9",
			"tiao1", "tiao2", "zhong", "fa"),
	}
	m := matchWithHands(t, rules, 0, hands, KindNorth) // 庄家第 14 张摸 bei(北) -> 打出
	m.Discard(0, KindNorth)
	// 1 号过, 2 号胡。
	m.Respond(1, RespondAction{Pass: true})
	res, err := m.Respond(2, RespondAction{Win: true})
	if err != nil {
		t.Fatalf("荣和失败: %v", err)
	}
	if res.Events[0].Kind != EvWin {
		t.Fatal("应产生和牌事件")
	}
	// 计分: 基础1番(2号无门清? 2号无副露+荣和: 门清+1番) = 2番=4分; 放炮者-4。
	// 2号手牌无副露, 荣和不破坏门清(打出的牌不计副露) -> 门清+1 -> 2番。
	scores := m.Scores()
	if scores[2] != 4 {
		t.Fatalf("赢家应 +4: %v", scores)
	}
	if scores[0] != -4 {
		t.Fatalf("放炮者应 -4: %v", scores)
	}
	if scores[1] != 0 || scores[3] != 0 {
		t.Fatalf("无关家应为 0: %v", scores)
	}
}

// TestChowFlow 吃流程: 仅上家可吃。
func TestChowFlow(t *testing.T) {
	rules := DefaultRuleSet()
	// 0 号打 wan4; 1 号(下家)持 wan3 wan5 可吃; 2 号(非下家)持同搭子不可吃。
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan5", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("wan3", "wan5", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong1", "tong3", "tong5", "tong7", "dong", "nan", "xi"),
		kinds("wan3", "wan5", "wan2", "wan4", "wan6", "wan8", "tiao2", "tiao4", "tiao6",
			"tong2", "tong4", "tong6", "bei"),
		kinds("wan9", "wan9", "wan9", "tiao7", "tiao8", "tiao9", "tong7", "tong8", "tong9",
			"tiao1", "tiao2", "dong", "xi"),
	}
	m := matchWithHands(t, rules, 0, hands, KindWan4) // 庄家第 14 张摸 wan4 -> 打出
	m.Discard(0, KindWan4)
	// 2 号(非下家)吃应被拒绝。
	if _, err := m.Respond(2, RespondAction{Chow: &ChowCombo{With: [2]TileKind{KindWan3, KindWan5}}}); err == nil {
		t.Fatal("非上家关系吃应被拒绝")
	}
	// 1 号吃(wan3+wan5+wan4)。
	if _, err := m.Respond(1, RespondAction{Chow: &ChowCombo{With: [2]TileKind{KindWan3, KindWan5}}}); err != nil {
		t.Fatalf("吃失败: %v", err)
	}
	v1 := m.ViewFor(1)
	if len(v1.Melds) != 1 || v1.Melds[0].Kind != "chow" {
		t.Fatalf("应有吃副露: %+v", v1.Melds)
	}
	if v1.Melds[0].Tiles[0] != "wan3" || v1.Melds[0].Tiles[2] != "wan5" {
		t.Fatalf("顺子应为 wan3,4,5: %v", v1.Melds[0].Tiles)
	}
}

// TestDuplicateRespond 重复响应拒绝。
func TestDuplicateRespond(t *testing.T) {
	rules := DefaultRuleSet()
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong"),
		kinds("bei", "bei", "wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5",
			"tong1", "tong3", "tong5", "dong"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "bei"),
		kinds("wan9", "wan9", "wan9", "tiao7", "tiao8", "tiao9", "tong7", "tong8", "tong9",
			"tiao1", "tiao2", "nan", "xi"),
	}
	m := matchWithHands(t, rules, 0, hands, KindNorth) // 庄家第 14 张摸 bei -> 打出
	m.Discard(0, KindNorth)
	// 1 号碰(手牌两张 bei)。
	if _, err := m.Respond(1, RespondAction{Pong: true}); err != nil {
		t.Fatalf("碰失败: %v", err)
	}
	// 窗口已关, 再响应报阶段错误。
	if _, err := m.Respond(1, RespondAction{Pass: true}); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("窗口关闭后再响应应报错: %v", err)
	}
	// 1 号打牌重开窗口: 对 wan2 其余人没有选择, 窗口立即自动关闭。
	m.Discard(1, KindWan2)
	if _, err := m.Respond(2, RespondAction{Pass: true}); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("自动过牌后窗口应关闭, 再响应应报阶段错误: %v", err)
	}
	// 重复响应保护: 白盒构造一个仍在等待 2 号的窗口。
	m.turnStage = stageRespond
	m.respPending = &pendingDiscard{by: 1, tile: KindWan2}
	m.respAnswered = [4]bool{}
	m.respAnswered[2] = true
	if _, err := m.Respond(2, RespondAction{Pass: true}); !errors.Is(err, ErrAlreadyResponded) {
		t.Fatalf("重复 pass 应报 ErrAlreadyResponded: %v", err)
	}
}

// TestAutoPassKeepsResponder 有真实选择的人不会被自动过牌, 无选择的人被自动跳过。
func TestAutoPassKeepsResponder(t *testing.T) {
	rules := DefaultRuleSet()
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong"),
		kinds("bei", "bei", "wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5",
			"tong1", "tong3", "tong5", "dong"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan"),
		kinds("wan9", "wan9", "wan9", "tiao7", "tiao8", "tiao9", "tong7", "tong8", "tong9",
			"tiao1", "tiao2", "nan", "xi"),
	}
	m := matchWithHands(t, rules, 0, hands, KindNorth) // 庄家第 14 张 bei, 打出
	res, err := m.Discard(0, KindNorth)
	if err != nil {
		t.Fatalf("打牌失败: %v", err)
	}
	// 窗口仍然开放(1 号可以碰), 只给 1 号显示「碰 + 过」。
	if m.turnStage != stageRespond || m.PendingSeat() != 0 {
		t.Fatalf("1 号可碰时应保留响应窗口: stage=%s pending=%d", m.turnStage, m.PendingSeat())
	}
	acts1 := m.ViewFor(1).Actions
	if !hasAction(acts1, "pong") || !hasAction(acts1, "pass") {
		t.Fatalf("1 号应看到碰与过: %v", acts1)
	}
	for _, seat := range []Seat{2, 3} {
		if acts := m.ViewFor(seat).Actions; len(acts) != 0 {
			t.Fatalf("%d 号无可响应动作, 应自动跳过: %v", seat, acts)
		}
	}
	if len(res.Events) != 1 || res.Events[0].Kind != EvDiscard {
		t.Fatalf("窗口未关闭前不应产生 pass 事件: %+v", res.Events)
	}
	// 1 号放弃碰: 窗口收敛, 追加一条 pass 事件并轮转到 1 号摸牌。
	res, err = m.Respond(1, RespondAction{Pass: true})
	if err != nil {
		t.Fatalf("过失败: %v", err)
	}
	if len(res.Events) != 1 || res.Events[0].Kind != EvPass || res.Events[0].Seat != 1 {
		t.Fatalf("放弃碰后应产生 pass 事件: %+v", res.Events)
	}
	if m.TurnSeat() != 1 || m.turnStage != stageDraw {
		t.Fatalf("应轮到 1 号摸牌: turn=%d stage=%s", m.TurnSeat(), m.turnStage)
	}
}

// TestConcealedKong 暗杠流程。
func TestConcealedKong(t *testing.T) {
	rules := DefaultRuleSet()
	// 全局 wan6 ≤ 4: 0号手牌不配 wan6, 用 wan2 顺位替换。
	hands := [4][]TileKind{
		kinds("wan1", "wan1", "wan1", "wan1", "wan2", "wan3", "wan4", "wan5", "wan6",
			"wan7", "wan8", "wan9", "dong"),
		kinds("wan5", "wan5", "wan5", "wan6", "wan6", "tiao1", "tiao3", "tiao5",
			"tong1", "tong3", "tong5", "tong7", "dong"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao5", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan"),
		kinds("wan9", "wan9", "wan9", "tiao7", "tiao8", "tiao9", "tong7", "tong8", "tong9",
			"tiao1", "tiao2", "bei", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindTiao1) // 庄家第 14 张摸 tiao1
	res, err := m.KongSelf(0, KindWan1, false)
	if err != nil {
		t.Fatalf("暗杠失败: %v", err)
	}
	if res.Events[0].Kind != EvKong {
		t.Fatal("应产生杠事件")
	}
	v0 := m.ViewFor(0)
	if len(v0.Melds) != 1 || v0.Melds[0].Kind != "kong.concealed" {
		t.Fatalf("应有暗杠副露: %+v", v0.Melds)
	}
	// 暗杠后补摸 1 张, 回到 14-4+1=11 张待打。
	if len(v0.Hand) != 11 {
		t.Fatalf("暗杠后应 11 张手牌: %d", len(v0.Hand))
	}
	// 暗杠牌面公开(四人麻将常规: 暗杠需亮明四张)。
	v1 := m.ViewFor(1)
	if len(v1.OthersMelds[0]) != 1 || v1.OthersMelds[0][0].Kind != "kong.concealed" {
		t.Fatalf("他人应见暗杠副露: %+v", v1.OthersMelds[0])
	}
}

// TestRandomMatches 随机对局属性测试: 随机打牌推进到终局, 验证不变量。
func TestRandomMatches(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		seed := seed
		t.Run("seed", func(t *testing.T) {
			rules := DefaultRuleSet()
			m, err := NewMatch(rules, 0, NewSeededSource(seed))
			if err != nil {
				t.Fatal(err)
			}
			// 简单策略: 每回合打第一张手牌; 有自摸就胡; 有碰/杠就碰/杠; 有胡就胡。
			rounds := 0
			for m.Phase() == PhaseTurn && rounds < 1000 {
				rounds++
				// 响应窗口优先处理: 非出牌者依次尝试胡/碰/杠/吃/过。
				if m.turnStage == stageRespond && m.respPending != nil {
					pend := m.respPending
					progressed := false
					for s := 0; s < 4; s++ {
						seat := Seat(s)
						if seat == pend.by || m.respAnswered[seat] {
							continue
						}
						acts := m.AvailableActions(seat)
						if hasAction(acts, "hu.robbing") {
							m.Respond(seat, RespondAction{Win: true})
							progressed = true
							break
						}
						if hasAction(acts, "kong.exposed") {
							m.Respond(seat, RespondAction{Kong: true})
							progressed = true
							break
						}
						if hasAction(acts, "pong") {
							m.Respond(seat, RespondAction{Pong: true})
							progressed = true
							break
						}
						if hasAction(acts, "chow") {
							// 吃需指定搭配。
							for _, c := range chowCandidates(m.players[seat].hand, pend.tile) {
								if _, err := m.Respond(seat, RespondAction{Chow: &ChowCombo{With: c}}); err == nil {
									progressed = true
									break
								}
							}
							if progressed {
								break
							}
						}
					}
					if !progressed {
						// 全部 pass。
						for s := 0; s < 4; s++ {
							seat := Seat(s)
							if seat == pend.by || m.respAnswered[seat] {
								continue
							}
							m.Respond(seat, RespondAction{Pass: true})
						}
					}
					continue
				}
				// 摸牌阶段。
				if m.turnStage == stageDraw {
					if _, err := m.Draw(); err != nil {
						t.Fatalf("摸牌失败: %v", err)
					}
					continue
				}
				// 打牌阶段: 自摸/暗杠/补杠/打牌。
				seat := m.turnSeat
				acts := m.AvailableActions(seat)
				if hasAction(acts, "hu.self") {
					if _, err := m.WinSelf(seat); err != nil {
						t.Fatalf("自摸失败: %v", err)
					}
					continue
				}
				if hasAction(acts, "kong.concealed") {
					// 找 4 张同种。
					for k, c := range kindCounts(m.players[seat].hand) {
						if c >= 4 {
							if _, err := m.KongSelf(seat, TileKind(k), false); err == nil {
								break
							}
						}
					}
					continue
				}
				if hasAction(acts, "kong.added") {
					for _, md := range m.players[seat].melds {
						if md.Kind == MeldPong && countKind(m.players[seat].hand, md.Tiles[0]) >= 1 {
							if _, err := m.KongSelf(seat, md.Tiles[0], true); err == nil {
								break
							}
						}
					}
					continue
				}
				// 打第一张。
				hand := m.players[seat].hand
				if len(hand) == 0 {
					t.Fatal("打牌阶段手牌为空")
				}
				if _, err := m.Discard(seat, hand[0]); err != nil {
					t.Fatalf("打牌失败: %v", err)
				}
			}
			// 终局不变量。
			if m.Phase() != PhaseFinished {
				t.Fatalf("应在 1000 回合内终局: %s (rounds=%d)", m.Phase(), rounds)
			}
			scores := m.Scores()
			sum := 0
			for _, s := range scores {
				sum += s
			}
			if sum != 0 {
				t.Fatalf("四人零和不变量被破坏: %v (sum=%d)", scores, sum)
			}
			// 牌数守恒: 手牌+副露×3(杠4)+河 == 发牌总量。
			total := 0
			for i := 0; i < 4; i++ {
				total += len(m.players[i].hand)
				for _, md := range m.players[i].melds {
					if md.Kind == MeldKongExposed || md.Kind == MeldKongConcealed || md.Kind == MeldKongAdded {
						total += 4
					} else {
						total += 3
					}
				}
				total += len(m.discards[i])
			}
			// 和牌者手牌含最后摸/打的牌: 流局时总牌数 = 136 - 剩余墙。
			if m.Winner() < 0 {
				if total+ m.wall.Remaining() != TileTotal {
					t.Fatalf("流局牌数不守恒: 场上%d + 墙%d != 136", total, m.wall.Remaining())
				}
			}
		})
	}
}

func hasAction(acts []string, want string) bool {
	for _, a := range acts {
		if a == want {
			return true
		}
	}
	return false
}

// fixedClock 返回可手动推进的测试时钟。
func fixedClock(t *testing.T, start time.Time) (*time.Time, func() time.Time) {
	t.Helper()
	cur := start
	return &cur, func() time.Time { return cur }
}

// TestRespondCountdownAutoPass 响应倒计时到点后自动"过", 无需玩家点击。
func TestRespondCountdownAutoPass(t *testing.T) {
	rules := DefaultRuleSet()
	rules.RespondMeldSec = 3
	rules.RespondWinSec = 5
	// 1 号两张 tiao5 可碰; 2/3 号无选择(会被自动过)。
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("tiao5", "tiao5", "wan2", "wan4", "wan6", "wan8", "tong1", "tong3", "tong5",
			"tong7", "tong9", "nan", "xi"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan", "bei"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao2", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "xi", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindTiao5)
	base := time.Now()
	_, setClock := fixedClock(t, base)
	m.SetClock(setClock)
	if _, err := m.Discard(0, KindTiao5); err != nil {
		t.Fatalf("打牌失败: %v", err)
	}
	// 只有 1 号（能碰）留在窗口里，其余座位已被自动过。
	acts1 := m.ViewFor(1).Actions
	if !hasAction(acts1, "pong") || !hasAction(acts1, "pass") {
		t.Fatalf("1 号应能碰且能过, 实际 %v", acts1)
	}
	if got := m.ViewFor(2).Actions; len(got) != 0 {
		t.Fatalf("2 号无真实选择, 不应有动作, 实际 %v", got)
	}
	// 倒计时未到: Tick 不推进。
	if _, changed := m.Tick(base.Add(2 * time.Second)); changed {
		t.Fatal("倒计时未到不应推进")
	}
	// 倒计时到点: 自动过, 窗口关闭, 轮到下家摸牌。
	res, changed := m.Tick(base.Add(4 * time.Second))
	if !changed {
		t.Fatal("倒计时到点应自动过")
	}
	if m.TurnSeat() != 1 || m.TurnStage() != "draw" {
		t.Fatalf("自动过后应轮到 1 号摸牌, 实际 seat=%d stage=%s", m.TurnSeat(), m.TurnStage())
	}
	var hasPass bool
	for _, ev := range res.Events {
		if ev.Kind == EvPass {
			hasPass = true
		}
	}
	if !hasPass {
		t.Fatalf("应产生 pass 事件, 实际 %+v", res.Events)
	}
}

// TestRespondCountdownPrefersWin 同一玩家可胡可碰时按胡的较长倒计时计时。
func TestRespondCountdownPrefersWin(t *testing.T) {
	rules := DefaultRuleSet()
	rules.RespondMeldSec = 3
	rules.RespondWinSec = 5
	// 1 号可同时碰与胡 tiao5: 手牌含两张 tiao5, 再摸到第三张即 tiao555 刻子 +
	// 其余 11 张(wan111 wan222 tong111 dong dong)成和, 同时两张 tiao5 也够碰。
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("tiao5", "tiao5", "wan1", "wan1", "wan1", "wan2", "wan2", "wan2",
			"tong1", "tong1", "tong1", "dong", "dong"),
		kinds("wan3", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan", "bei"),
		kinds("wan4", "wan5", "wan6", "wan7", "tiao2", "tiao4", "tiao6", "tiao8",
			"tong2", "tong4", "tong6", "xi", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindTiao5)
	base := time.Now()
	_, setClock := fixedClock(t, base)
	m.SetClock(setClock)
	if _, err := m.Discard(0, KindTiao5); err != nil {
		t.Fatalf("打牌失败: %v", err)
	}
	acts := m.ViewFor(1).Actions
	if !hasAction(acts, "hu.robbing") || !hasAction(acts, "pong") {
		t.Fatalf("1 号应能胡也能碰, 实际 %v", acts)
	}
	// 3 秒(碰的倒计时)时不该过期, 因为取的是胡的 5 秒。
	if _, changed := m.Tick(base.Add(4 * time.Second)); changed {
		t.Fatal("可胡可碰时应按较长的胡倒计时, 4 秒不应过期")
	}
	if _, changed := m.Tick(base.Add(6 * time.Second)); !changed {
		t.Fatal("超过胡倒计时应自动过")
	}
}

// TestDiscardCountdownAutoDiscards 出牌倒计时到点后自动打牌(优先刚摸的牌)。
func TestDiscardCountdownAutoDiscards(t *testing.T) {
	rules := DefaultRuleSet()
	rules.DiscardSec = 5
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("tiao5", "tiao5", "wan2", "wan4", "wan6", "wan8", "tong1", "tong3", "tong5",
			"tong7", "tong9", "nan", "xi"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan", "bei"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao2", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "xi", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindTiao5)
	base := time.Now()
	_, setClock := fixedClock(t, base)
	m.SetClock(setClock)
	// 庄家开局即待出牌, 且第 14 张(刚摸的)是 tiao5。
	if _, changed := m.Tick(base.Add(4 * time.Second)); changed {
		t.Fatal("出牌倒计时未到不应自动打牌")
	}
	res, changed := m.Tick(base.Add(6 * time.Second))
	if !changed {
		t.Fatal("出牌倒计时到点应自动打牌")
	}
	var discarded TileKind
	for _, ev := range res.Events {
		if ev.Kind == EvDiscard {
			discarded = ev.Tiles[0]
		}
	}
	if discarded != KindTiao5 {
		t.Fatalf("超时应优先打刚摸的牌 tiao5, 实际 %s", discarded.Code())
	}
	if m.TurnStage() != "respond" && m.TurnStage() != "draw" {
		t.Fatalf("自动打牌后应进入响应/摸牌阶段, 实际 %s", m.TurnStage())
	}
}

// TestForceDrawWhenNoRespondTimer 关闭响应倒计时时, 下家可用「摸牌」强制收敛窗口。
func TestForceDrawWhenNoRespondTimer(t *testing.T) {
	rules := DefaultRuleSet()
	rules.RespondMeldSec = 0
	rules.RespondWinSec = 0
	rules.DiscardSec = 0
	if rules.RespondEnabled() {
		t.Fatal("两个响应倒计时都为 0 时应视为未启用")
	}
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("tiao5", "tiao5", "wan2", "wan4", "wan6", "wan8", "tong1", "tong3", "tong5",
			"tong7", "tong9", "nan", "xi"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan", "bei"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao2", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "xi", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindTiao5)
	if _, err := m.Discard(0, KindTiao5); err != nil {
		t.Fatalf("打牌失败: %v", err)
	}
	// 1 号能碰: 窗口保持, 下家(1 号)视图里有 forceDraw 标记。
	if !m.ViewFor(1).ForceDraw {
		t.Fatal("未启用倒计时时, 下家应看到 forceDraw")
	}
	if m.ViewFor(2).ForceDraw {
		t.Fatal("非下家不应看到 forceDraw")
	}
	// 非下家强制摸牌应被拒。
	if _, err := m.ForceDraw(2); err == nil {
		t.Fatal("非下家调用 ForceDraw 应被拒绝")
	}
	res, err := m.ForceDraw(1)
	if err != nil {
		t.Fatalf("下家强制摸牌失败: %v", err)
	}
	var hasDraw bool
	for _, ev := range res.Events {
		if ev.Kind == EvDraw {
			hasDraw = true
		}
	}
	if !hasDraw {
		t.Fatalf("强制摸牌应直接摸一张, 实际 %+v", res.Events)
	}
	if m.TurnSeat() != 1 || m.TurnStage() != "discard" {
		t.Fatalf("强制摸牌后应轮到 1 号出牌, 实际 seat=%d stage=%s", m.TurnSeat(), m.TurnStage())
	}
}

// TestClaimStatesPendingSuccessFailed 判定中/判定成功/判定失败的状态流转。
func TestClaimStatesPendingSuccessFailed(t *testing.T) {
	rules := DefaultRuleSet()
	rules.RobWin = true
	hands := [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("tiao5", "tiao5", "wan2", "wan4", "wan6", "wan8", "tong1", "tong3", "tong5",
			"tong7", "tong9", "nan", "xi"),
		kinds("wan1", "wan2", "wan3", "tiao4", "tiao6", "tiao7", "tiao8", "tiao9",
			"tong1", "tong2", "tong3", "dong", "dong"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao1", "tiao3", "tiao2", "tiao9",
			"tong2", "tong4", "tong6", "bei", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindTiao5)
	if _, err := m.Discard(0, KindTiao5); err != nil {
		t.Fatalf("打牌失败: %v", err)
	}
	// 1 号碰 → 判定中(2 号还能胡)。
	if _, err := m.Respond(1, RespondAction{Pong: true}); err != nil {
		t.Fatalf("碰失败: %v", err)
	}
	if m.ClaimState(1) != "pending" {
		t.Fatalf("1 号应为判定中, 实际 %q", m.ClaimState(1))
	}
	if states := m.ViewFor(0).ClaimStates; len(states) != 4 || states[1] != "pending" {
		t.Fatalf("视图应透出判定状态, 实际 %v", states)
	}
	// 2 号胡 → 胡成功, 1 号碰失败。
	if _, err := m.Respond(2, RespondAction{Win: true}); err != nil {
		t.Fatalf("胡失败: %v", err)
	}
	if m.ClaimState(2) != "success" {
		t.Fatalf("2 号应为判定成功, 实际 %q", m.ClaimState(2))
	}
	if m.ClaimState(1) != "failed" {
		t.Fatalf("1 号应为判定失败, 实际 %q", m.ClaimState(1))
	}
	if m.Winner() != 2 {
		t.Fatalf("应由 2 号和牌, 实际 %d", m.Winner())
	}
}

// TestRobKongWin 抢杠胡: 补杠时其他家可抢, 抢成则和牌 +2 番, 补杠不成立。
func TestRobKongWin(t *testing.T) {
	rules := DefaultRuleSet()
	rules.RobKongWin = true
	// 0 号(庄)已碰 wan5 且手牌有第 4 张 wan5 可补杠; 2 号听 wan5(wan3 wan4 + wan5)。
	hands := [4][]TileKind{
		kinds("wan5", "wan1", "wan2", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong", "dong"),
		kinds("tiao4", "tiao5", "tiao6", "wan9", "wan9", "tong4", "tong5", "tong6",
			"tiao1", "tiao2", "tiao3", "xi", "xi"),
		kinds("wan3", "wan4", "tiao1", "tiao2", "tiao3", "tong1", "tong2", "tong3",
			"tiao7", "tiao8", "tiao9", "nan", "nan"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao4", "tiao6", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "bei", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindNorth)
	// 注入 0 号已有的 wan5 碰(另 3 张从牌池扣除, 第 4 张在手上)。
	m.players[0].melds = append(m.players[0].melds, Meld{
		Kind: MeldPong, Tiles: [4]TileKind{KindWan5, KindWan5, KindWan5}, From: 1,
	})
	if _, err := m.KongSelf(0, KindWan5, true); err != nil {
		t.Fatalf("补杠(抢杠)失败: %v", err)
	}
	// 应进入抢杠响应窗口, 尚未完成补杠。
	if m.turnStage != stageRespond || m.respPending == nil || !m.respPending.robKong {
		t.Fatalf("应进入抢杠响应窗口: stage=%s pend=%+v", m.turnStage, m.respPending)
	}
	// 2 号抢杠胡。
	if _, err := m.Respond(2, RespondAction{Win: true}); err != nil {
		t.Fatalf("抢杠胡失败: %v", err)
	}
	if m.Winner() != 2 {
		t.Fatalf("应由 2 号抢杠和牌, 实际 %d", m.Winner())
	}
	rv := m.ViewFor(2).Result
	if rv == nil {
		t.Fatal("应有结算")
	}
	if !hasFlag(rv.Flags, "抢杠胡") {
		t.Fatalf("番种应含抢杠胡: %v", rv.Flags)
	}
	// 补杠未完成: 0 号的碰仍是碰。
	for _, md := range m.players[0].melds {
		if md.Kind == MeldKongAdded {
			t.Fatal("被抢杠后不应升级为补杠")
		}
	}
}

// TestRobKongWinDeclined 无人抢杠时补杠照常完成。
func TestRobKongWinDeclined(t *testing.T) {
	rules := DefaultRuleSet()
	rules.RobKongWin = true
	hands := [4][]TileKind{
		kinds("wan5", "wan1", "wan2", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong", "dong"),
		kinds("tiao4", "tiao5", "tiao6", "wan9", "wan9", "tong4", "tong5", "tong6",
			"tiao1", "tiao2", "tiao3", "xi", "xi"),
		// 2 号不再听 wan5。
		kinds("wan1", "wan2", "wan3", "tiao4", "tiao6", "tiao7", "tong7", "tong8", "tong9",
			"nan", "nan", "tiao1", "tiao2"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao4", "tiao6", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "bei", "zhong"),
	}
	m := matchWithHands(t, rules, 0, hands, KindNorth)
	m.players[0].melds = append(m.players[0].melds, Meld{
		Kind: MeldPong, Tiles: [4]TileKind{KindWan5, KindWan5, KindWan5}, From: 1,
	})
	if _, err := m.KongSelf(0, KindWan5, true); err != nil {
		t.Fatalf("补杠失败: %v", err)
	}
	if m.Phase() != PhaseTurn {
		t.Fatalf("应仍在进行中, 实际 %s", m.Phase())
	}
	got := false
	for _, md := range m.players[0].melds {
		if md.Kind == MeldKongAdded {
			got = true
		}
	}
	if !got {
		t.Fatalf("无人抢杠时应升级为补杠: %+v", m.players[0].melds)
	}
	if m.TurnSeat() != 0 || m.TurnStage() != "discard" {
		t.Fatalf("补杠后应轮到 0 号出牌: seat=%d stage=%s", m.TurnSeat(), m.TurnStage())
	}
}

// winHand 构造一副 14 张和牌手牌(四组 + 将)。
func winHand(t *testing.T) []TileKind {
	t.Helper()
	return kinds("wan1", "wan2", "wan3", "tiao1", "tiao2", "tiao3", "tong1", "tong2", "tong3",
		"tiao7", "tiao8", "tiao9", "nan", "nan")
}

// fanTestMatch 构造一副可和牌的对局(仅用于结算番种)。
func fanTestMatch(t *testing.T, rules RuleSet) *Match {
	t.Helper()
	return matchWithHands(t, rules, 0, [4][]TileKind{
		kinds("wan1", "wan2", "wan3", "wan4", "wan6", "wan7", "wan8", "wan9",
			"tiao1", "tiao2", "tiao3", "dong", "dong"),
		kinds("tiao4", "tiao5", "tiao6", "wan9", "wan9", "tong4", "tong5", "tong6",
			"tiao1", "tiao2", "tiao3", "xi", "xi"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao4", "tiao6", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "nan", "bei"),
		kinds("wan2", "wan4", "wan6", "wan8", "tiao4", "tiao6", "tiao7", "tiao9",
			"tong2", "tong4", "tong6", "bei", "zhong"),
	}, KindNorth)
}

// TestKongFlowerFan 杠上开花: 杠后补摸的牌自摸和牌 → 额外 +2 番。
func TestKongFlowerFan(t *testing.T) {
	rules := DefaultRuleSet()
	m := fanTestMatch(t, rules)
	shape, ok := CanWinHand(winHand(t), nil)
	if !ok {
		t.Fatal("构造手牌应可和")
	}
	m.kongFlower = true
	if _, err := m.resolveWin(0, shape, true, -1); err != nil {
		t.Fatal(err)
	}
	rv := m.ViewFor(0).Result
	if !hasFlag(rv.Flags, "杠上开花") {
		t.Fatalf("应计杠上开花: %v", rv.Flags)
	}
	// 基础1 + 自摸1 + 门清1 + 杠上开花2 = 5 番。
	if rv.Fan != 5 {
		t.Fatalf("番数应为 5, 实际 %d (%v)", rv.Fan, rv.Flags)
	}
	if rv.Pay != rules.BaseScore*(1<<5) {
		t.Fatalf("支付额不符: %d", rv.Pay)
	}
}

// TestLastTileFan 海底捞月(自摸) / 河底捞鱼(点炮)。
func TestLastTileFan(t *testing.T) {
	rules := DefaultRuleSet()
	shape, ok := CanWinHand(winHand(t), nil)
	if !ok {
		t.Fatal("构造手牌应可和")
	}
	m := fanTestMatch(t, rules)
	m.lastDrawTile = true
	if _, err := m.resolveWin(0, shape, true, -1); err != nil {
		t.Fatal(err)
	}
	if rv := m.ViewFor(0).Result; !hasFlag(rv.Flags, "海底捞月") {
		t.Fatalf("应计海底捞月: %v", rv.Flags)
	}
	m2 := fanTestMatch(t, rules)
	m2.riverEnd = true
	if _, err := m2.resolveWin(0, shape, false, 1); err != nil {
		t.Fatal(err)
	}
	if rv := m2.ViewFor(0).Result; !hasFlag(rv.Flags, "河底捞鱼") {
		t.Fatalf("应计河底捞鱼: %v", rv.Flags)
	}
}

// TestWinFlagsSwitchable 关闭规则时不追加对应番种。
func TestWinFlagsSwitchable(t *testing.T) {
	rules := DefaultRuleSet()
	rules.KongFlower = false
	shape, ok := CanWinHand(winHand(t), nil)
	if !ok {
		t.Fatal("构造手牌应可和")
	}
	m := fanTestMatch(t, rules)
	m.kongFlower = true
	if _, err := m.resolveWin(0, shape, true, -1); err != nil {
		t.Fatal(err)
	}
	if rv := m.ViewFor(0).Result; hasFlag(rv.Flags, "杠上开花") {
		t.Fatalf("关闭规则后不应计杠上开花: %v", rv.Flags)
	}
}

// hasFlag 判断番种列表是否包含指定项。
func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// TestRuleSetTimingValidation 计时规则的范围校验。
func TestRuleSetTimingValidation(t *testing.T) {
	base := DefaultRuleSet()
	cases := []struct {
		name  string
		patch func(*RuleSet)
		ok    bool
	}{
		{"默认合法", func(r *RuleSet) {}, true},
		{"碰倒计时 0", func(r *RuleSet) { r.RespondMeldSec = 0 }, true},
		{"碰倒计时 31", func(r *RuleSet) { r.RespondMeldSec = 31 }, false},
		{"胡倒计时 -1", func(r *RuleSet) { r.RespondWinSec = -1 }, false},
		{"出牌倒计时 0", func(r *RuleSet) { r.DiscardSec = 0 }, true},
		{"出牌倒计时 2", func(r *RuleSet) { r.DiscardSec = 2 }, false},
		{"出牌倒计时 120", func(r *RuleSet) { r.DiscardSec = 120 }, true},
		{"出牌倒计时 121", func(r *RuleSet) { r.DiscardSec = 121 }, false},
	}
	for _, c := range cases {
		r := base
		c.patch(&r)
		err := r.Validate()
		if c.ok && err != nil {
			t.Fatalf("%s: 应通过校验, 实际 %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s: 应校验失败", c.name)
		}
	}
}
