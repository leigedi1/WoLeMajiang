-- 阶段11: DIY 自定义牌面主题。
-- diy_tiles: 每张牌的自定义图片与填充方式(图片文件本身落在数据目录的 diy/ 子目录)。
-- diy_settings: 键值配置, 目前用于保存管理密码的 PBKDF2 哈希。

CREATE TABLE IF NOT EXISTS diy_tiles (
    code       TEXT PRIMARY KEY,
    file_name  TEXT NOT NULL,
    fit        TEXT NOT NULL DEFAULT 'cover',
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS diy_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
