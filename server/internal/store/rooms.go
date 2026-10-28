package store

import (
	"context"
	"fmt"
	"time"
)

// RoomRow 房间元数据持久化行(仅元数据; 权威对局状态在内存中, 阶段7做恢复)。
type RoomRow struct {
	ID           string
	Code         string
	HostGuestID  string
	Mode         string
	RuleSet      string // JSON
	Theme        string
	Status       string
	Name         string
	IsPublic     bool
	PasswordHash string
	CreatedAt    int64
	UpdatedAt    int64
}

// CreateRoom 写入房间元数据。
func (s *Store) CreateRoom(ctx context.Context, row RoomRow) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO rooms(id, code, host_guest_id, mode, rule_set, theme, status,
                  name, is_public, password_hash, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.Code, row.HostGuestID, row.Mode, row.RuleSet, row.Theme, row.Status,
		row.Name, boolToInt(row.IsPublic), row.PasswordHash, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: 写入房间: %w", err)
	}
	return nil
}

// DeleteRoom 删除房间元数据。
func (s *Store) DeleteRoom(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: 删除房间: %w", err)
	}
	return nil
}

// UpdateRoomActivity 更新房间状态与活跃时间。
func (s *Store) UpdateRoomActivity(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE rooms SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("store: 更新房间: %w", err)
	}
	return nil
}

// DeleteStaleRooms 删除活跃时间早于 cutoffUnix 的房间元数据。
// 启动时调用: 服务器停机期间超过空房 TTL 的房间视为已过期, 元数据一并清除。
func (s *Store) DeleteStaleRooms(ctx context.Context, cutoffUnix int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE updated_at < ?`, cutoffUnix)
	if err != nil {
		return 0, fmt.Errorf("store: 清理过期房间: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
