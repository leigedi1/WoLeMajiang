package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DIY 主题的持久化: 每张牌的自定义图片(文件落在数据目录 diy/ 下)与管理密码哈希。

// DIYTileRow 一张牌的自定义样式。
type DIYTileRow struct {
	Code      string
	FileName  string
	Fit       string
	UpdatedAt int64
}

// DIY 图片填充方式白名单(与前端 object-fit 取值一致)。
var diyFits = map[string]bool{
	"cover":   true, // 填充: 铺满并裁掉溢出
	"contain": true, // 完整显示: 等比缩放后留白
	"fill":    true, // 拉伸: 铺满但可能变形
	"none":    true, // 原始尺寸: 居中不缩放
	"scale-down": true, // 缩小: 只在超出时等比缩小
}

// NormalizeDIYFit 归一化填充方式, 非法值退回 cover。
func NormalizeDIYFit(fit string) string {
	if diyFits[fit] {
		return fit
	}
	return "cover"
}

// ListDIYTiles 返回所有已自定义的牌。
func (s *Store) ListDIYTiles(ctx context.Context) ([]DIYTileRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT code, file_name, fit, updated_at FROM diy_tiles ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("store: 读取 DIY 牌面: %w", err)
	}
	defer rows.Close()
	var out []DIYTileRow
	for rows.Next() {
		var r DIYTileRow
		if err := rows.Scan(&r.Code, &r.FileName, &r.Fit, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: 读取 DIY 牌面: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 读取 DIY 牌面: %w", err)
	}
	return out, nil
}

// GetDIYTile 读取单张牌的自定义样式; 未自定义时返回 (nil, nil)。
func (s *Store) GetDIYTile(ctx context.Context, code string) (*DIYTileRow, error) {
	var r DIYTileRow
	err := s.db.QueryRowContext(ctx,
		`SELECT code, file_name, fit, updated_at FROM diy_tiles WHERE code = ?`, code).
		Scan(&r.Code, &r.FileName, &r.Fit, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: 读取 DIY 牌面: %w", err)
	}
	return &r, nil
}

// SaveDIYTile 写入/更新一张牌的自定义样式, 返回被替换掉的旧文件名(可能为空)。
func (s *Store) SaveDIYTile(ctx context.Context, code, fileName, fit string) (string, error) {
	old, err := s.GetDIYTile(ctx, code)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO diy_tiles(code, file_name, fit, updated_at) VALUES(?, ?, ?, ?)
ON CONFLICT(code) DO UPDATE SET file_name = excluded.file_name,
                                fit = excluded.fit,
                                updated_at = excluded.updated_at`,
		code, fileName, NormalizeDIYFit(fit), time.Now().Unix())
	if err != nil {
		return "", fmt.Errorf("store: 保存 DIY 牌面: %w", err)
	}
	if old != nil {
		return old.FileName, nil
	}
	return "", nil
}

// DeleteDIYTile 删除一张牌的自定义样式, 返回原文件名以便调用方清理文件。
func (s *Store) DeleteDIYTile(ctx context.Context, code string) (string, error) {
	old, err := s.GetDIYTile(ctx, code)
	if err != nil || old == nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM diy_tiles WHERE code = ?`, code); err != nil {
		return "", fmt.Errorf("store: 删除 DIY 牌面: %w", err)
	}
	return old.FileName, nil
}

// DIYPasswordHash 读取管理密码哈希; 未设置时返回空串。
func (s *Store) DIYPasswordHash(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM diy_settings WHERE key = 'password_hash'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: 读取 DIY 密码: %w", err)
	}
	return v, nil
}

// SetDIYPasswordHash 写入管理密码哈希。
func (s *Store) SetDIYPasswordHash(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO diy_settings(key, value, updated_at) VALUES('password_hash', ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		hash, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("store: 保存 DIY 密码: %w", err)
	}
	return nil
}
