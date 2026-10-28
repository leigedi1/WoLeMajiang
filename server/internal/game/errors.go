package game

import "errors"

// 引擎错误(传输层据此映射稳定错误码)。
var (
	ErrMatchOver       = errors.New("对局已结束")
	ErrNotStarted      = errors.New("对局尚未开始")
	ErrWrongPhase      = errors.New("当前阶段不接受该操作")
	ErrNotYourTurn     = errors.New("未轮到该玩家行动")
	ErrNotResponder    = errors.New("当前不在该玩家的响应窗口")
	ErrAlreadyResponded = errors.New("该玩家已提交响应")
	ErrTileNotInHand   = errors.New("所指定的牌不在手牌中")
	ErrIllegalMeld     = errors.New("副露不合法")
	ErrIllegalChow     = errors.New("吃牌组合不合法")
	ErrWinNotAllowed   = errors.New("当前规则下不允许该方式和牌")
	ErrIllegalAction   = errors.New("非法动作")
)
