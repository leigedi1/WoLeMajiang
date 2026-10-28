package secret

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Package secret 房间/邀请密码与令牌的哈希工具(仅标准库)。密码哈希: PBKDF2-HMAC-SHA256, 每密码独立随机盐。
// 存储格式: pbkdf2-sha256$<iterations>$<salt-b64>$<hash-b64>
// 说明: 选用 PBKDF2 而非裸 SHA-256, 因为房间/邀请密码由用户自选, 熵通常很低。
const (
	pbkdf2Iterations = 120000
	pbkdf2KeyLen     = 32
	pbkdf2Prefix     = "pbkdf2-sha256"
)

// HashPassword 生成密码哈希(空密码返回空串, 表示"不设置密码")。
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("secret: 生成密码盐: %w", err)
	}
	key := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyLen)
	return fmt.Sprintf("%s$%d$%s$%s", pbkdf2Prefix, pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword 校验密码。hash 为空表示该房间/邀请未设置密码, 任何密码都通过。
func VerifyPassword(hash, password string) bool {
	if hash == "" {
		return true
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != pbkdf2Prefix {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 || iter > 5_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2SHA256 标准 PBKDF2(单块输出, 足够 32 字节密钥)。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	hLen := sha256.Size
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// NewInviteToken 生成邀请令牌(返回明文与哈希; 明文只在创建时下发一次)。
func NewInviteToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("secret: 生成邀请令牌: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashInviteToken(token), nil
}

// HashInviteToken 计算邀请令牌哈希(校验时使用)。
func HashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RandomID 生成随机十六进制 ID(房间/邀请主键)。
func RandomID(bytes int) (string, error) {
	if bytes <= 0 {
		bytes = 16
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ConstantTimeEqual 常数时间比较两个等长字符串(令牌哈希比较)。
func ConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}


