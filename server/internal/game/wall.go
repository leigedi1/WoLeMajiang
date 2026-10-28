package game

import (
	crand "crypto/rand"
	"encoding/binary"
	mrand "math/rand"
)

// RandomSource 可注入随机源(便于测试重放)。math/rand 的 *rand.Rand 天然满足。
type RandomSource interface {
	Int63n(n int64) int64
}

// NewSeededSource 创建确定性随机源(测试用)。
func NewSeededSource(seed int64) RandomSource {
	return mrand.New(mrand.NewSource(seed)) //nolint:gosec // 测试与重放用途
}

// NewCryptoSource 创建密码学种子随机源(生产用)。
func NewCryptoSource() RandomSource {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic("game: 系统随机源不可用: " + err.Error())
	}
	return mrand.New(mrand.NewSource(int64(binary.LittleEndian.Uint64(b[:])))) //nolint:gosec // 种子来自 crypto/rand
}

// NewShuffledWall 用给定随机源构造洗好的牌墙(Fisher-Yates)。
// 种子确定时牌序确定, 便于公平性重放与测试。
func NewShuffledWall(src RandomSource) *Wall { return NewShuffledWallOf(DefaultPoolKinds(), src) }

// DefaultPoolKinds 标准 136 张(34 种 × 4)。
func DefaultPoolKinds() []TileID {
	ids := make([]TileID, 0, TileTotal)
	for id := 0; id < TileTotal; id++ {
		ids = append(ids, TileID(id))
	}
	return ids
}

// NewShuffledWallOf 用给定的物理牌列表构造洗好的牌墙(支持自定义牌集)。
func NewShuffledWallOf(ids []TileID, src RandomSource) *Wall {
	tiles := make([]TileID, len(ids))
	copy(tiles, ids)
	for i := len(tiles) - 1; i > 0; i-- {
		j := int(src.Int63n(int64(i + 1)))
		tiles[i], tiles[j] = tiles[j], tiles[i]
	}
	return &Wall{tiles: tiles}
}

// Draw 摸一张牌; 牌墙耗尽时返回 false。
func (w *Wall) Draw() (TileID, bool) {
	if w.dealed >= len(w.tiles) {
		return 0, false
	}
	t := w.tiles[w.dealed]
	w.dealed++
	return t, true
}
