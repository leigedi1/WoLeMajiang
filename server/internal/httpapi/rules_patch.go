package httpapi

import "majiang-server/internal/game"

// ruleSetPatch 房间创建时客户端提交的规则补丁。
// 全部字段用指针: 未提交的字段保留服务端默认值, 提交 false/0 才算显式关闭。
// 这样阶段10 之后新增的规则字段不会被旧客户端覆盖成零值。
type ruleSetPatch struct {
	Players           *int            `json:"players"`
	Chow              *bool           `json:"chow"`
	SevenPairs        *bool           `json:"seven_pairs"`
	SelfDraw          *bool           `json:"self_draw"`
	DiscardWin        *bool           `json:"discard_win"`
	ConcealedHandOnly *bool           `json:"concealed_hand_only"`
	BaseScore         *int            `json:"base_score"`
	RespondMeldSec    *int            `json:"respond_meld_sec"`
	RespondWinSec     *int            `json:"respond_win_sec"`
	DiscardSec        *int            `json:"discard_sec"`
	RobWin            *bool           `json:"rob_win"`
	RobMeld           *bool           `json:"rob_meld"`
	RobKongWin        *bool           `json:"rob_kong_win"`
	KongFlower        *bool           `json:"kong_flower"`
	LastTile          *bool           `json:"last_tile"`
	Pool              map[string]int  `json:"pool"`
}

// apply 把补丁叠加到基础规则上。
func (p ruleSetPatch) apply(base game.RuleSet) game.RuleSet {
	if p.Players != nil {
		base.Players = *p.Players
	}
	if p.Chow != nil {
		base.Chow = *p.Chow
	}
	if p.SevenPairs != nil {
		base.SevenPairs = *p.SevenPairs
	}
	if p.SelfDraw != nil {
		base.SelfDraw = *p.SelfDraw
	}
	if p.DiscardWin != nil {
		base.DiscardWin = *p.DiscardWin
	}
	if p.ConcealedHandOnly != nil {
		base.ConcealedHandOnly = *p.ConcealedHandOnly
	}
	if p.BaseScore != nil {
		base.BaseScore = *p.BaseScore
	}
	if p.RespondMeldSec != nil {
		base.RespondMeldSec = *p.RespondMeldSec
	}
	if p.RespondWinSec != nil {
		base.RespondWinSec = *p.RespondWinSec
	}
	if p.DiscardSec != nil {
		base.DiscardSec = *p.DiscardSec
	}
	if p.RobWin != nil {
		base.RobWin = *p.RobWin
	}
	if p.RobMeld != nil {
		base.RobMeld = *p.RobMeld
	}
	if p.RobKongWin != nil {
		base.RobKongWin = *p.RobKongWin
	}
	if p.KongFlower != nil {
		base.KongFlower = *p.KongFlower
	}
	if p.LastTile != nil {
		base.LastTile = *p.LastTile
	}
	if len(p.Pool) > 0 {
		base.Pool = p.Pool
	}
	return base
}
