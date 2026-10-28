package sig

import (
	"crypto/rand"
	"fmt"
)

// shortRoomCode 生成短房间码 (默认 6 位大写字母数字, 易识别、不易混淆 O/0/1/I)。
func shortRoomCode(n int) (string, error) {
	if n <= 0 {
		n = 6
	}
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去掉易混字符
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	out := make([]byte, n)
	for i := range b {
		out[i] = charset[int(b[i])%len(charset)]
	}
	return string(out), nil
}

// newPeerSuffix 为匿名 peer 生成简短随机后缀。
func newPeerSuffix() string {
	s, _ := shortRoomCode(4)
	return s
}
