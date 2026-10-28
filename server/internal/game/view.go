package game

// SeatView 某座位的完整可见状态(权威端按座位裁剪后交给传输层)。
type SeatView struct {
	Seat             Seat         `json:"seat"`
	Phase            string       `json:"phase"`
	TurnSeat         Seat         `json:"turnSeat"`
	TurnStage        string       `json:"turnStage"` // draw|discard|respond
	WallLeft         int          `json:"wallLeft"`
	Hand             []string     `json:"hand"`             // 仅本人: 暗牌代码(升序)
	Drawn            string       `json:"drawn,omitempty"`  // 仅本人: 本回合摸的牌
	Melds            []MeldView   `json:"melds"`            // 本人的副露
	Discards         [][]string   `json:"discards"`         // 全员河牌(公开)
	DiscardLog       []DiscardEntryView `json:"discardLog"` // 全局出牌流水(公开, 有序)
	OthersHandCounts []int        `json:"othersHandCounts"` // 按座位顺序的他人手牌数
	OthersMelds      [][]MeldView `json:"othersMelds"`      // 按座位顺序的他人副露(公开)
	Actions          []string     `json:"actions"`          // 本人当前可执行的动作代码
	// 响应窗口信息。
	PendingTile string `json:"pendingTile,omitempty"` // 等待响应的牌(公开)
	PendingBy   Seat   `json:"pendingBy,omitempty"`
	// 计时与判定(阶段10)。
	RespondSecLeft  int      `json:"respondSecLeft,omitempty"`  // 本人响应倒计时剩余秒(0=无)
	RespondDeadline int64    `json:"respondDeadline,omitempty"` // 本人响应截止(Unix 毫秒)
	DiscardSecLeft  int      `json:"discardSecLeft,omitempty"`  // 出牌倒计时剩余秒(0=不限时)
	DiscardDeadline int64    `json:"discardDeadline,omitempty"` // 出牌截止(Unix 毫秒)
	ClaimStates     []string `json:"claimStates,omitempty"`     // 各座位抢牌判定状态
	ForceDraw       bool     `json:"forceDraw,omitempty"`       // 可否点「摸牌」强制收敛响应窗口
	// 结算信息(PhaseFinished 时)。
	Result *ResultView `json:"result,omitempty"`
}

// DiscardEntryView 出牌流水的对外投影(牌面用线缆代码, 不泄露物理 ID)。
type DiscardEntryView struct {
	Seat int    `json:"seat"`
	Tile string `json:"tile"`
}

// ResultView 结算投影。
type ResultView struct {
	Winner  int           `json:"winner"` // -1 = 流局
	ByDraw  bool          `json:"byDraw"`
	Loser   int           `json:"loser,omitempty"`
	Fan     int           `json:"fan"`
	Pay     int           `json:"pay"`
	WinKind string        `json:"winKind,omitempty"`
	Flags   []string      `json:"flags,omitempty"` // 命中的番种名称(阶段11)
	Shape   *WinShapeView `json:"shape,omitempty"`
	Scores  []int         `json:"scores"`
}

// ViewFor 生成指定座位的可见状态。
// 安全边界: 他人手牌仅数量; 牌墙仅剩余数; 本人摸牌代码仅本人事件。
func (m *Match) ViewFor(seat Seat) SeatView {
	v := SeatView{
		Seat:      seat,
		Phase:     string(m.phase),
		TurnSeat:  m.turnSeat,
		TurnStage: string(m.turnStage),
		WallLeft:  m.wall.Remaining(),
		Discards:  make([][]string, m.rules.Players),
	}
	// 本人。
	p := m.players[seat]
	v.Hand = tilesToStrings(p.hand)
	if m.turnStage == stageDiscard && m.turnSeat == seat && m.turnDrawn != 0 {
		v.Drawn = m.turnDrawn.Code()
	}
	for _, md := range p.melds {
		v.Melds = append(v.Melds, md.viewFor(true))
	}
	// 他人: 手牌数 + 副露。
	for i := 0; i < m.rules.Players; i++ {
		v.OthersHandCounts = append(v.OthersHandCounts, len(m.players[i].hand))
		var oms []MeldView
		for _, md := range m.players[i].melds {
			oms = append(oms, md.viewFor(false))
		}
		v.OthersMelds = append(v.OthersMelds, oms)
	}
	// 河牌(公开)。
	for i := 0; i < m.rules.Players; i++ {
		v.Discards[i] = tilesToStrings(m.discards[i])
	}
	// 全局出牌流水(公开, 有序): 手机端据此显示「最近 6 张」与历史面板。
	v.DiscardLog = make([]DiscardEntryView, 0, len(m.discardLog))
	for _, d := range m.discardLog {
		v.DiscardLog = append(v.DiscardLog, DiscardEntryView{Seat: int(d.Seat), Tile: d.Tile.Code()})
	}
	// 响应窗口(公开牌面与出牌者)。
	if m.respPending != nil {
		v.PendingTile = m.respPending.tile.Code()
		v.PendingBy = m.respPending.by
	}
	// 计时与判定状态(阶段10)。
	now := m.now()
	if dl := m.respDeadlines[seat]; !dl.IsZero() {
		v.RespondDeadline = dl.UnixMilli()
		if left := int(dl.Sub(now).Seconds()) + 1; left > 0 {
			v.RespondSecLeft = left
		}
	}
	if !m.discardDeadline.IsZero() {
		v.DiscardDeadline = m.discardDeadline.UnixMilli()
		if left := int(m.discardDeadline.Sub(now).Seconds()) + 1; left > 0 {
			v.DiscardSecLeft = left
		}
	}
	// 抢牌判定状态: 仅在有内容时下发, 避免每条事件都携带空数组。
	var claims []string
	for i := 0; i < m.rules.Players; i++ {
		if m.claimState[i] != "" {
			if claims == nil {
				claims = make([]string, m.rules.Players)
			}
			claims[i] = m.claimState[i]
		}
	}
	v.ClaimStates = claims
	// 未启用响应倒计时时, 下家可以用「摸牌」按钮强制收敛窗口。
	// 抢杠胡窗口例外: 它有固定倒计时, 且不能用摸牌替别人放弃抢杠。
	if m.turnStage == stageRespond && m.respPending != nil && !m.rules.RespondEnabled() &&
		!m.respPending.robKong &&
		seat == Seat((int(m.respPending.by)+1)%m.rules.Players) {
		v.ForceDraw = true
	}
	// 可执行动作。
	// 保持 JSON 稳定: 无动作时输出空数组而不是 null(前端按数组消费)。
	if acts := m.AvailableActions(seat); len(acts) > 0 {
		v.Actions = acts
	} else {
		v.Actions = []string{}
	}
	// 结算。
	if m.phase == PhaseFinished {
		rv := &ResultView{
			Winner: int(m.winner), ByDraw: m.winByDraw, Fan: m.fanOf(), Pay: m.payOf(),
			WinKind: string(m.winShape.Kind), Flags: m.winFlags, Scores: m.scores[:m.rules.Players],
		}
		if m.winShape.Kind != "" {
			rv.Shape = new(WinShapeView)
			*rv.Shape = m.winShape.view()
		}
		if !m.winByDraw && m.winner >= 0 {
			rv.Loser = int(m.loser)
		}
		v.Result = rv
	}
	return v
}

// fanOf/payOf 结算元数据快照(投影用)。
// 番数与支付在 resolveWin 时一次性算定(含时序型番种), 投影直接读取,
// 避免两处各算一遍导致结算界面与实际扣分不一致。
func (m *Match) fanOf() int {
	if m.phase != PhaseFinished || m.winner < 0 {
		return 0
	}
	return m.winFan
}

func (m *Match) payOf() int {
	if m.phase != PhaseFinished || m.winner < 0 {
		return 0
	}
	return m.winPay
}

// fan 计算本局番数(与 resolveWin 一致; 保留给测试与内部校验使用)。
func (m *Match) fan() int {
	if m.winFan > 0 {
		return m.winFan
	}
	fan := 1
	if m.winShape.Kind == WinSevenPairs {
		fan += 2
	}
	if m.winByDraw {
		fan += 1
	}
	if m.winShape.Kind != WinSevenPairs && m.winner >= 0 && m.isConcealed(m.winner) {
		fan += 1
	}
	return fan
}

// pay 单家支付额。
func (m *Match) pay() int {
	if m.winPay > 0 {
		return m.winPay
	}
	return m.rules.BaseScore * (1 << m.fan())
}

// AvailableActions 枚举该座位当前可执行的合法动作代码。
// 动作代码(线缆稳定): draw / discard / chow / pong / kong.exposed /
// kong.concealed / kong.added / hu.self / hu.robbing / pass。
func (m *Match) AvailableActions(seat Seat) []string {
	var acts []string
	if m.phase != PhaseTurn {
		return acts
	}
	// 响应窗口: 非出牌者的可响应动作。
	// 设计: 只有"真正能吃到/碰到/杠到/和牌"的座位才需要决策, 因此也只给它们
	// 返回动作(含"pass")。没有任何真实选择的座位由引擎自动通过(autoPass),
	// 不再要求玩家逐家点"过"。
	if m.turnStage == stageRespond && m.respPending != nil && seat != m.respPending.by {
		if !m.respAnswered[seat] {
			acts = m.respondActions(seat)
			if len(acts) > 0 {
				acts = append(acts, "pass")
			}
		}
		// 未启用响应倒计时时, 下家可用独立的「摸牌」动作强制收敛窗口;
		// 该动作不进入 Actions(避免 AI 误选), 由视图的 ForceDraw 标记驱动。
		return acts
	}
	// 自己回合。
	if m.turnStage == stageDraw && seat == m.turnSeat {
		return []string{"draw"}
	}
	if m.turnStage == stageDiscard && seat == m.turnSeat {
		p := m.players[seat]
		// 自摸。
		if p.selfDraw {
			acts = append(acts, "hu.self")
		}
		// 暗杠: 任意 4 张同种。
		for k, c := range kindCounts(p.hand) {
			if c >= 4 {
				acts = append(acts, "kong.concealed")
				_ = k
				break
			}
		}
		// 补杠: 已碰 + 手牌有第 4 张。
		for _, md := range p.melds {
			if md.Kind == MeldPong && countKind(p.hand, md.Tiles[0]) >= 1 {
				acts = append(acts, "kong.added")
				break
			}
		}
		acts = append(acts, "discard")
	}
	return acts
}

// respondActions 返回该座位在响应窗口中"有实际收益"的动作(不含 pass)。
// 空切片表示该座位对被叫的牌没有任何选择, 调用方应视为自动过。
func (m *Match) respondActions(seat Seat) []string {
	var acts []string
	if m.phase != PhaseTurn || m.turnStage != stageRespond || m.respPending == nil {
		return acts
	}
	if seat == m.respPending.by || m.respAnswered[seat] {
		return acts
	}
	pend := m.respPending
	// 荣和。
	if m.rules.DiscardWin {
		concealed := append(append([]TileKind{}, m.players[seat].hand...), pend.tile)
		if _, ok := CanWinHand(concealed, m.players[seat].melds); ok {
			acts = append(acts, "hu.robbing")
		}
	}
	// 抢杠胡窗口: 只能胡, 不能碰/杠/吃(那张牌本就在补杠者手里)。
	if pend.robKong {
		return acts
	}
	// 碰 / 明杠。
	if countKind(m.players[seat].hand, pend.tile) >= 2 {
		acts = append(acts, "pong")
		if countKind(m.players[seat].hand, pend.tile) >= 3 {
			acts = append(acts, "kong.exposed")
		}
	}
	// 吃(仅上家)。
	if m.rules.Chow && m.isLeftOf(seat, pend.by) {
		if len(chowCandidates(m.players[seat].hand, pend.tile)) > 0 {
			acts = append(acts, "chow")
		}
	}
	return acts
}

// chowCandidates 列出用手牌与被打牌组成顺子的搭配(去重)。
func chowCandidates(hand []TileKind, tile TileKind) [][2]TileKind {
	var out [][2]TileKind
	seen := map[[2]TileKind]bool{}
	for _, a := range hand {
		for _, b := range hand {
			if a >= b {
				continue
			}
			if _, ok := chowRun(tile, a, b); ok {
				pair := [2]TileKind{a, b}
				if !seen[pair] {
					seen[pair] = true
					out = append(out, pair)
				}
			}
		}
	}
	return out
}

// kindCounts 手牌种类计数。
func kindCounts(hand []TileKind) *[KindCount]int {
	var c [KindCount]int
	for _, k := range hand {
		c[k]++
	}
	return &c
}

// PendingSeat 返回响应窗口中出牌的座位; 无响应窗口时返回 -1。
// 供本地驱动按座次距离决定多家可胡时的响应顺序。
func (m *Match) PendingSeat() Seat {
	if m.respPending == nil {
		return -1
	}
	return m.respPending.by
}
