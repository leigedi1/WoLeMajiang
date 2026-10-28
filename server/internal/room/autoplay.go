// 阶段5: 在线房间的自动推进 —— 机器人座位与人类超时托管。
//
// 设计约束:
//   - 机器人与人类走完全相同的权威动作入口(applyLocked), 不使用任何捷径,
//     因此"AI 能看到的信息"受 ViewFor(seat) 投影约束, 结构上无法看到他人手牌。
//   - 推进由服务器定时器驱动(见 httpapi 的 ticker), 每个房间单步串行, 无并发写。
//   - 阶段10 起, 计时规则(响应倒计时/出牌倒计时)由 game.Match 自己持有并可在
//     房间设置里开关; 房间层只负责按 tick 调用 Match.Tick, 避免两套超时逻辑
//     互相打架。房间层额外保留一条"掉线兜底": 若该座位玩家已断开且引擎没有为
//     它设定任何倒计时(即房主把计时全部关掉), 则立即托管一步, 防止对局永久卡死。
package room

import (
	"encoding/json"
	"time"

	"majiang-server/internal/ai"
	"majiang-server/internal/game"
)

// 机器人难度(固定 normal: 兼顾速度与牌效; 难度选择属于单机模式)。
const botDifficulty = ai.Normal

// AutoStep 推进一次自动行动: 先处理计时超时, 再让机器人连续行动,
// 最后为"已掉线且没有倒计时"的座位做兜底托管。
// 返回是否发生了动作(调用方据此广播)。
func (r *Room) AutoStep(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.match == nil || r.match.Phase() == game.PhaseFinished {
		return false
	}
	changed := false

	// 1) 计时器: 响应倒计时到点自动"过", 出牌倒计时到点自动打牌。
	//    每次 Tick 收敛一批到期项, 循环到没有更多到期项为止, 这样连续的
	//    短倒计时也能被同一次 tick 推进完。
	for i := 0; i < 16; i++ {
		res, ok := r.match.Tick(now)
		if !ok {
			break
		}
		r.commitLocked(res)
		changed = true
		if r.match.Phase() == game.PhaseFinished {
			r.lastActivity = now
			return true
		}
	}

	// 2) 机器人: 轮到就立即行动(不等倒计时)。
	for i := 0; i < 200; i++ {
		seat, ok := r.nextBotLocked()
		if !ok {
			break
		}
		if err := r.autoActLocked(seat, now); err != nil {
			// 决策被引擎拒绝: 退化为最保守的合法动作, 绝不让对局卡死。
			if fallbackErr := r.fallbackActLocked(seat, now); fallbackErr != nil {
				r.lastActivity = now
				return changed
			}
		}
		changed = true
		if r.match.Phase() == game.PhaseFinished {
			break
		}
	}

	// 3) 掉线/停滞兜底: 引擎没有为该座位设倒计时(计时规则被关闭), 但轮到它
	//    已经超过 DefaultTurnTimeout, 或它已经掉线, 则替它走一步 ——
	//    在线房间必须永远能向前推进, 否则一个人锁屏就卡死整桌。
	if r.match.Phase() != game.PhaseFinished {
		if seat, ok := r.nextStalledLocked(now); ok {
			if err := r.fallbackActLocked(seat, now); err == nil {
				changed = true
			}
		}
	}

	if changed {
		r.lastActivity = now
	}
	return changed
}

// nextBotLocked 找出下一个需要行动的机器人座位(调用方持锁)。
// 响应窗口内所有有可选动作的机器人按优先级/座次顺序排队; 自家回合只有当前座位。
func (r *Room) nextBotLocked() (game.Seat, bool) {
	best := game.Seat(-1)
	bestPri := 99
	bestDist := 99
	pendBy := r.match.PendingSeat()
	for _, p := range r.players {
		if !p.IsBot {
			continue
		}
		seat := game.Seat(p.Seat)
		acts := r.match.AvailableActions(seat)
		if len(acts) == 0 {
			continue
		}
		view := r.match.ViewFor(seat)
		if view.TurnStage != "respond" && r.match.TurnSeat() != seat {
			continue
		}
		pri := 2
		if hasAction(acts, "hu.robbing") || hasAction(acts, "hu.self") {
			pri = 0
		} else if hasAction(acts, "pong") || hasAction(acts, "kong.exposed") || hasAction(acts, "chow") {
			pri = 1
		}
		dist := 0
		if pendBy >= 0 {
			dist = (int(seat) - int(pendBy) + r.RuleSet.Players) % r.RuleSet.Players
		}
		if pri < bestPri || (pri == bestPri && dist < bestDist) {
			best, bestPri, bestDist = seat, pri, dist
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// nextStalledLocked 找出"该行动但引擎没有计时"的座位(调用方持锁):
// 已经掉线, 或轮到它已超过 DefaultTurnTimeout。仅在没有可用倒计时时兜底,
// 正常开启倒计时的房间不会走到这里。
func (r *Room) nextStalledLocked(now time.Time) (game.Seat, bool) {
	deadlines := r.match.ResponseDeadlines()
	discardDeadline := r.match.DiscardDeadline()
	stalled := now.Sub(r.lastActivity) >= DefaultTurnTimeout
	for _, p := range r.players {
		if p.IsBot {
			continue
		}
		seat := game.Seat(p.Seat)
		if len(r.match.AvailableActions(seat)) == 0 {
			continue
		}
		view := r.match.ViewFor(seat)
		if view.TurnStage != "respond" && r.match.TurnSeat() != seat {
			continue
		}
		// 引擎已经在为它计时 → 交给 Tick, 这里不重复托管。
		if int(seat) < len(deadlines) && !deadlines[seat].IsZero() {
			continue
		}
		if !discardDeadline.IsZero() && r.match.TurnSeat() == seat {
			continue
		}
		if p.Connected && !stalled {
			continue
		}
		return seat, true
	}
	return 0, false
}

// autoActLocked 让 AI 为该座位做一次决策并提交(调用方持锁)。
func (r *Room) autoActLocked(seat game.Seat, now time.Time) error {
	view := r.match.ViewFor(seat)
	req := ai.Request{View: view, Rules: r.RuleSet, Difficulty: botDifficulty}
	dec := ai.Decide(req, game.NewSeededSource(now.UnixNano()))
	payload, err := decisionPayload(dec)
	if err != nil {
		return err
	}
	res, err := r.applyLocked(seat, dec.Action, payload)
	if err != nil {
		return err
	}
	r.commitLocked(res)
	return nil
}

// fallbackActLocked 保底动作: 响应阶段 pass, 摸牌阶段 draw, 打牌阶段打最后一张。
func (r *Room) fallbackActLocked(seat game.Seat, now time.Time) error {
	acts := r.match.AvailableActions(seat)
	pick := ""
	for _, a := range acts {
		switch a {
		case "pass":
			pick = "pass"
		case "draw":
			if pick == "" {
				pick = "draw"
			}
		}
	}
	var payload []byte
	if pick == "" {
		view := r.match.ViewFor(seat)
		hand := append([]string{}, view.Hand...)
		if view.Drawn != "" {
			hand = append(hand, view.Drawn)
		}
		if len(hand) == 0 {
			return ErrNoMatch
		}
		pick = "discard"
		payload, _ = json.Marshal(map[string]string{"tile": hand[len(hand)-1]})
	}
	res, err := r.applyLocked(seat, pick, payload)
	if err != nil {
		return err
	}
	r.commitLocked(res)
	return nil
}

// decisionPayload 把 AI 决策转换为引擎负载。
func decisionPayload(d ai.Decision) (json.RawMessage, error) {
	switch d.Action {
	case "discard", "kong.concealed", "kong.added":
		if d.Tile == "" {
			return nil, ErrNoMatch
		}
		return json.Marshal(map[string]string{"tile": d.Tile})
	case "chow":
		if len(d.With) != 2 {
			return nil, ErrNoMatch
		}
		return json.Marshal(map[string][]string{"with": d.With})
	default:
		return nil, nil
	}
}

// CurrentTurn 当前行动座位与截止时间(测试与 UI 倒计时用)。
// 响应窗口中返回最早到期的响应座位。
func (r *Room) CurrentTurn() (game.Seat, time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.match == nil || r.match.Phase() == game.PhaseFinished {
		return 0, time.Time{}, false
	}
	deadlines := r.match.ResponseDeadlines()
	best := game.Seat(-1)
	var bestAt time.Time
	for i, dl := range deadlines {
		if dl.IsZero() {
			continue
		}
		if best < 0 || dl.Before(bestAt) {
			best, bestAt = game.Seat(i), dl
		}
	}
	if best >= 0 {
		return best, bestAt, true
	}
	return r.match.TurnSeat(), r.match.DiscardDeadline(), true
}

// hasAction 动作列表里是否含某动作。
func hasAction(acts []string, want string) bool {
	for _, a := range acts {
		if a == want {
			return true
		}
	}
	return false
}
