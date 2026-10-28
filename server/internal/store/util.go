package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// randomHex 生成 n 字节密码学随机数的 hex 字符串(2n 字符)。
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败属于系统级故障, 直接 panic 优于静默降级为可预测值。
		panic("store: 系统随机源不可用: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// sha256Hex 计算 SHA-256 摘要的 hex。
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// constantTimeEqual 常数时间字符串比较。
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
