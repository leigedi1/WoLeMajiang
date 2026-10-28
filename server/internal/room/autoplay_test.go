package room

import (
	"context"
	"testing"
	"time"

	"majiang-server/internal/game"
)

// fullRoom 创建"1 真人 + 3 机器人"的满座房间并开局。
// 出牌倒计时打开(3 秒), 这样人类座位在时间推进后会被引擎自动托管。
func fullRoom(t *testing.T, seed int64) (*Registry, *Room) {
	t.Helper()
	r, rm := newTestRoom(t, "", true)
	rm.RuleSet.DiscardSec = 3
	for seat := 1; seat <= 3; seat++ {
		if err := rm.SetBot("host", seat, true); err != nil {
			t.Fatalf("补入机器人(座位 %d): %v", seat, err)
		}
	}
	readyAll(t, rm)
	if err := rm.StartMatch(game.NewSeededSource(seed)); err != nil {
		t.Fatalf("开局: %v", err)
	}
	return r, rm
}

// 机器人应能独自把一局推进到终局(人类玩家由超时托管代打),
// 且终局分数守恒为 0、不出现非法动作或死循环。
func TestAutoStepDrivesMatchToEnd(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		_, rm := fullRoom(t, seed)
		// 时间推进到"人类必然超时", 让 AutoStep 接管真人座位。
		deadline := time.Now().Add(2 * time.Minute)

		steps := 0
		for steps < 4000 {
			if rm.MatchFinished() {
				break
			}
			if !rm.AutoStep(deadline) {
				t.Fatalf("seed %d: AutoStep 无法推进(第 %d 步)", seed, steps)
			}
			steps++
		}
		if !rm.MatchFinished() {
			t.Fatalf("seed %d: %d 步后仍未终局", seed, steps)
		}
		// 终局后不应再有动作。
		if rm.AutoStep(deadline.Add(time.Minute)) {
			t.Fatalf("seed %d: 终局后 AutoStep 仍在动作", seed)
		}
		view, ok := rm.GameViewFor("host")
		if !ok || view.Result == nil {
			t.Fatalf("seed %d: 终局缺少结果投影", seed)
		}
		sum := 0
		for _, s := range view.Result.Scores {
			sum += s
		}
		if sum != 0 {
			t.Fatalf("seed %d: 分数不守恒, 合计 %d (%v)", seed, sum, view.Result.Scores)
		}
	}
}

// 2 人 / 3 人房间同样能被托管推进到终局, 且分数在参与座位间零和。
func TestAutoStepSupportsTwoAndThreePlayers(t *testing.T) {
	for _, players := range []int{2, 3} {
		players := players
		t.Run(seatLabel(players), func(t *testing.T) {
			r := NewRegistry(3, time.Hour, nil)
			rm, err := r.Create(context.Background(), CreateOptions{
				GuestID: "host", Nickname: "房主",
				RuleSet: game.DefaultRuleSet(),
				Theme:   game.ThemeClassic,
			})
			if err != nil {
				t.Fatal(err)
			}
			rm.RuleSet.Players = players
			rm.RuleSet.DiscardSec = 3
			for seat := 1; seat < players; seat++ {
				if err := rm.SetBot("host", seat, true); err != nil {
					t.Fatalf("补入机器人(座位 %d): %v", seat, err)
				}
			}
			if err := rm.StartMatch(game.NewSeededSource(3)); err != nil {
				t.Fatalf("%d 人开局: %v", players, err)
			}
			deadline := time.Now().Add(time.Hour)
			steps := 0
			for steps < 4000 && !rm.MatchFinished() {
				if !rm.AutoStep(deadline) {
					t.Fatalf("%d 人: AutoStep 无法推进(第 %d 步)", players, steps)
				}
				steps++
			}
			if !rm.MatchFinished() {
				t.Fatalf("%d 人: %d 步后仍未终局", players, steps)
			}
			view, ok := rm.GameViewFor("host")
			if !ok || view.Result == nil {
				t.Fatalf("%d 人: 终局缺少结果投影", players)
			}
			sum := 0
			for _, s := range view.Result.Scores {
				sum += s
			}
			if sum != 0 {
				t.Fatalf("%d 人: 分数不守恒, 合计 %d (%v)", players, sum, view.Result.Scores)
			}
			if got := len(view.Result.Scores); got != players {
				t.Fatalf("%d 人: 结算分数条目应为 %d, 实际 %d", players, players, got)
			}
		})
	}
}

func seatLabel(players int) string {
	switch players {
	case 2:
		return "2人"
	case 3:
		return "3人"
	default:
		return "4人"
	}
}

// 未超时的人类座位必须等待, 不得被 AI 代打。
func TestAutoStepWaitsForHuman(t *testing.T) {	_, rm := fullRoom(t, 11)
	// 仅机器人座位会被推进; 直到轮到人类且未超时, AutoStep 必须返回 false。
	now := time.Now()
	for i := 0; i < 200; i++ {
		seat, _, ok := rm.CurrentTurn()
		if ok && seat == 0 {
			break
		}
		if !rm.AutoStep(now) {
			break
		}
	}
	seat, _, ok := rm.CurrentTurn()
	if !ok {
		t.Skip("对局已结束(种子导致), 跳过")
	}
	if seat != 0 {
		t.Skipf("机器人尚未轮到人类座位(当前 %d), 跳过", seat)
	}
	before := rm.CurrentSeq()
	if rm.AutoStep(now) {
		t.Fatal("人类未超时时不应被 AI 代打")
	}
	if rm.CurrentSeq() != before {
		t.Fatal("未动作不应产生事件")
	}
	// 超时后必须接管。
	if !rm.AutoStep(now.Add(5 * time.Minute)) {
		t.Fatal("超时后应自动托管")
	}
}

// 超时托管必须对真人"响应窗口"同样生效(否则会卡死在等牌阶段)。
func TestAutoStepHandlesResponseWindow(t *testing.T) {
	_, rm := fullRoom(t, 3)
	now := time.Now()
	// 先让机器人推进到出现响应窗口(或终局)。
	for i := 0; i < 400; i++ {
		if rm.MatchFinished() {
			t.Skip("对局已结束, 跳过")
		}
		if rm.AutoStep(now) == false {
			break
		}
	}
	// 此时若停在人类决策处, 超时应能推进。
	before := rm.CurrentSeq()
	if !rm.AutoStep(now.Add(5*time.Minute)) {
		t.Fatal("超时后应推进对局")
	}
	if rm.CurrentSeq() == before {
		t.Fatal("推进应产生事件")
	}
}
