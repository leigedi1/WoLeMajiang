-- 0001_init: 游客身份与房间元数据基础表
CREATE TABLE IF NOT EXISTS guests (
    id           TEXT PRIMARY KEY,          -- 游客 ID(16 字节随机 hex)
    nickname     TEXT NOT NULL,             -- 昵称(1~24 可见字符)
    token_hash   TEXT NOT NULL,             -- 重连令牌 SHA-256 hex(令牌本体不落库)
    created_at   INTEGER NOT NULL,          -- Unix 秒
    last_seen_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS rooms (
    id            TEXT PRIMARY KEY,         -- 房间 ID(8 字节随机 hex)
    code          TEXT NOT NULL UNIQUE,     -- 6 位邀请码
    host_guest_id TEXT NOT NULL REFERENCES guests(id),
    mode          TEXT NOT NULL DEFAULT 'online',  -- online | p2p | lan
    rule_set      TEXT NOT NULL DEFAULT '{}',      -- 规则配置 JSON,阶段 2/8 细化
    theme         TEXT NOT NULL DEFAULT 'classic', -- classic | ink
    status        TEXT NOT NULL DEFAULT 'lobby',   -- lobby | playing | finished
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rooms_updated ON rooms(updated_at);
