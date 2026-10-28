package store

import (
	"context"
	"fmt"
	"time"
)

// InviteRow 邀请持久化行。令牌只保存哈希, 独立密码同样只保存哈希。
type InviteRow struct {
	ID                 string
	RoomID             string
	TokenHash          string
	PasswordHash       string
	BypassRoomPassword bool
	MaxUses            int
	Uses               int
	Revoked            bool
	ExpiresAt          int64
	CreatedAt          int64
}

// CreateInvite 写入邀请。
func (s *Store) CreateInvite(ctx context.Context, row InviteRow) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO invites(id, room_id, token_hash, password_hash, bypass_room_password,
                    max_uses, uses, revoked, expires_at, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.RoomID, row.TokenHash, row.PasswordHash,
		boolToInt(row.BypassRoomPassword), row.MaxUses, row.Uses,
		boolToInt(row.Revoked), row.ExpiresAt, row.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: 写入邀请: %w", err)
	}
	return nil
}

// ConsumeInvite 原子地占用一次使用次数(uses < max_uses 或 max_uses = 0 表示不限)。
// 返回 false 表示已用尽/已撤销/已过期, 调用方据此拒绝加入。
func (s *Store) ConsumeInvite(ctx context.Context, id string, now int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE invites SET uses = uses + 1
WHERE id = ? AND revoked = 0
  AND (expires_at = 0 OR expires_at > ?)
  AND (max_uses = 0 OR uses < max_uses)`, id, now)
	if err != nil {
		return false, fmt.Errorf("store: 占用邀请: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: 占用邀请结果: %w", err)
	}
	return n > 0, nil
}

// RevokeInvite 撤销邀请。
func (s *Store) RevokeInvite(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE invites SET revoked = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: 撤销邀请: %w", err)
	}
	return nil
}

// ListInvitesByRoom 列出房间的邀请(不含令牌明文, 只有哈希)。
func (s *Store) ListInvitesByRoom(ctx context.Context, roomID string) ([]InviteRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, room_id, token_hash, password_hash, bypass_room_password,
       max_uses, uses, revoked, expires_at, created_at
FROM invites WHERE room_id = ? ORDER BY created_at DESC`, roomID)
	if err != nil {
		return nil, fmt.Errorf("store: 查询邀请: %w", err)
	}
	defer rows.Close()
	var out []InviteRow
	for rows.Next() {
		var row InviteRow
		var bypass, revoked int
		if err := rows.Scan(&row.ID, &row.RoomID, &row.TokenHash, &row.PasswordHash,
			&bypass, &row.MaxUses, &row.Uses, &revoked, &row.ExpiresAt, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: 扫描邀请: %w", err)
		}
		row.BypassRoomPassword = bypass != 0
		row.Revoked = revoked != 0
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 遍历邀请: %w", err)
	}
	return out, nil
}

// DeleteInvitesByRoom 删除房间全部邀请(房间解散时调用)。
func (s *Store) DeleteInvitesByRoom(ctx context.Context, roomID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE room_id = ?`, roomID); err != nil {
		return fmt.Errorf("store: 删除邀请: %w", err)
	}
	return nil
}

// TouchRoom 更新房间状态/名称/公开性/密码哈希与活跃时间。
func (s *Store) TouchRoom(ctx context.Context, id, status, name string, isPublic bool, passwordHash string) error {
	if _, err := s.db.ExecContext(ctx, `
UPDATE rooms SET status = ?, name = ?, is_public = ?, password_hash = ?, updated_at = ?
WHERE id = ?`, status, name, boolToInt(isPublic), passwordHash, time.Now().Unix(), id); err != nil {
		return fmt.Errorf("store: 更新房间: %w", err)
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
