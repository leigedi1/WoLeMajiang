// Package local 在单进程内驱动一局对局。
//
// 设计目标:
//   - 复用 game 包的权威规则引擎与 ai 包的决策器, 不重复实现任何规则;
//   - 与传输层解耦: 浏览器侧(WASM 单机)、服务器侧(AI 补位)、测试侧共用同一驱动;
//   - 事件流只含公共信息(与 WS 协议一致), 私有状态一律通过 ViewFor 按座位投影;
//   - 只做"谁来行动 / 合法动作有哪些"的编排, 不做任何牌型判定。
//
// 安全边界: AI 决策只读取 ai.Request.View, 即权威端裁剪后的单人投影,
// 结构上不含牌墙内容与他人暗牌。
package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"majiang-server/internal/ai"
	"majiang-server/internal/game"
)

// 驱动层错误。
var (
	// ErrNoMatch 对局未开始或已关闭。
	ErrNoMatch = errors.New("对局未开始")
	// ErrBadSeat 座位号越界。
	ErrBadSeat = errors.New("座位号越界")
	// ErrBadConfig 配置不合法。
	ErrBadConfig = errors.New("配置不合法")
)

// SeatSpec 一个座位的参与者描述。
type SeatSpec struct {
	Name       string `json:"name"`
	AI         bool   `json:"ai"`
	Difficulty string `json:"difficulty"`
}

// Config 单机对局配置。
type Config struct {
	Rules  game.RuleSet `json:"rules"`
	Dealer int          `json:"dealer"`
	Seed   int64        `json:"seed"`
	Seats  []SeatSpec   `json:"seats"`
}

// Event 一条公共对局事件(与 WS 协议的 game.event 负载同构)。
type Event struct {
	Seq   int      `json:"seq"`
	Kind  string   `json:"kind"`
	Seat  int      `json:"seat"`
	Tiles []string `json:"tiles,omitempty"`
}

// Table 单局对局的本地驱动。
type Table struct {
	mu    sync.Mutex
	rules game.RuleSet
	match *game.Match
	seats []SeatSpec
	diff  []ai.Difficulty
	rng   game.RandomSource
	seq   int
	evs   []Event
}

// New 创建并开局一局对局。随机源注入: Seed 为 0 时使用加密随机源。
func New(cfg Config) (*Table, error) {
	rules := cfg.Rules
	if rules.Players == 0 {
		rules = game.DefaultRuleSet()
	}
	if err := rules.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadConfig, err)
	}
	if len(cfg.Seats) != rules.Players {
		return nil, fmt.Errorf("%w: 座位数 %d 与规则人数 %d 不一致", ErrBadConfig, len(cfg.Seats), rules.Players)
	}
	if cfg.Dealer < 0 || cfg.Dealer >= rules.Players {
		return nil, fmt.Errorf("%w: 庄家座位 %d 越界", ErrBadConfig, cfg.Dealer)
	}
	diff := make([]ai.Difficulty, len(cfg.Seats))
	for i, s := range cfg.Seats {
		if !s.AI {
			continue
		}
		d, err := ai.ParseDifficulty(s.Difficulty)
		if err != nil {
			return nil, fmt.Errorf("%w: 座位 %d %v", ErrBadConfig, i, err)
		}
		diff[i] = d
	}
	var src game.RandomSource
	if cfg.Seed != 0 {
		src = game.NewSeededSource(cfg.Seed)
	} else {
		src = game.NewCryptoSource()
	}
	m, err := game.NewMatch(rules, game.Seat(cfg.Dealer), src)
	if err != nil {
		return nil, err
	}
	seats := make([]SeatSpec, len(cfg.Seats))
	copy(seats, cfg.Seats)
	return &Table{rules: rules, match: m, seats: seats, diff: diff, rng: src}, nil
}

// Rules 返回本局规则副本。
func (t *Table) Rules() game.RuleSet {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rules
}

// Seats 返回座位描述副本。
func (t *Table) Seats() []SeatSpec {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SeatSpec, len(t.seats))
	copy(out, t.seats)
	return out
}

// SeatCount 座位数。
func (t *Table) SeatCount() int { return t.rules.Players }

// Finished 对局是否结束。
func (t *Table) Finished() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.match == nil || t.match.Phase() == game.PhaseFinished
}

// Seq 当前事件序号水位线。
func (t *Table) Seq() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seq
}

// EventsSince 返回序号大于 after 的事件副本。
func (t *Table) EventsSince(after int) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	if after < 0 {
		after = 0
	}
	out := make([]Event, 0, len(t.evs))
	for _, ev := range t.evs {
		if ev.Seq > after {
			out = append(out, ev)
		}
	}
	return out
}

// ViewFor 返回指定座位的可见状态(裁剪后投影)。
func (t *Table) ViewFor(seat int) (game.SeatView, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.match == nil {
		return game.SeatView{}, ErrNoMatch
	}
	if seat < 0 || seat >= t.rules.Players {
		return game.SeatView{}, ErrBadSeat
	}
	return t.match.ViewFor(game.Seat(seat)), nil
}

// Apply 以指定座位身份执行一个动作(动作代码与线缆一致)。
// payload 为该动作的 JSON 负载(discard/kong.concealed/kong.added 用 {"tile":...},
// chow 用 {"with":[a,b]})。
func (t *Table) Apply(seat int, action string, payload json.RawMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.applyLocked(seat, action, payload)
}

func (t *Table) applyLocked(seat int, action string, payload json.RawMessage) error {
	if t.match == nil {
		return ErrNoMatch
	}
	if seat < 0 || seat >= t.rules.Players {
		return ErrBadSeat
	}
	s := game.Seat(seat)
	var (
		res game.ActionResult
		err error
	)
	switch action {
	case "draw":
		res, err = t.match.Draw()
	case "forcedraw":
		// 未启用响应倒计时时, 下家主动摸牌强制收敛响应窗口。
		res, err = t.match.ForceDraw(s)
	case "discard":
		var in struct {
			Tile string `json:"tile"`
		}
		if err = json.Unmarshal(payload, &in); err == nil {
			var k game.TileKind
			if k, err = parseTile(in.Tile); err == nil {
				res, err = t.match.Discard(s, k)
			}
		}
	case "pong":
		res, err = t.match.Respond(s, game.RespondAction{Pong: true})
	case "kong.exposed":
		res, err = t.match.Respond(s, game.RespondAction{Kong: true})
	case "hu.robbing":
		res, err = t.match.Respond(s, game.RespondAction{Win: true})
	case "pass":
		res, err = t.match.Respond(s, game.RespondAction{Pass: true})
	case "chow":
		var in struct {
			With []string `json:"with"`
		}
		if err = json.Unmarshal(payload, &in); err == nil && len(in.With) == 2 {
			var a, b game.TileKind
			if a, err = parseTile(in.With[0]); err == nil {
				if b, err = parseTile(in.With[1]); err == nil {
					res, err = t.match.Respond(s, game.RespondAction{
						Chow: &game.ChowCombo{With: [2]game.TileKind{a, b}},
					})
				}
			}
		} else if err == nil {
			err = fmt.Errorf("chow 需要 with 两张牌")
		}
	case "kong.concealed", "kong.added":
		var in struct {
			Tile string `json:"tile"`
		}
		if err = json.Unmarshal(payload, &in); err == nil {
			var k game.TileKind
			if k, err = parseTile(in.Tile); err == nil {
				res, err = t.match.KongSelf(s, k, action == "kong.added")
			}
		}
	case "hu.self":
		res, err = t.match.WinSelf(s)
	default:
		err = fmt.Errorf("未知动作: %s", action)
	}
	if err != nil {
		return err
	}
	t.recordLocked(seat, res)
	return nil
}

// recordLocked 把动作结果写入公共事件日志。
// 引擎在响应窗口的中间 pass 不产生事件, 此时补一条 state 事件保证
// 客户端事件序号前进(否则响应窗口会因无反馈而卡住)。
func (t *Table) recordLocked(seat int, res game.ActionResult) {
	if len(res.Events) == 0 {
		t.appendLocked("state", seat, nil)
		return
	}
	for _, ev := range res.Events {
		t.appendLocked(ev.Kind, int(ev.Seat), tilesToCodes(ev.Tiles))
	}
}

func (t *Table) appendLocked(kind string, seat int, tiles []string) {
	t.seq++
	t.evs = append(t.evs, Event{Seq: t.seq, Kind: kind, Seat: seat, Tiles: tiles})
}

// PendingAI 返回下一个应当行动的 AI 座位, 无人需要行动时 ok=false。
//
// 优先级: 和牌 > 响应窗口(按出牌者下家起的座次距离) > 自家回合。
func (t *Table) PendingAI() (seat int, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pendingAILocked()
}

func (t *Table) pendingAILocked() (int, bool) {
	if t.match == nil || t.match.Phase() == game.PhaseFinished {
		return 0, false
	}
	var pendBy = -1
	type cand struct {
		seat int
		pri  int
		dist int
	}
	var cands []cand
	for i := 0; i < t.rules.Players; i++ {
		if !t.seats[i].AI {
			continue
		}
		acts := t.match.AvailableActions(game.Seat(i))
		if len(acts) == 0 {
			continue
		}
		pri := 2
		if hasAction(acts, "hu.self") || hasAction(acts, "hu.robbing") {
			pri = 0
		} else if hasAction(acts, "pass") {
			pri = 1
		}
		if pri == 1 && pendBy < 0 {
			pendBy = int(t.match.PendingSeat())
		}
		dist := 0
		if pendBy >= 0 {
			dist = (i - pendBy + t.rules.Players) % t.rules.Players
		}
		cands = append(cands, cand{seat: i, pri: pri, dist: dist})
	}
	if len(cands) == 0 {
		return 0, false
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.pri < best.pri || (c.pri == best.pri && c.dist < best.dist) {
			best = c
		}
	}
	return best.seat, true
}

// StepAI 让下一个待行动的 AI 走一步; acted=false 表示当前轮到人类或已结束。
func (t *Table) StepAI() (seat int, acted bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.pendingAILocked()
	if !ok {
		return 0, false, nil
	}
	view := t.match.ViewFor(game.Seat(s))
	d := ai.Decide(ai.Request{View: view, Rules: t.rules, Difficulty: t.diff[s]}, t.rng)
	if !d.Legal(view.Actions) {
		// 决策必须落在合法动作集合内; 兜底为最保守的合法动作。
		d = fallbackDecision(view.Actions)
	}
	if d.Action == "" {
		return s, false, nil
	}
	payload := []byte("{}")
	if d.Tile != "" {
		b, _ := json.Marshal(map[string]any{"tile": d.Tile})
		payload = b
	}
	if len(d.With) == 2 {
		b, _ := json.Marshal(map[string]any{"with": d.With})
		payload = b
	}
	if err := t.applyLocked(s, d.Action, payload); err != nil {
		return s, false, err
	}
	return s, true, nil
}

// fallbackDecision 兜底决策: 有 pass 则过, 否则摸牌, 否则打第一张。
func fallbackDecision(acts []string) ai.Decision {
	if hasAction(acts, "pass") {
		return ai.Decision{Action: "pass"}
	}
	if hasAction(acts, "draw") {
		return ai.Decision{Action: "draw"}
	}
	return ai.Decision{}
}

func hasAction(acts []string, want string) bool {
	for _, a := range acts {
		if a == want {
			return true
		}
	}
	return false
}

func tilesToCodes(tiles []game.TileKind) []string {
	if len(tiles) == 0 {
		return nil
	}
	out := make([]string, len(tiles))
	for i, t := range tiles {
		out[i] = t.Code()
	}
	return out
}

// parseTile 解析线缆牌代码, 例如 "wan1"、"dong"。
func parseTile(code string) (game.TileKind, error) {
	k, ok := game.KindFromCode(code)
	if !ok {
		return 0, fmt.Errorf("未知牌代码: %q", code)
	}
	return k, nil
}

// WallLeft 牌墙剩余张数。
func (t *Table) WallLeft() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.match == nil {
		return 0
	}
	return t.match.RemainingWall()
}

// Tick 驱动倒计时: 响应超时自动过、出牌超时自动打牌。
// 返回是否发生变化(调用方据此广播事件)。
func (t *Table) Tick(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.match == nil {
		return false
	}
	res, changed := t.match.Tick(now)
	if !changed {
		return false
	}
	t.recordLocked(-1, res)
	return true
}
