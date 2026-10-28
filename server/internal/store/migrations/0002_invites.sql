-- 阶段5: 在线房间与邀请。
-- 房间增加: 名称、公开性、房间密码哈希(空串表示无密码)。
-- 新增邀请表: 令牌只保存 SHA-256 哈希, 支持独立密码、有效期、使用次数与撤销。

ALTER TABLE rooms ADD COLUMN name TEXT NOT NULL DEFAULT '';

ALTER TABLE rooms ADD COLUMN is_public INTEGER NOT NULL DEFAULT 1;

ALTER TABLE rooms ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS invites (
    id                    TEXT PRIMARY KEY,
    room_id               TEXT NOT NULL,
    token_hash            TEXT NOT NULL UNIQUE,
    password_hash         TEXT NOT NULL DEFAULT '',
    bypass_room_password  INTEGER NOT NULL DEFAULT 0,
    max_uses              INTEGER NOT NULL DEFAULT 0,
    uses                  INTEGER NOT NULL DEFAULT 0,
    revoked               INTEGER NOT NULL DEFAULT 0,
    expires_at            INTEGER NOT NULL DEFAULT 0,
    created_at            INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_invites_room ON invites(room_id);
