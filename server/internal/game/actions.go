package game

import (
	"fmt"
	"sort"
	"time"
)

// ActionResult 一次合法动作的结果(房间层据此生成事件与投影)。
type ActionResult struct {
	Events []EngineEvent
}

// EngineEvent 引擎产生的原始事件(房间层转换为带 seq 的协议事件)。
type EngineEvent struct {
	Kind    string   // 事件类型(见下方常量)
	Seat    Seat     // 相关座位(广播事件)
	Tiles   []TileKind // 涉及的公开牌
	Extra   map[string]any
}

// 引擎事件类型。
const (
	EvDealt       = "dealt"        // 发牌完成(每人张数)
	EvDraw        = "draw"         // 某座位摸牌(牌内容仅本人事件可见)
	EvDiscard     = "discard"      // 某座位打出牌
	EvChow        = "chow"         // 吃
	EvPong        = "pong"         // 碰
	EvKong        = "kong"         // 杠(明/暗/补)
	EvWin         = "win"          // 和牌
	EvDrawWall    = "draw_wall"    // 流局
	EvPass        = "pass"         // 响应窗口关闭(全员过)
	EvJudge       = "judge"        // 抢牌判定状态(判定中/判定成功/判定失败)
)

// Draw 当前座位摸牌。仅在 stageDraw 阶段合法。
func (m *Match) Draw() (ActionResult, error) {
	if m.phase != PhaseTurn {
		return ActionResult{}, phaseErr("摸牌", m.phase)
	}
	if m.turnStage != stageDraw {
		return ActionResult{}, fmt.Errorf("%w: 当前为打牌阶段", ErrWrongPhase)
	}
	seat := m.turnSeat
	t, ok := m.wall.Draw()
	if !ok {
		// 牌墙摸尽: 流局。
		return m.finishDrawWall()
	}
	p := m.players[seat]
	p.hand = append(p.hand, t.Kind())
	p.handIDs = append(p.handIDs, t)
	sort.Slice(p.hand, func(i, j int) bool { return p.hand[i] < p.hand[j] })
	m.turnDrawn = t.Kind()
	m.turnStage = stageDiscard
	// 时序番种标记: 普通摸牌不是杠后补牌; 摸走牌墙最后一张记「海底」。
	m.kongFlower = false
	m.lastDrawTile = m.wall.Remaining() == 0
	m.armDiscardDeadlineLocked()
	// 自摸判定标记。
	_, canWin := CanWinHand(p.hand, p.melds)
	p.selfDraw = canWin && m.rules.SelfDraw
	return ActionResult{Events: []EngineEvent{{Kind: EvDraw, Seat: seat, Tiles: []TileKind{t.Kind()}}}}, nil
}

// Discard 打出一张手牌。仅在 stageDiscard 且是当前行动者本人时合法。
// 打出后开放响应窗口(胡/碰/明杠/上家吃)。
func (m *Match) Discard(seat Seat, tile TileKind) (ActionResult, error) {
	if m.phase != PhaseTurn {
		return ActionResult{}, phaseErr("打牌", m.phase)
	}
	if m.turnStage != stageDiscard {
		return ActionResult{}, fmt.Errorf("%w: 当前为响应等待阶段", ErrWrongPhase)
	}
	if seat != m.turnSeat {
		return ActionResult{}, ErrNotYourTurn
	}
	idx := indexOfKind(m.players[seat].hand, tile)
	if idx < 0 {
		return ActionResult{}, ErrTileNotInHand
	}
	p := m.players[seat]
	p.hand = append(p.hand[:idx], p.hand[idx+1:]...)
	p.handIDs = append(p.handIDs[:idx], p.handIDs[idx+1:]...)
	m.discards[seat] = append(m.discards[seat], tile)
	m.discardLog = append(m.discardLog, DiscardEntry{Seat: seat, Tile: tile})
	m.lastDiscardSeat = seat
	m.turnDrawn = 0
	m.turnStage = stageRespond
	m.discardDeadline = time.Time{}
	// 河底捞鱼: 牌墙已空时的最后一张出牌, 荣和时额外计番。
	m.riverEnd = m.wall.Remaining() == 0
	m.respPending = &pendingDiscard{by: seat, tile: tile}
	// 打开响应窗口: 为每个"确实有选择"的座位设独立倒计时, 无选择者直接视为已过。
	m.resetResponseLocked()
	res := ActionResult{Events: []EngineEvent{{Kind: EvDiscard, Seat: seat, Tiles: []TileKind{tile}}}}
	// 关键体验: 对这张牌没有胡/碰/杠/吃选择的座位直接视为"过", 无需人工确认。
	// 若所有人都无选择, 窗口立即收敛并轮转给下家。
	m.autoPassLocked(&res)
	return res, nil
}

// resetResponseLocked 打开新响应窗口: 清空响应状态, 按规则为每个
// "确实有选择"的座位设置独立倒计时, 并把无选择的座位直接标记为已过。
func (m *Match) resetResponseLocked() {
	n := m.rules.Players
	now := m.now()
	for i := 0; i < 4; i++ {
		m.respAnswered[i] = false
		m.respClaims[i] = nil
		m.respDeadlines[i] = time.Time{}
		m.claimState[i] = ""
		m.claimStateID[i] = 0
	}
	for i := 0; i < n; i++ {
		seat := Seat(i)
		if seat == m.respPending.by {
			continue
		}
		acts := m.respondActions(seat)
		if len(acts) == 0 {
			m.respAnswered[i] = true
			continue
		}
		// 同一玩家同时可胡可碰: 取最长倒计时(胡的秒数), 避免碰的短倒计时
		// 先把高优先级的胡动作吞掉。
		sec := m.rules.ResponseSecondsFor(acts)
		// 抢杠胡窗口: 即使规则关闭了响应倒计时, 也必须给一个有限窗口,
		// 否则没人抢时补杠永远挂起(该窗口没有「摸牌」按钮可点)。
		if m.respPending != nil && m.respPending.robKong && sec <= 0 {
			sec = 5
		}
		if sec > 0 {
			m.respDeadlines[i] = now.Add(time.Duration(sec) * time.Second)
		}
	}
}

// autoPassLocked 把"没有任何真实响应选项"的座位标记为已响应。
// 只有存在真实选项(能胡/碰/杠/吃)的座位才会留在响应窗口里等待决策,
// 因此自动通过不会替玩家放弃任何可行动作。
// 全部无选择时收敛窗口并立即推进到下家摸牌, 并追加对应的 pass 事件。
func (m *Match) autoPassLocked(res *ActionResult) {
	if m.phase != PhaseTurn || m.turnStage != stageRespond || m.respPending == nil {
		return
	}
	settled, done := m.settleLocked()
	if done {
		res.Events = append(res.Events, settled.Events...)
	}
}

// RespondAction 响应窗口内的动作请求。
type RespondAction struct {
	Pass  bool       // 过
	Win   bool       // 和(荣和)
	Pong  bool       // 碰
	Kong  bool       // 明杠
	Chow  *ChowCombo // 吃(指定搭配)
}

// ChowCombo 吃牌的搭配(上家打出的牌 + 自家两张)。
type ChowCombo struct {
	With [2]TileKind
}

// indexOfKind 在种类切片中查找(返回下标或 -1)。
func indexOfKind(hand []TileKind, k TileKind) int {
	for i, h := range hand {
		if h == k {
			return i
		}
	}
	return -1
}

// respondClaimOf 把动作请求解析为待仲裁的抢牌请求; 非法时返回错误。
func (m *Match) respondClaimOf(seat Seat, act RespondAction, pend *pendingDiscard) (*respondClaim, error) {
	if act.Win {
		if !m.rules.DiscardWin {
			return nil, fmt.Errorf("%w: 规则禁止荣和", ErrWinNotAllowed)
		}
		concealed := append(append([]TileKind{}, m.players[seat].hand...), pend.tile)
		shape, ok := CanWinHand(concealed, m.players[seat].melds)
		if !ok {
			return nil, fmt.Errorf("%w: 牌型不构成和牌", ErrIllegalAction)
		}
		return &respondClaim{pri: priWin, sub: "hu.robbing", shape: shape}, nil
	}
	if act.Pong {
		if count := countKind(m.players[seat].hand, pend.tile); count < 2 {
			return nil, fmt.Errorf("%w: 手牌不足两张同种", ErrIllegalMeld)
		}
		return &respondClaim{pri: priMeld, sub: "pong"}, nil
	}
	if act.Kong {
		if count := countKind(m.players[seat].hand, pend.tile); count < 3 {
			return nil, fmt.Errorf("%w: 手牌不足三张同种", ErrIllegalMeld)
		}
		return &respondClaim{pri: priMeld, sub: "kong.exposed"}, nil
	}
	if act.Chow != nil {
		if !m.rules.Chow {
			return nil, fmt.Errorf("%w: 规则禁止吃", ErrWinNotAllowed)
		}
		if !m.isLeftOf(seat, pend.by) {
			return nil, fmt.Errorf("%w: 只能吃上家的牌", ErrIllegalChow)
		}
		run, ok := chowRun(pend.tile, act.Chow.With[0], act.Chow.With[1])
		if !ok {
			return nil, fmt.Errorf("%w: 不构成顺子", ErrIllegalChow)
		}
		for _, w := range act.Chow.With {
			if countKind(m.players[seat].hand, w) < 1 {
				return nil, ErrTileNotInHand
			}
		}
		return &respondClaim{pri: priChow, sub: "chow", run: run, with: act.Chow.With}, nil
	}
	return nil, nil
}

// Respond 响应窗口动作仲裁。
//
// 优先级: 胡 > 碰/明杠 > 吃。提交后不会立刻执行, 而是先进入仲裁:
//   - 仍有更高优先级(或同优先级但按出牌顺序更靠前)的座位未表态 → 保持"判定中";
//   - 否则按优先级/顺序/先到先得执行最优的那一个, 其余提示"判定失败"。
//
// 返回结果时窗口可能仍开放(等待其余玩家), 房间层据此广播。
func (m *Match) Respond(seat Seat, act RespondAction) (ActionResult, error) {
	if m.phase != PhaseTurn {
		return ActionResult{}, phaseErr("响应", m.phase)
	}
	if m.turnStage != stageRespond || m.respPending == nil {
		return ActionResult{}, fmt.Errorf("%w: 无待响应的出牌", ErrWrongPhase)
	}
	if seat == m.respPending.by {
		return ActionResult{}, ErrNotResponder
	}
	if m.respAnswered[seat] {
		return ActionResult{}, ErrAlreadyResponded
	}
	pend := m.respPending
	// 空动作等价于「过」: 兼容只发 {} 表示放弃的调用方(含旧客户端与测试)。
	if act.Pass || (!act.Win && !act.Pong && !act.Kong && act.Chow == nil) {
		m.respAnswered[seat] = true
		m.respClaims[seat] = nil
		m.respDeadlines[seat] = time.Time{}
		settled, _ := m.settleLocked()
		return settled, nil
	}
	claim, err := m.respondClaimOf(seat, act, pend)
	if err != nil {
		return ActionResult{}, err
	}
	if claim == nil {
		return ActionResult{}, fmt.Errorf("%w: 未知响应动作", ErrIllegalAction)
	}
	m.claimSeq++
	claim.seq = m.claimSeq
	m.respClaims[seat] = claim
	m.respAnswered[seat] = true
	m.respDeadlines[seat] = time.Time{}
	m.setClaimStateLocked(seat, "pending")
	settled, done := m.settleLocked()
	if done {
		// 已当场仲裁出结果: settleLocked 已包含「判定成功/失败」与执行事件。
		return settled, nil
	}
	// 仍在等更高优先级(或更靠前顺位)的座位表态: 只回报"判定中"。
	return ActionResult{Events: []EngineEvent{m.judgeEvent(seat, "pending")}}, nil
}

// setClaimStateLocked 更新某座位的抢牌判定状态并递增版本号(前端一次性提示)。
func (m *Match) setClaimStateLocked(seat Seat, state string) {
	if m.claimState[seat] == state {
		return
	}
	m.claimState[seat] = state
	m.claimStateID[seat]++
}

// judgeEvent 构造判定状态事件(前端据此弹出「判定中/判定成功/判定失败」)。
func (m *Match) judgeEvent(seat Seat, state string) EngineEvent {
	return EngineEvent{Kind: EvJudge, Seat: seat, Extra: map[string]any{"judge": state}}
}

// claimDistance 座位相对出牌者的顺位距离(1=下家)。
func (m *Match) claimDistance(seat, by Seat) int {
	n := m.rules.Players
	return (int(seat) - int(by) + n) % n
}

// robEnabled 该优先级是否启用"按出牌顺序优先"的截牌规则。
func (m *Match) robEnabled(pri int) bool {
	if pri == priWin {
		return m.rules.RobWin
	}
	return m.rules.RobMeld
}

// settleLocked 仲裁当前响应窗口。
// 返回 (事件, 是否已收敛)。未收敛表示仍在等待某些座位表态。
func (m *Match) settleLocked() (ActionResult, bool) {
	if m.respPending == nil {
		return ActionResult{}, true
	}
	n := m.rules.Players
	by := m.respPending.by

	// 1) 选出当前最优的已提交请求。
	best := Seat(-1)
	bestPri := 0
	var bestClaim *respondClaim
	for i := 0; i < n; i++ {
		c := m.respClaims[i]
		if c == nil {
			continue
		}
		seat := Seat(i)
		if best < 0 {
			best, bestPri, bestClaim = seat, c.pri, c
			continue
		}
		better := false
		switch {
		case c.pri > bestPri:
			better = true
		case c.pri == bestPri:
			if m.robEnabled(c.pri) {
				better = m.claimDistance(seat, by) < m.claimDistance(best, by)
			} else {
				better = c.seq < bestClaim.seq ||
					(c.seq == bestClaim.seq && m.claimDistance(seat, by) < m.claimDistance(best, by))
			}
		}
		if better {
			best, bestPri, bestClaim = seat, c.pri, c
		}
	}

	// 2) 检查是否还有座位可能"截"掉当前最优请求。
	if best >= 0 {
		for i := 0; i < n; i++ {
			if m.respAnswered[i] || Seat(i) == by {
				continue
			}
			pot := priorityOf(m.respondActions(Seat(i)))
			if pot == 0 {
				continue
			}
			if pot > bestPri {
				return ActionResult{}, false // 更高优先级尚未表态
			}
			if pot == bestPri && m.robEnabled(pot) &&
				m.claimDistance(Seat(i), by) < m.claimDistance(best, by) {
				return ActionResult{}, false // 同优先级但顺位更靠前, 需等待
			}
		}
	}

	// 3) 无请求且全员已表态: 关闭窗口, 下家摸牌。
	if best < 0 {
		for i := 0; i < n; i++ {
			if Seat(i) != by && !m.respAnswered[i] {
				return ActionResult{}, false
			}
		}
		return m.closeWindowLocked(), true
	}

	// 4) 执行最优请求, 其余请求判负。
	// 事件顺序: 先"判定失败"提示, 再执行动作本身, 最后"判定成功"——
	// 前端按序播放时, 失败提示不会盖住真正的动作事件。
	var events []EngineEvent
	for i := 0; i < n; i++ {
		if m.respClaims[i] == nil || Seat(i) == best {
			continue
		}
		m.setClaimStateLocked(Seat(i), "failed")
		events = append(events, m.judgeEvent(Seat(i), "failed"))
	}
	m.setClaimStateLocked(best, "success")
	res, err := m.executeClaimLocked(best, bestClaim)
	if err != nil {
		return ActionResult{}, false
	}
	events = append(events, res.Events...)
	events = append(events, m.judgeEvent(best, "success"))
	return ActionResult{Events: events}, true
}

// priorityOf 一组动作里的最高优先级(0=没有真实选择)。
func priorityOf(acts []string) int {
	best := 0
	for _, a := range acts {
		switch a {
		case "hu.robbing":
			if best < priWin {
				best = priWin
			}
		case "pong", "kong.exposed":
			if best < priMeld {
				best = priMeld
			}
		case "chow":
			if best < priChow {
				best = priChow
			}
		}
	}
	return best
}

// executeClaimLocked 执行已仲裁通过的抢牌请求。
func (m *Match) executeClaimLocked(seat Seat, c *respondClaim) (ActionResult, error) {
	pend := m.respPending
	switch c.sub {
	case "hu.robbing":
		robKong := pend.robKong
		m.pendingKong = nil
		m.clearResponseLocked()
		// 抢杠胡: 被抢的是补杠者, 记入番种标记。
		m.winRobKong = robKong
		return m.resolveWin(seat, c.shape, false, pend.by)
	case "pong":
		return m.resolvePong(seat, pend)
	case "kong.exposed":
		return m.resolveKongExposed(seat, pend)
	case "chow":
		return m.resolveChow(seat, pend, c.run)
	}
	return ActionResult{}, fmt.Errorf("%w: 未知抢牌类型", ErrIllegalAction)
}

// clearResponseLocked 收敛响应窗口(保留判定状态供前端提示)。
func (m *Match) clearResponseLocked() {
	m.respPending = nil
	for i := 0; i < 4; i++ {
		m.respClaims[i] = nil
		m.respDeadlines[i] = time.Time{}
		m.respAnswered[i] = false
	}
}

// closeWindowLocked 全员过: 关闭窗口, 下家摸牌。
// 若窗口来自「抢杠胡」(补杠被挂起), 无人抢则补杠照常完成, 由杠者继续打牌。
func (m *Match) closeWindowLocked() ActionResult {
	if m.pendingKong != nil {
		pk := m.pendingKong
		m.pendingKong = nil
		by := m.respPending.by
		m.clearResponseLocked()
		_ = by
		res, err := m.executeAddedKongLocked(pk.seat, pk.tile)
		if err != nil {
			// 理论上不可达: 前置校验已通过。退化为直接推进到下家摸牌。
			m.turnSeat = Seat((int(pk.seat) + 1) % m.rules.Players)
			m.turnStage = stageDraw
			return ActionResult{Events: []EngineEvent{{Kind: EvPass, Seat: m.turnSeat}}}
		}
		return res
	}
	by := m.respPending.by
	seat := Seat((int(by) + 1) % m.rules.Players)
	m.clearResponseLocked()
	m.turnSeat = seat
	m.turnStage = stageDraw
	m.discardDeadline = time.Time{}
	return ActionResult{Events: []EngineEvent{{Kind: EvPass, Seat: seat}}}
}

// ForceDraw 下家主动「摸牌」强制收敛响应窗口(仅在未启用响应倒计时时开放)。
// 语义: 一旦下家摸牌, 其他玩家视为全部放弃碰/杠/胡。
func (m *Match) ForceDraw(seat Seat) (ActionResult, error) {
	if m.phase != PhaseTurn {
		return ActionResult{}, phaseErr("摸牌", m.phase)
	}
	if m.turnStage != stageRespond || m.respPending == nil {
		return ActionResult{}, fmt.Errorf("%w: 无待响应的出牌", ErrWrongPhase)
	}
	// 抢杠胡窗口有自己的固定倒计时, 不允许用「摸牌」抢先收敛
	// (否则下家一点就替其他家放弃了抢杠机会)。
	if m.respPending.robKong {
		return ActionResult{}, fmt.Errorf("%w: 抢杠判定中, 请等待", ErrIllegalAction)
	}
	if m.rules.RespondEnabled() {
		return ActionResult{}, fmt.Errorf("%w: 已启用响应倒计时, 请等待倒计时结束", ErrIllegalAction)
	}
	if seat != Seat((int(m.respPending.by)+1)%m.rules.Players) {
		return ActionResult{}, ErrNotYourTurn
	}
	for i := 0; i < 4; i++ {
		if m.respClaims[i] != nil {
			m.setClaimStateLocked(Seat(i), "failed")
		}
	}
	res := m.closeWindowLocked()
	// 关闭后直接替该座位摸牌, 让「摸牌」按钮一次点击即推进。
	drawn, err := m.Draw()
	if err != nil {
		return res, nil
	}
	res.Events = append(res.Events, drawn.Events...)
	return res, nil
}

// Tick 处理超时: 响应倒计时到点自动"过", 出牌倒计时到点自动打牌。
// 返回 (事件, 是否发生变化)。没有计时器时不产生任何变化。
func (m *Match) Tick(now time.Time) (ActionResult, bool) {
	if m.phase != PhaseTurn {
		return ActionResult{}, false
	}
	switch m.turnStage {
	case stageRespond:
		changed := false
		for i := 0; i < m.rules.Players; i++ {
			if m.respAnswered[i] || m.respDeadlines[i].IsZero() {
				continue
			}
			if now.Before(m.respDeadlines[i]) {
				continue
			}
			m.respAnswered[i] = true
			m.respClaims[i] = nil
			m.respDeadlines[i] = time.Time{}
			changed = true
		}
		if !changed {
			return ActionResult{}, false
		}
		res, done := m.settleLocked()
		if !done {
			return ActionResult{Events: []EngineEvent{{Kind: "state"}}}, true
		}
		return res, true
	case stageDiscard:
		if m.discardDeadline.IsZero() || now.Before(m.discardDeadline) {
			return ActionResult{}, false
		}
		seat := m.turnSeat
		tile, ok := m.autoDiscardTile(seat)
		if !ok {
			return ActionResult{}, false
		}
		res, err := m.Discard(seat, tile)
		if err != nil {
			return ActionResult{}, false
		}
		return res, true
	}
	return ActionResult{}, false
}

// autoDiscardTile 出牌超时自动选择: 优先刚摸到的牌(杠/摸牌后), 否则最右边的牌。
func (m *Match) autoDiscardTile(seat Seat) (TileKind, bool) {
	if m.turnDrawn != 0 && indexOfKind(m.players[seat].hand, m.turnDrawn) >= 0 {
		return m.turnDrawn, true
	}
	hand := m.players[seat].hand
	if len(hand) == 0 {
		return 0, false
	}
	return hand[len(hand)-1], true
}

// ResponseDeadlines 各座位响应倒计时截止时刻(零值=无倒计时)。
func (m *Match) ResponseDeadlines() [4]time.Time { return m.respDeadlines }

// DiscardDeadline 出牌倒计时截止时刻(零值=不限时)。
func (m *Match) DiscardDeadline() time.Time { return m.discardDeadline }

// ClaimState 某座位当前抢牌判定状态(""|pending|success|failed)。
func (m *Match) ClaimState(seat Seat) string { return m.claimState[seat] }

// Now 引擎当前时间(供房间层与投影使用)。
func (m *Match) Now() time.Time { return m.now() }

// SetClock 注入测试时钟(传 nil 恢复系统时钟)。
func (m *Match) SetClock(fn func() time.Time) { m.clock = fn }

func (m *Match) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// isLeftOf 报告 seat 是否是 by 的下家(座位顺时针递增)。
func (m *Match) isLeftOf(seat, by Seat) bool {
	return Seat((int(by)+1)%m.rules.Players) == seat
}

// takeFromRiver 从出牌者河中移除最近打出的该牌(被碰/杠/吃取走)。
func (m *Match) takeFromRiver(by Seat, tile TileKind) {
	river := m.discards[by]
	for i := len(river) - 1; i >= 0; i-- {
		if river[i] == tile {
			m.discards[by] = append(river[:i], river[i+1:]...)
			return
		}
	}
}

// resolvePong 结算碰。
func (m *Match) resolvePong(seat Seat, pend *pendingDiscard) (ActionResult, error) {
	p := m.players[seat]
	// 从手牌移除两张同种。
	removed := 0
	for i := 0; i < len(p.hand) && removed < 2; i++ {
		if p.hand[i] == pend.tile {
			p.hand = append(p.hand[:i], p.hand[i+1:]...)
			p.handIDs = append(p.handIDs[:i], p.handIDs[i+1:]...)
			removed++
			i--
		}
	}
	if removed != 2 {
		return ActionResult{}, fmt.Errorf("%w: 内部状态错误", ErrIllegalMeld)
	}
	p.melds = append(p.melds, Meld{
		Kind: MeldPong, Tiles: [4]TileKind{pend.tile, pend.tile, pend.tile}, From: pend.by,
	})
	m.takeFromRiver(pend.by, pend.tile)
	m.respPending = nil
	m.turnSeat = seat
	m.turnStage = stageDiscard
	m.armDiscardDeadlineLocked()
	return ActionResult{Events: []EngineEvent{{
		Kind: EvPong, Seat: seat, Tiles: []TileKind{pend.tile, pend.tile, pend.tile},
		Extra: map[string]any{"from": int(pend.by)},
	}}}, nil
}

// resolveKongExposed 结算明杠。
func (m *Match) resolveKongExposed(seat Seat, pend *pendingDiscard) (ActionResult, error) {
	p := m.players[seat]
	removed := 0
	for i := 0; i < len(p.hand) && removed < 3; i++ {
		if p.hand[i] == pend.tile {
			p.hand = append(p.hand[:i], p.hand[i+1:]...)
			p.handIDs = append(p.handIDs[:i], p.handIDs[i+1:]...)
			removed++
			i--
		}
	}
	if removed != 3 {
		return ActionResult{}, fmt.Errorf("%w: 内部状态错误", ErrIllegalMeld)
	}
	p.melds = append(p.melds, Meld{
		Kind: MeldKongExposed, Tiles: [4]TileKind{pend.tile, pend.tile, pend.tile, pend.tile}, From: pend.by,
	})
	m.takeFromRiver(pend.by, pend.tile)
	m.respPending = nil
	m.turnSeat = seat
	// 明杠后从牌墙末端摸替换牌(基础规则: 杠后补牌)。
	return m.afterKongDraw(seat, EvKong, "kong.exposed", []TileKind{pend.tile, pend.tile, pend.tile, pend.tile}, pend.by)
}

// afterKongDraw 杠后补摸并回到打牌阶段; 若牌墙尽则流局。
// 补摸的这张牌可能构成「杠上开花」, 因此打上 kongFlower 标记;
// 若它同时是牌墙最后一张, 也记「海底」标记。
func (m *Match) afterKongDraw(seat Seat, evKind, sub string, tiles []TileKind, from Seat) (ActionResult, error) {
	events := []EngineEvent{{Kind: evKind, Seat: seat, Tiles: tiles, Extra: map[string]any{
		"kong": sub, "from": int(from),
	}}}
	t, ok := m.wall.Draw()
	if !ok {
		return m.finishDrawWall()
	}
	p := m.players[seat]
	p.hand = append(p.hand, t.Kind())
	p.handIDs = append(p.handIDs, t)
	sort.Slice(p.hand, func(i, j int) bool { return p.hand[i] < p.hand[j] })
	m.turnDrawn = t.Kind()
	m.turnStage = stageDiscard
	m.kongFlower = true
	m.lastDrawTile = m.wall.Remaining() == 0
	m.armDiscardDeadlineLocked()
	// 补牌后重新判定自摸(杠上开花的前提)。
	if _, canWin := CanWinHand(p.hand, p.melds); canWin && m.rules.SelfDraw {
		p.selfDraw = true
	} else {
		p.selfDraw = false
	}
	events = append(events, EngineEvent{Kind: EvDraw, Seat: seat, Tiles: []TileKind{t.Kind()}})
	return ActionResult{Events: events}, nil
}

// resolveChow 结算吃。
func (m *Match) resolveChow(seat Seat, pend *pendingDiscard, run [3]TileKind) (ActionResult, error) {
	p := m.players[seat]
	for _, w := range run {
		if w == pend.tile {
			continue
		}
		idx := indexOfKind(p.hand, w)
		if idx < 0 {
			return ActionResult{}, ErrTileNotInHand
		}
		p.hand = append(p.hand[:idx], p.hand[idx+1:]...)
		p.handIDs = append(p.handIDs[:idx], p.handIDs[idx+1:]...)
	}
	p.melds = append(p.melds, Meld{Kind: MeldChow, Tiles: [4]TileKind{run[0], run[1], run[2]}, From: pend.by})
	m.takeFromRiver(pend.by, pend.tile)
	m.respPending = nil
	m.turnSeat = seat
	m.turnStage = stageDiscard
	m.armDiscardDeadlineLocked()
	return ActionResult{Events: []EngineEvent{{
		Kind: EvChow, Seat: seat, Tiles: run[:], Extra: map[string]any{"from": int(pend.by)},
	}}}, nil
}

// KongSelf 自己回合内的暗杠或补杠(不打牌直接发起)。
//
// 补杠(added)会先开放「抢杠胡」窗口: 其他家若能用这张牌和牌, 可以抢;
// 无人抢时才真正把碰升级为补杠并补摸。暗杠不开放抢杠。
func (m *Match) KongSelf(seat Seat, tile TileKind, added bool) (ActionResult, error) {
	if m.phase != PhaseTurn {
		return ActionResult{}, phaseErr("杠", m.phase)
	}
	if m.turnStage != stageDiscard {
		return ActionResult{}, fmt.Errorf("%w: 响应等待阶段不可杠", ErrWrongPhase)
	}
	if seat != m.turnSeat {
		return ActionResult{}, ErrNotYourTurn
	}
	p := m.players[seat]
	if added {
		// 前置校验: 必须有对应碰牌且手牌持有第 4 张。
		hasPong := false
		for _, md := range p.melds {
			if md.Kind == MeldPong && md.Tiles[0] == tile {
				hasPong = true
				break
			}
		}
		if !hasPong {
			return ActionResult{}, fmt.Errorf("%w: 无对应碰牌可补杠", ErrIllegalMeld)
		}
		if indexOfKind(p.hand, tile) < 0 {
			return ActionResult{}, ErrTileNotInHand
		}
		// 抢杠胡: 有人能用这张牌和牌时, 先挂起补杠并开放响应窗口。
		if m.rules.RobKongWin && m.canRobKong(seat, tile) {
			m.pendingKong = &pendingKong{seat: seat, tile: tile, added: true}
			m.turnStage = stageRespond
			m.discardDeadline = time.Time{}
			m.respPending = &pendingDiscard{by: seat, tile: tile, robKong: true}
			m.resetResponseLocked()
			res, _ := m.settleLocked()
			return res, nil
		}
		return m.executeAddedKongLocked(seat, tile)
	}
	// 暗杠: 手牌 4 张同种。
	if count := countKind(p.hand, tile); count < 4 {
		return ActionResult{}, fmt.Errorf("%w: 手牌不足四张同种", ErrIllegalMeld)
	}
	removed := 0
	for i := 0; i < len(p.hand) && removed < 4; i++ {
		if p.hand[i] == tile {
			p.hand = append(p.hand[:i], p.hand[i+1:]...)
			p.handIDs = append(p.handIDs[:i], p.handIDs[i+1:]...)
			removed++
			i--
		}
	}
	p.melds = append(p.melds, Meld{Kind: MeldKongConcealed, Tiles: [4]TileKind{tile, tile, tile, tile}})
	return m.afterKongDraw(seat, EvKong, "kong.concealed", []TileKind{tile, tile, tile, tile}, -1)
}

// canRobKong 检查除 seat 外是否有人能用 tile 和牌(抢杠胡前提)。
func (m *Match) canRobKong(seat Seat, tile TileKind) bool {
	if !m.rules.DiscardWin {
		return false
	}
	for i := 0; i < m.rules.Players; i++ {
		s := Seat(i)
		if s == seat {
			continue
		}
		p := m.players[s]
		concealed := append(append([]TileKind{}, p.hand...), tile)
		if _, ok := CanWinHand(concealed, p.melds); ok {
			return true
		}
	}
	return false
}

// executeAddedKongLocked 真正执行补杠(抢杠窗口无人抢之后)。
func (m *Match) executeAddedKongLocked(seat Seat, tile TileKind) (ActionResult, error) {
	p := m.players[seat]
	found := false
	for i := range p.melds {
		if p.melds[i].Kind == MeldPong && p.melds[i].Tiles[0] == tile {
			p.melds[i].Kind = MeldKongAdded
			p.melds[i].Tiles[3] = tile
			found = true
			break
		}
	}
	if !found {
		return ActionResult{}, fmt.Errorf("%w: 无对应碰牌可补杠", ErrIllegalMeld)
	}
	idx := indexOfKind(p.hand, tile)
	if idx < 0 {
		return ActionResult{}, ErrTileNotInHand
	}
	p.hand = append(p.hand[:idx], p.hand[idx+1:]...)
	p.handIDs = append(p.handIDs[:idx], p.handIDs[idx+1:]...)
	return m.afterKongDraw(seat, EvKong, "kong.added", []TileKind{tile, tile, tile, tile}, -1)
}

// WinSelf 自己回合内自摸和牌。
func (m *Match) WinSelf(seat Seat) (ActionResult, error) {
	if m.phase != PhaseTurn {
		return ActionResult{}, phaseErr("和牌", m.phase)
	}
	if m.turnStage != stageDiscard {
		return ActionResult{}, fmt.Errorf("%w: 响应等待阶段不可自摸", ErrWrongPhase)
	}
	if seat != m.turnSeat {
		return ActionResult{}, ErrNotYourTurn
	}
	if !m.rules.SelfDraw {
		return ActionResult{}, fmt.Errorf("%w: 规则禁止自摸", ErrWinNotAllowed)
	}
	p := m.players[seat]
	if !p.selfDraw {
		return ActionResult{}, fmt.Errorf("%w: 当前手牌不构成和牌", ErrIllegalAction)
	}
	shape, _ := CanWinHand(p.hand, p.melds)
	return m.resolveWin(seat, shape, true, -1)
}

// resolveWin 结算和牌并计分。
func (m *Match) resolveWin(winner Seat, shape WinShape, byDraw bool, loser Seat) (ActionResult, error) {
	m.phase = PhaseFinished
	m.winner = winner
	m.winShape = shape
	m.winByDraw = byDraw
	m.loser = loser

	fan := 1 // 基础 1 番。
	flags := []string{"和牌"}
	if shape.Kind == WinSevenPairs {
		fan += 2
		flags = append(flags, "七对子")
	}
	if byDraw {
		fan += 1 // 自摸 +1 番。
		flags = append(flags, "自摸")
	}
	// 门清 +1 番(七对必然门清, 不重复计)。
	if shape.Kind != WinSevenPairs && m.isConcealed(winner) {
		fan += 1
		flags = append(flags, "门清")
	}
	// 时序型番种(阶段11): 抢杠胡 / 杠上开花 / 海底捞月·河底捞鱼。
	// 三者互斥地绑定同一次和牌时机, 因此按优先级只计最高的一个。
	switch {
	case m.winRobKong && m.rules.RobKongWin:
		fan += 2
		flags = append(flags, "抢杠胡")
	case byDraw && m.kongFlower && m.rules.KongFlower:
		fan += 2
		flags = append(flags, "杠上开花")
	case byDraw && m.lastDrawTile && m.rules.LastTile:
		fan += 2
		flags = append(flags, "海底捞月")
	case !byDraw && m.riverEnd && m.rules.LastTile:
		fan += 2
		flags = append(flags, "河底捞鱼")
	}
	m.winFan = fan
	pay := m.rules.BaseScore * (1 << fan)
	m.winPay = pay
	m.winFlags = flags
	if m.rules.Players == 4 {
		if byDraw {
			// 自摸: 其余每家各付。
			for i := 0; i < 4; i++ {
				if Seat(i) == winner {
					m.scores[i] += pay * 3
				} else {
					m.scores[i] -= pay
				}
			}
		} else {
			// 点炮: 放炮者全额。
			for i := 0; i < 4; i++ {
				switch Seat(i) {
				case winner:
					m.scores[i] += pay
				case loser:
					m.scores[i] -= pay
				}
			}
		}
	} else {
		// 2/3 人: 自摸其余各付; 点炮放炮者付(下限 1 家)。
		if byDraw {
			payers := m.rules.Players - 1
			m.scores[winner] += pay * payers
			for i := 0; i < m.rules.Players; i++ {
				if Seat(i) != winner {
					m.scores[i] -= pay
				}
			}
		} else {
			m.scores[winner] += pay
			m.scores[loser] -= pay
		}
	}
	events := []EngineEvent{{
		Kind: EvWin, Seat: winner, Tiles: nil,
		Extra: map[string]any{
			"by_draw": byDraw, "loser": int(loser), "fan": fan, "pay": pay,
			"win_kind": string(shape.Kind), "win_flags": flags,
		},
	}}
	return ActionResult{Events: events}, nil
}

// isConcealed 报告座位是否门清(无吃/碰/明杠/补杠)。
func (m *Match) isConcealed(seat Seat) bool {
	for _, md := range m.players[seat].melds {
		if md.isCalled() {
			return false
		}
	}
	return true
}

// finishDrawWall 流局结算(无人和牌, 零赔付)。
func (m *Match) finishDrawWall() (ActionResult, error) {
	m.phase = PhaseFinished
	m.winner = -1
	m.respPending = nil
	return ActionResult{Events: []EngineEvent{{Kind: EvDrawWall}}}, nil
}

// countKind 统计手牌中该种类的张数。
func countKind(hand []TileKind, k TileKind) int {
	n := 0
	for _, h := range hand {
		if h == k {
			n++
		}
	}
	return n
}

// phaseErr 构造阶段错误。
func phaseErr(op string, p Phase) error {
	if p == PhaseFinished {
		return ErrMatchOver
	}
	return fmt.Errorf("%w: 阶段 %s 不接受 %s", ErrWrongPhase, p, op)
}
