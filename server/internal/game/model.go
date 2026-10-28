// Package game 麻将领域数据模型(与传输/UI无关): 牌、牌墙、座位、动作、规则配置、主题。
// 本阶段仅固定类型与不变量; 洗牌/判定等规则引擎在阶段2实现。
package game

import "fmt"

// TileKind 牌的种类(34 种): 万0-8、条9-17、饼18-26、风27-30、三元31-33。
type TileKind uint8

const (
	KindWan1  TileKind = iota // 万一
	KindWan2
	KindWan3
	KindWan4
	KindWan5
	KindWan6
	KindWan7
	KindWan8
	KindWan9
	KindTiao1 // 条一(索)
	KindTiao2
	KindTiao3
	KindTiao4
	KindTiao5
	KindTiao6
	KindTiao7
	KindTiao8
	KindTiao9
	KindTong1 // 饼一(筒)
	KindTong2
	KindTong3
	KindTong4
	KindTong5
	KindTong6
	KindTong7
	KindTong8
	KindTong9
	KindEast  // 东
	KindSouth // 南
	KindWest  // 西
	KindNorth // 北
	KindRed   // 中(红中)
	KindGreen // 发(绿发)
	KindWhite // 白(白板)

	KindCount // = 34
)

// TileTotal 标准牌总数: 34 种 × 4 张 = 136。花牌(阶段8)将从 136 起扩展编号。
const TileTotal = 136

// kindCodes 种类到稳定线缆代码的映射表(顺序即 TileKind 值)。
var kindCodes = [KindCount]string{
	"wan1", "wan2", "wan3", "wan4", "wan5", "wan6", "wan7", "wan8", "wan9",
	"tiao1", "tiao2", "tiao3", "tiao4", "tiao5", "tiao6", "tiao7", "tiao8", "tiao9",
	"tong1", "tong2", "tong3", "tong4", "tong5", "tong6", "tong7", "tong8", "tong9",
	"dong", "nan", "xi", "bei",
	"zhong", "fa", "bai",
}

// Code 返回稳定线缆代码, 如 "wan1"、"dong"、"zhong"。
func (k TileKind) Code() string {
	if int(k) >= len(kindCodes) {
		return fmt.Sprintf("invalid(%d)", uint8(k))
	}
	return kindCodes[k]
}

// KindFromCode 由代码反查种类。
func KindFromCode(code string) (TileKind, bool) {
	for i, c := range kindCodes {
		if c == code {
			return TileKind(i), true
		}
	}
	return 0, false
}

// TileID 唯一物理牌编号 0..135: 种类 = id/4, 同种副本 = id%4。
// 隐藏信息(牌墙、他人手牌)以 TileID 在服务端内部流转, 投影时只对可见者转代码。
type TileID uint8

// Kind 返回该物理牌的种类。
func (id TileID) Kind() TileKind { return TileKind(id / 4) }

// Valid 报告编号是否属于标准 136 张。
func (id TileID) Valid() bool { return id < TileTotal }

// Wall 牌墙: 一次性洗定的有序牌序列与已摸指针。
// 洗牌算法与随机种子处理在阶段2实现(种子用于公平性验证与重放)。
type Wall struct {
	tiles  []TileID
	dealed int
}

// NewOrderedWall 构造按编号排序的牌墙(测试与规则校验基线)。
func NewOrderedWall() *Wall {
	tiles := make([]TileID, 0, TileTotal)
	for id := 0; id < TileTotal; id++ {
		tiles = append(tiles, TileID(id))
	}
	return &Wall{tiles: tiles}
}

// Remaining 剩余可摸牌数。
func (w *Wall) Remaining() int { return len(w.tiles) - w.dealed }

// Seat 座位编号 0..3, 与风位绑定(庄家为东风位起步)。
type Seat int

// 风位名称。
const (
	WindEast  = "dong"
	WindSouth = "nan"
	WindWest  = "xi"
	WindNorth = "bei"
)

var seatWinds = [4]string{WindEast, WindSouth, WindWest, WindNorth}

// Wind 返回座位对应风位代码。
func (s Seat) Wind() string {
	if s < 0 || int(s) >= len(seatWinds) {
		return ""
	}
	return seatWinds[s]
}

// ActionKind 玩家动作类型(线缆稳定字符串)。
type ActionKind string

const (
	ActionDiscard       ActionKind = "discard"        // 打出一张牌
	ActionChow          ActionKind = "chow"           // 吃(上家牌)
	ActionPong          ActionKind = "pong"           // 碰
	ActionKongExposed   ActionKind = "kong.exposed"   // 明杠(手牌3张+他人牌)
	ActionKongConcealed ActionKind = "kong.concealed" // 暗杠(手牌4张)
	ActionKongAdded     ActionKind = "kong.added"     // 加杠(已碰+补第4张)
	ActionHu            ActionKind = "hu"             // 和牌
	ActionPass          ActionKind = "pass"           // 过
)

// Action 一次玩家动作。
type Action struct {
	Kind ActionKind `json:"kind"`
	Seat Seat       `json:"seat"`
	Tile TileKind   `json:"tile,omitempty"`  // discard/chow/pong/kong 涉及的牌
	With []TileKind `json:"with,omitempty"`  // chow 搭配的手牌
}

// DiscardEntry 一次出牌的全局有序记录。
// 四家牌河各自成数组时会丢失「谁先谁后」, 手机端只能显示「最近几张」，
// 因此权威端额外维护一条全局出牌流水, 供「最近出牌」横条与历史面板使用。
type DiscardEntry struct {
	Seat Seat     `json:"seat"`
	Tile TileKind `json:"tile"`
}

// Phase 一局内的权威状态机阶段(阶段2实现转移规则)。
type Phase string

const (
	PhaseLobby    Phase = "lobby"    // 未开局
	PhaseDealing  Phase = "dealing"  // 发牌中
	PhaseTurn     Phase = "turn"     // 行牌回合
	PhaseFinished Phase = "finished" // 结算
)

// Theme 主题标识。
type Theme string

const (
	ThemeClassic Theme = "classic" // 经典主题
	ThemeInk     Theme = "ink"     // 水墨主题
	ThemeDIY     Theme = "diy"     // DIY 主题(自定义牌面)
)

// Valid 报告主题标识是否合法。
func (t Theme) Valid() bool { return t == ThemeClassic || t == ThemeInk || t == ThemeDIY }
