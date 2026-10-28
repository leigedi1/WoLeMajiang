package game

import (
	"fmt"
	"sort"
	"time"
)

// Match 一局权威对局(四人基础规则)。所有方法非并发安全——由房间层持锁调用。
type Match struct {
	rules   RuleSet
	seed    int64 // 洗牌种子(重放用)
	wall    *Wall
	players [4]*matchPlayer
	phase   Phase

	// 回合状态。
	turnSeat   Seat  // 当前轮到行动的座位(摸牌后待打, 或响应窗口等待中)
	turnDrawn  TileKind // 本回合刚摸的牌(若来自摸牌); 打出后清零
	turnStage  turnStage

	// 响应窗口: 出牌/暗杠/补杠后开放, 全员 pass 或最高优先级响应即关闭。
	respPending   *pendingDiscard // 非空表示等待他人响应
	respAnswered  [4]bool         // 各座位是否已提交响应(含放弃)
	respClaims    [4]*respondClaim // 已提交但尚未仲裁的抢牌请求
	respDeadlines [4]time.Time     // 各座位响应倒计时截止时刻(零值=无倒计时)
	claimSeq      int64            // 抢牌提交顺序(截胡/截碰关闭时先到先得)
	claimState    [4]string        // 本座位抢牌判定状态: ""|pending|success|failed
	claimStateID  [4]int64         // 判定状态版本号(前端一次性提示用)
	// 抢杠胡: 补杠动作先挂起, 等响应窗口无人抢再真正执行。
	pendingKong *pendingKong

	// 出牌阶段倒计时(0 秒=不限时)。
	discardDeadline time.Time
	// clock 可注入时钟(测试用; nil=系统时钟)。
	clock func() time.Time

	// 时序型番种标记(阶段11)。
	kongFlower   bool // 本次摸牌来自杠后补牌(杠上开花判定)
	lastDrawTile bool // 本次摸牌摸走了牌墙最后一张(海底捞月判定)
	riverEnd     bool // 本次出牌时牌墙已空(河底捞鱼判定)
	winRobKong   bool // 本局由抢杠胡结束(抢杠胡番种)

	// 结算。
	winner      Seat   // 胡牌者; 流局时为 -1
	winShape    WinShape
	winByDraw   bool   // true=自摸, false=点炮(含抢杠胡)
	loser       Seat   // 点炮者(自摸时无效)
	winFan      int    // 结算番数(结算时一次性算定, 投影直接读取)
	winPay      int    // 单家支付额
	winFlags    []string // 命中的番种名称(结算展示)
	scores      [4]int // 各座位本局得分(正=赢, 负=输)
	discards    [4][]TileKind // 各家打出的牌(河)
	discardLog  []DiscardEntry // 全局出牌流水(有序), 前端「最近出牌」用
	lastDiscardSeat Seat  // 最近一次打牌者座位; -1 表示无
}

// 抢牌优先级: 胡 > 碰/杠 > 吃。
const (
	priChow = 1
	priMeld = 2
	priWin  = 3
)

// respondClaim 一个已提交、等待仲裁的抢牌请求。
type respondClaim struct {
	pri   int        // 优先级(priChow/priMeld/priWin)
	sub   string     // pong / kong.exposed / chow / hu.robbing
	seq   int64      // 提交顺序
	run   [3]TileKind // 吃: 三张顺子
	with  [2]TileKind // 吃: 手里的两张
	shape WinShape   // 胡: 牌型
}

type turnStage string

const (
	stageDraw   turnStage = "draw"   // 待当前座位摸牌(或开局摸牌)
	stageDiscard turnStage = "discard" // 已摸/已副露, 待打牌
	stageRespond turnStage = "respond" // 等待他人响应(碰/吃/杠/胡)
)

// matchPlayer 对局内玩家私有状态(仅权威端持有)。
type matchPlayer struct {
	hand     []TileKind // 暗牌(有序)
	melds    []Meld     // 亮出的副露
	handIDs  []TileID   // 暗牌对应的物理 ID(与 hand 同步; 阶段7重连快照用)
	selfDraw bool       // 本回合是否自摸待和
}

// pendingDiscard 等待响应的出牌。
// robKong=true 表示这不是普通打出的牌, 而是补杠的第 4 张(抢杠胡窗口):
// 此时其他家只能"胡", 不能碰/杠/吃; 无人抢则补杠照常完成。
type pendingDiscard struct {
	by      Seat
	tile    TileKind
	robKong bool
}

// pendingKong 等待抢杠判定结束后才真正执行的补杠。
type pendingKong struct {
	seat  Seat
	tile  TileKind
	added bool
}

// DiscardResult 打牌后的即时状态: 是否开放响应窗口与可响应动作。
type DiscardResult struct {
	OpenWindow bool
}

// NewMatch 创建一局并立即发牌。dealer 为庄家座位。
// 随机源由调用方注入(生产用 crypto 种子, 测试用固定种子重放)。
func NewMatch(rules RuleSet, dealer Seat, src RandomSource) (*Match, error) {
	if err := rules.Validate(); err != nil {
		return nil, err
	}
	if dealer < 0 || int(dealer) >= rules.Players {
		return nil, fmt.Errorf("庄家座位越界: %d", dealer)
	}
	m := &Match{
		rules: rules,
		wall:  NewShuffledWallOf(rules.PoolKinds(), src),
		phase: PhaseDealing,
		lastDiscardSeat: -1,
	}
	for i := 0; i < rules.Players; i++ {
		m.players[i] = &matchPlayer{}
	}
	m.deal(dealer)
	return m, nil
}

// deal 发牌: 每人 13 张, 庄家再摸 1 张进入打牌阶段。
func (m *Match) deal(dealer Seat) {
	n := m.rules.Players
	// 发 13 张 × n 家。
	for round := 0; round < 13; round++ {
		for i := 0; i < n; i++ {
			seat := Seat((int(dealer) + i) % n)
			if t, ok := m.wall.Draw(); ok {
				m.players[seat].hand = append(m.players[seat].hand, t.Kind())
				m.players[seat].handIDs = append(m.players[seat].handIDs, t)
			}
		}
	}
	// 庄家摸第 14 张。
	if t, ok := m.wall.Draw(); ok {
		d := int(dealer)
		p := m.players[d]
		p.hand = append(p.hand, t.Kind())
		p.handIDs = append(p.handIDs, t)
		m.turnDrawn = t.Kind()
		sort.Slice(p.hand, func(i, j int) bool { return p.hand[i] < p.hand[j] })
		// 天和判定(庄家起手即和)。
		_, canWin := CanWinHand(p.hand, p.melds)
		p.selfDraw = canWin && m.rules.SelfDraw
	}
	// 只整理实际参与的座位: 2/3 人规则下其余槽位为 nil。
	for i := 0; i < n; i++ {
		p := m.players[i]
		sort.Slice(p.hand, func(a, b int) bool { return p.hand[a] < p.hand[b] })
	}
	m.turnSeat = dealer
	m.turnStage = stageDiscard
	m.armDiscardDeadlineLocked()
	m.phase = PhaseTurn
}

// armDiscardDeadlineLocked 进入出牌阶段时按规则设置出牌倒计时。
// DiscardSec 为 0 表示不限时(一直等待该玩家出牌)。
func (m *Match) armDiscardDeadlineLocked() {
	if m.rules.DiscardSec > 0 {
		m.discardDeadline = m.now().Add(time.Duration(m.rules.DiscardSec) * time.Second)
	} else {
		m.discardDeadline = time.Time{}
	}
}

// Phase 当前阶段。
func (m *Match) Phase() Phase { return m.phase }

// TurnStage 返回当前回合阶段(draw/discard/respond), 供房间层区分
// “自己回合”与“响应窗口”两种超时策略。
func (m *Match) TurnStage() string { return string(m.turnStage) }

// TurnSeat 当前行动座位(响应窗口期指出牌者)。
func (m *Match) TurnSeat() Seat { return m.turnSeat }

// RemainingWall 牌墙剩余。
func (m *Match) RemainingWall() int { return m.wall.Remaining() }

// Winner 胜者座位; 流局为 -1。
func (m *Match) Winner() Seat { return m.winner }

// Scores 结算分数快照。
func (m *Match) Scores() [4]int { return m.scores }

// SeatCount 本局人数。
func (m *Match) SeatCount() int { return m.rules.Players }

// handCounts 手牌数量快照(投影用)。
func (m *Match) handCounts() [4]int {
	var out [4]int
	for i := 0; i < m.rules.Players; i++ {
		if p := m.players[i]; p != nil {
			out[i] = len(p.hand)
		}
	}
	return out
}

// drawCurrent 当前摸牌(该玩家的第 14 张)。
func (m *Match) drawCurrent() Seat {
	// 轮到谁: turnSeat 在 draw 阶段即待摸者。
	return m.turnSeat
}
