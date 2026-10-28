package game

import (
	"math/rand"
	"testing"
)

// TestMatchSupportsCustomPool 自定义牌集: 只发池内的牌, 牌数守恒等于池总张数。
func TestMatchSupportsCustomPool(t *testing.T) {
	pool := map[string]int{}
	for _, c := range []string{"wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
		"tiao1", "tiao2", "tiao3", "tiao4", "tiao5", "tiao6", "tiao7", "tiao8", "tiao9"} {
		pool[c] = 3
	}
	rules := DefaultRuleSet()
	rules.Players = 2
	rules.Pool = pool
	total := rules.PoolTotal()
	if total != 54 {
		t.Fatalf("牌池应为 54 张, 实际 %d", total)
	}
	for seed := int64(1); seed <= 5; seed++ {
		m, err := NewMatch(rules, 0, NewSeededSource(seed))
		if err != nil {
			t.Fatalf("seed %d: 创建对局失败: %v", seed, err)
		}
		// 发牌后不得出现池外种类(字牌/筒子)。
		for seat := 0; seat < rules.Players; seat++ {
			for _, k := range m.players[seat].hand {
				if k >= KindEast {
					t.Fatalf("seed %d: 出现池外字牌 %s", seed, k.Code())
				}
			}
		}
		rng := rand.New(rand.NewSource(seed))
		for step := 0; step < 4000 && m.Phase() != PhaseFinished; step++ {
			if err := randomStep(m, rng); err != nil {
				t.Fatalf("seed %d step %d: 动作被拒: %v", seed, step, err)
			}
		}
		if got := countTiles(m); got != total {
			t.Fatalf("seed %d: 牌数不守恒, 场上 %d 张(应为 %d)", seed, got, total)
		}
	}
}

// TestRuleSetRejectsTooSmallPool 牌太少必须显式拒绝, 而不是开局后才流局。
func TestRuleSetRejectsTooSmallPool(t *testing.T) {
	rules := DefaultRuleSet()
	rules.Players = 4
	rules.Pool = map[string]int{"wan1": 4, "wan2": 4}
	if err := rules.Validate(); err == nil {
		t.Fatal("牌集过少时应校验失败")
	}
	rules.Players = 2
	if err := rules.Validate(); err == nil {
		t.Fatal("2 人也需要至少 27 张, 应校验失败")
	}
}

// TestMatchSupportsTwoToFourPlayers 覆盖 2/3/4 人规则: 随机对局必须跑到终局,
// 且满足两个硬不变量: 牌数守恒(场上 136 张) 与 得分零和。
func TestMatchSupportsTwoToFourPlayers(t *testing.T) {
	for _, players := range []int{2, 3, 4} {
		players := players
		t.Run(seatName(players), func(t *testing.T) {
			for seed := int64(1); seed <= 8; seed++ {
				rules := DefaultRuleSet()
				rules.Players = players
				m, err := NewMatch(rules, 0, NewSeededSource(seed))
				if err != nil {
					t.Fatalf("seed %d: 创建对局失败: %v", seed, err)
				}
				rng := rand.New(rand.NewSource(seed))
				finished := false
				for step := 0; step < 4000 && !finished; step++ {
					if m.Phase() == PhaseFinished {
						finished = true
						break
					}
					if err := randomStep(m, rng); err != nil {
						t.Fatalf("seed %d step %d: 动作被拒: %v", seed, step, err)
					}
				}
				if m.Phase() != PhaseFinished {
					t.Fatalf("seed %d: 4000 步内未终局 (players=%d, phase=%s)", seed, players, m.Phase())
				}
				if got := countTiles(m); got != 136 {
					t.Fatalf("seed %d: 牌数不守恒, 场上 %d 张", seed, got)
				}
				sum := 0
				scores := m.Scores()
				for i := 0; i < players; i++ {
					sum += scores[i]
				}
				if sum != 0 {
					t.Fatalf("seed %d: 得分不零和 %v", seed, scores)
				}
				for i := players; i < 4; i++ {
					if scores[i] != 0 {
						t.Fatalf("seed %d: 未参与座位 %d 得分应为 0, 实际 %d", seed, i, scores[i])
					}
				}
			}
		})
	}
}

func seatName(players int) string {
	switch players {
	case 2:
		return "2人"
	case 3:
		return "3人"
	default:
		return "4人"
	}
}

// randomStep 走一步随机合法动作, 覆盖摸牌/打牌/响应/过牌。
func randomStep(m *Match, rng *rand.Rand) error {
	if m.turnStage == stageDraw {
		_, err := m.Draw()
		return err
	}
	if m.turnStage == stageDiscard {
		hand := m.players[m.turnSeat].hand
		if len(hand) == 0 {
			return nil
		}
		tile := hand[rng.Intn(len(hand))]
		_, err := m.Discard(m.turnSeat, tile)
		return err
	}
	// 响应窗口: 只有引擎认为可以响应的座位才会在 ViewFor().Actions 里看到动作。
	for i := 0; i < m.rules.Players; i++ {
		// 前面座位的响应可能已仲裁出结果并关闭窗口(甚至直接终局), 需重新判断。
		if m.Phase() == PhaseFinished || m.turnStage != stageRespond || m.respPending == nil {
			return nil
		}
		seat := Seat(i)
		if m.respAnswered[seat] {
			continue
		}
		acts := m.ViewFor(seat).Actions
		if len(acts) == 0 {
			continue
		}
		// 一半概率抓牌, 一半概率过; 有胡也随机选。
		if rng.Intn(2) == 0 {
			act := acts[rng.Intn(len(acts))]
			resp := RespondAction{}
			switch act {
			case "pong":
				resp.Pong = true
			case "kong.exposed", "kong.concealed", "kong.added":
				resp.Kong = true
			case "hu.robbing", "hu.self":
				resp.Win = true
			case "chow":
				// 吃需要指定搭配: 从视图里没有候选信息, 交给下面按过处理。
				resp = RespondAction{}
			default:
				resp = RespondAction{}
			}
			if _, err := m.Respond(seat, resp); err != nil {
				return err
			}
		}
	}
	return nil
}

// countTiles 统计场上所有牌: 各家手牌 + 副露 + 牌河 + 牌墙。
// 注意 Meld.Tiles 是定长 [4] 数组(顺子/碰用前 3 位), 必须按种类计数。
func countTiles(m *Match) int {
	total := m.wall.Remaining()
	for i := 0; i < m.rules.Players; i++ {
		p := m.players[i]
		if p == nil {
			continue
		}
		total += len(p.hand)
		for _, meld := range p.melds {
			switch meld.Kind {
			case MeldKongExposed, MeldKongConcealed, MeldKongAdded:
				total += 4
			default:
				total += 3
			}
		}
		total += len(m.discards[i])
	}
	return total
}
