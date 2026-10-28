package game

import "fmt"

// 首版规则预设: 基础四人麻将(PresetBasic)。
//
// 牌组: 136 张 = 万/条/饼各 1~9 × 4 + 东南西北 × 4 + 中发白 × 4。
// 起手: 每人 13 张, 庄家开局先摸第 14 张。
// 胡牌牌型: 标准形(4 面子 + 1 对子); 七对子(开关, 严格 7 种各 2 张)。
//   面子 = 顺子(同花色连续三张) 或 刻子(同种三张)。
// 响应优先级: 胡 > 碰/明杠 > 吃(仅上家打出且自己是下家); 多家可胡时
//   以出牌者的下家为起点顺座位序最近者胡(一炮多响不启用)。
// 流局: 牌墙摸完即流局(不留尾牌), 无听牌赔付, 庄家不连庄(轮换由上层处理)。
// 计分(简化, 明确非地域规则): 支付 = 底分 × 2^总番。
//   点炮: 放炮者全额支付; 自摸: 其余每家各支付。
//   番数表: 和牌基础 1 番; 七对子 +2 番; 自摸 +1 番; 门清 +1 番(七对必门清不重复计)。
//   门清定义: 无吃/碰/明杠/补杠(暗杠不破坏门清)。
//
// 首版未覆盖(后续阶段扩展, 此处明确不混入): 十三幺、清一色、混一色、字一色、
// 碰碰胡、断幺九、全求人、抢杠(含抢杠胡)、杠上开花、海底捞月/河底捞鱼、
// 连风位番、役满体系、庄家连庄赔付、流局听牌赔付、花牌(阶段8)。
//
// RuleSet 为可验证的结构化配置; 冲突规则在 Validate 中显式拒绝; 不执行任何用户脚本。
type RuleSet struct {
	Players           int  `json:"players"`             // 2~4
	Chow              bool `json:"chow"`                // 允许吃
	SevenPairs        bool `json:"seven_pairs"`         // 允许七对子
	SelfDraw          bool `json:"self_draw"`           // 允许自摸
	DiscardWin        bool `json:"discard_win"`         // 允许点炮(荣和)
	ConcealedHandOnly bool `json:"concealed_hand_only"` // 仅门清可和
	BaseScore         int  `json:"base_score"`          // 底分(支付 = 底分 × 2^总番)

	// 计时规则(阶段10: 响应/出牌倒计时)。
	// RespondMeldSec/RespondWinSec 为 0 表示"无倒计时": 响应窗口不再自动过期,
	// 改由下家的「摸牌」按钮强制收敛; 非 0 时不显示摸牌按钮。
	// DiscardSec 为 0 表示出牌阶段不限时(一直等待该玩家出牌)。
	RespondMeldSec int `json:"respond_meld_sec"` // 碰/杠/吃 响应倒计时(0~30 秒), 默认 3
	RespondWinSec  int `json:"respond_win_sec"`  // 胡(荣和)响应倒计时(0~30 秒), 默认 5
	DiscardSec     int `json:"discard_sec"`      // 出牌倒计时(0 关闭; 3~120 秒)

	// 优先级仲裁开关(阶段10)。
	RobWin  bool `json:"rob_win"`  // 截胡: 开=按出牌顺序(离出牌者最近者)优先; 关=先按者得
	RobMeld bool `json:"rob_meld"` // 截碰/截杠: 同上

	// 时序型番种开关(阶段11)。
	RobKongWin bool `json:"rob_kong_win"` // 抢杠胡: 补杠时可被他人荣和
	KongFlower bool `json:"kong_flower"`  // 杠上开花: 杠后补牌自摸
	LastTile   bool `json:"last_tile"`    // 海底捞月 / 河底捞鱼: 牌墙最后一张自摸 / 最后一张出牌被荣和

	// Pool 自定义牌集: 牌种代码("wan1".."bai") → 每种张数(1~4)。
	// 省略或为空表示标准 136 张(数牌/字牌各 4 张)。
	Pool map[string]int `json:"pool,omitempty"`
}

// DefaultRuleSet 默认规则: 基础四人麻将。
func DefaultRuleSet() RuleSet {
	return RuleSet{
		Players:    4,
		Chow:       true,
		SevenPairs: true,
		SelfDraw:   true,
		DiscardWin: true,
		BaseScore:  1,

		// 默认: 碰/杠 3 秒、胡 5 秒的响应倒计时; 出牌阶段不限时(开关关闭)。
		RespondMeldSec: 3,
		RespondWinSec:  5,
		DiscardSec:     0,

		// 默认开启截胡/截碰(按出牌顺序优先)。
		RobWin:  true,
		RobMeld: true,

		// 默认开启时序型番种。
		RobKongWin: true,
		KongFlower: true,
		LastTile:   true,
	}
}

// PoolKinds 返回自定义牌集展开后的物理牌列表; 未配置时返回标准 136 张。
// 编号规则沿用 TileID: 种类 × 4 + 副本序, 因此每种最多 4 张。
func (r RuleSet) PoolKinds() []TileID {
	if len(r.Pool) == 0 {
		ids := make([]TileID, 0, TileTotal)
		for id := 0; id < TileTotal; id++ {
			ids = append(ids, TileID(id))
		}
		return ids
	}
	ids := make([]TileID, 0, TileTotal)
	for code, n := range r.Pool {
		k, ok := KindFromCode(code)
		if !ok || n <= 0 {
			continue
		}
		if n > 4 {
			n = 4
		}
		for c := 0; c < n; c++ {
			ids = append(ids, TileID(int(k)*4+c))
		}
	}
	return ids
}

// PoolTotal 返回自定义牌集的总张数。
func (r RuleSet) PoolTotal() int { return len(r.PoolKinds()) }

// Validate 校验规则配置, 冲突规则必须拒绝。
func (r RuleSet) Validate() error {
	if r.Players < 2 || r.Players > 4 {
		return fmt.Errorf("players 必须在 2~4 之间, 实际 %d", r.Players)
	}
	if len(r.Pool) > 0 {
		total := 0
		for code, n := range r.Pool {
			if _, ok := KindFromCode(code); !ok {
				return fmt.Errorf("未知牌种: %s", code)
			}
			if n < 0 || n > 4 {
				return fmt.Errorf("牌 %s 的张数必须在 0~4 之间, 实际 %d", code, n)
			}
			total += n
		}
		if min := 13*r.Players + 1; total < min {
			return fmt.Errorf("牌集张数不足: 需要至少 %d 张, 当前 %d 张", min, total)
		}
	}
	if !r.SelfDraw && !r.DiscardWin {
		return fmt.Errorf("冲突规则: self_draw 与 discard_win 均关闭则无人能够和牌")
	}
	if r.ConcealedHandOnly && !r.SelfDraw {
		// 门清限定下, 荣和虽然可以是门清牌型, 但若同时禁止自摸则实际不可达, 视为冲突。
		return fmt.Errorf("冲突规则: concealed_hand_only 且禁止 self_draw 时和牌不可达")
	}
	if r.BaseScore < 0 || r.BaseScore > 100000 {
		return fmt.Errorf("base_score 必须在 0~100000 之间, 实际 %d", r.BaseScore)
	}
	if r.RespondMeldSec < 0 || r.RespondMeldSec > 30 {
		return fmt.Errorf("respond_meld_sec 必须在 0~30 之间, 实际 %d", r.RespondMeldSec)
	}
	if r.RespondWinSec < 0 || r.RespondWinSec > 30 {
		return fmt.Errorf("respond_win_sec 必须在 0~30 之间, 实际 %d", r.RespondWinSec)
	}
	if r.DiscardSec < 0 || r.DiscardSec > 120 {
		return fmt.Errorf("discard_sec 必须在 0~120 之间, 实际 %d", r.DiscardSec)
	}
	if r.DiscardSec > 0 && r.DiscardSec < 3 {
		return fmt.Errorf("discard_sec 开启时必须在 3~120 秒之间, 实际 %d", r.DiscardSec)
	}
	return nil
}

// RespondEnabled 响应窗口是否启用倒计时(碰/杠或胡任一非 0)。
func (r RuleSet) RespondEnabled() bool { return r.RespondMeldSec > 0 || r.RespondWinSec > 0 }

// ResponseSecondsFor 返回某座位在响应窗口内的倒计时秒数:
// 同时可胡可碰时取较长者(胡), 保证高优先级动作不会先被低优先级的短倒计时吞掉。
// 返回 0 表示该座位不应有倒计时。
func (r RuleSet) ResponseSecondsFor(actions []string) int {
	sec := 0
	for _, a := range actions {
		switch a {
		case "hu.robbing":
			if r.RespondWinSec > sec {
				sec = r.RespondWinSec
			}
		case "pong", "kong.exposed", "chow":
			if r.RespondMeldSec > sec {
				sec = r.RespondMeldSec
			}
		}
	}
	return sec
}
