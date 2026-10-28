package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// NicknameMinLength / NicknameMaxLength 游客昵称长度(可见字符)范围。
const (
	NicknameMinLength = 1
	NicknameMaxLength = 24
)

// Guest 游客身份。
type Guest struct {
	ID         string
	Nickname   string
	CreatedAt  int64 // Unix 秒
	LastSeenAt int64
}

// ValidateNickname 校验昵称: 去除首尾空白后 1~24 个字符, 不含控制字符。
func ValidateNickname(raw string) (string, error) {
	nick := strings.TrimSpace(raw)
	runes := []rune(nick)
	if len(runes) < NicknameMinLength {
		return "", errors.New("昵称不能为空")
	}
	if len(runes) > NicknameMaxLength {
		return "", fmt.Errorf("昵称最长 %d 个字符", NicknameMaxLength)
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return "", errors.New("昵称不能包含控制字符")
		}
	}
	return nick, nil
}

// CreateGuest 创建游客身份, 返回游客与一次性下发的重连令牌。
// 令牌本体仅在创建时返回一次; 数据库只保存 SHA-256 哈希。
func (s *Store) CreateGuest(ctx context.Context, nickname string) (Guest, string, error) {
	nick, err := ValidateNickname(nickname)
	if err != nil {
		return Guest{}, "", err
	}
	id := randomHex(16)
	token := randomHex(32)
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO guests(id, nickname, token_hash, created_at, last_seen_at)
VALUES(?, ?, ?, ?, ?)`, id, nick, sha256Hex(token), now, now)
	if err != nil {
		return Guest{}, "", fmt.Errorf("store: 写入游客: %w", err)
	}
	return Guest{ID: id, Nickname: nick, CreatedAt: now, LastSeenAt: now}, token, nil
}

// VerifyGuest 用重连令牌校验游客身份(常数时间比较哈希)。
func (s *Store) VerifyGuest(ctx context.Context, guestID, token string) (Guest, bool, error) {
	if guestID == "" || token == "" {
		return Guest{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, nickname, token_hash, created_at, last_seen_at
FROM guests WHERE id = ?`, guestID)
	var g Guest
	var hash string
	err := row.Scan(&g.ID, &g.Nickname, &hash, &g.CreatedAt, &g.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Guest{}, false, nil
	}
	if err != nil {
		return Guest{}, false, fmt.Errorf("store: 查询游客: %w", err)
	}
	if !constantTimeEqual(hash, sha256Hex(token)) {
		return Guest{}, false, nil
	}
	return g, true, nil
}

// TouchGuest 更新游客活跃时间。
func (s *Store) TouchGuest(ctx context.Context, guestID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE guests SET last_seen_at = ? WHERE id = ?`, time.Now().Unix(), guestID)
	if err != nil {
		return fmt.Errorf("store: 更新游客活跃时间: %w", err)
	}
	return nil
}

// CountGuests 统计游客总数(健康检查展示用)。
func (s *Store) CountGuests(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM guests`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: 统计游客: %w", err)
	}
	return n, nil
}
