package game

// MeldKind 副露类型。
type MeldKind string

const (
	MeldChow          MeldKind = "chow"           // 吃(顺子, 来自上家)
	MeldPong          MeldKind = "pong"           // 碰(刻子)
	MeldKongExposed   MeldKind = "kong.exposed"   // 明杠(他人打出的第4张)
	MeldKongConcealed MeldKind = "kong.concealed" // 暗杠(自家手牌4张)
	MeldKongAdded     MeldKind = "kong.added"     // 补杠(已碰后补第4张)
)

// Meld 一组已亮明的副露(公开信息, 以种类表示)。
type Meld struct {
	Kind  MeldKind
	Tiles [4]TileKind // chow/pong 用前 2/3 位; 其余位为零值
	From  Seat        // 来源座位(吃/碰/明杠的被取牌者); 暗杠/补杠无来源
}

// MeldView 副露投影(线缆稳定字符串)。
type MeldView struct {
	Kind  string   `json:"kind"`
	Tiles []string `json:"tiles"` // 顺序: 顺子按点数升序; 杠含 4 张
}

// HiddenTileCode 暗杠对外隐藏时的占位牌码(不可与任何真实种类混淆)。
const HiddenTileCode = "back"

// view 生成副露投影(以"出牌者本人"视角, 即暗杠可见)。
func (m Meld) view() MeldView { return m.viewFor(true) }

// viewFor 按观察者是否为持有者生成副露投影。
//
// 安全边界: 暗杠(4 张全部来自自家手牌)对其他人必须隐藏具体牌面。
// 这里统一用 HiddenTileCode 占位, 由 UI 渲染成牌背; 吃/碰/明杠/补杠
// 本身即公开信息, 照常投影。AI 侧解析未知牌码时会忽略该占位,
// 因此不会获得暗杠牌的额外信息。
func (m Meld) viewFor(owner bool) MeldView {
	if m.Kind == MeldKongConcealed && !owner {
		return MeldView{Kind: string(m.Kind), Tiles: []string{
			HiddenTileCode, HiddenTileCode, HiddenTileCode, HiddenTileCode,
		}}
	}
	n := 3
	if m.Kind == MeldKongExposed || m.Kind == MeldKongConcealed || m.Kind == MeldKongAdded {
		n = 4
	}
	tiles := make([]string, 0, n)
	for i := 0; i < n; i++ {
		tiles = append(tiles, m.Tiles[i].Code())
	}
	return MeldView{Kind: string(m.Kind), Tiles: tiles}
}

// isCalled 报告该副露是否破坏门清(吃/碰/明杠/补杠破坏; 暗杠保持门清)。
func (m Meld) isCalled() bool {
	switch m.Kind {
	case MeldChow, MeldPong, MeldKongExposed, MeldKongAdded:
		return true
	default:
		return false
	}
}

// chowRun 校验三张同花色连续牌(顺子), 返回排序后的种类。
func chowRun(a, b, c TileKind) ([3]TileKind, bool) {
	// 非数字牌不可能成顺。
	if !isSuitKind(a) || !isSuitKind(b) || !isSuitKind(c) {
		return [3]TileKind{}, false
	}
	ra, rb, rc := int(a)%9, int(b)%9, int(c)%9
	sa, sb, sc := int(a)/9, int(b)/9, int(c)/9
	if sa != sb || sb != sc {
		return [3]TileKind{}, false
	}
	// 排序后必须连续。
	ranks := []int{ra, rb, rc}
	for i := 1; i < len(ranks); i++ {
		for j := i; j > 0 && ranks[j] < ranks[j-1]; j-- {
			ranks[j], ranks[j-1] = ranks[j-1], ranks[j]
		}
	}
	if ranks[1] != ranks[0]+1 || ranks[2] != ranks[1]+1 {
		return [3]TileKind{}, false
	}
	base := TileKind(sa*9 + ranks[0])
	return [3]TileKind{base, base + 1, base + 2}, true
}
