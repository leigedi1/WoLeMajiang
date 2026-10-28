// Package config 服务器配置: 默认值 -> JSON 文件(可选) -> 环境变量,最后统一校验。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// HTTP HTTP 服务配置。
type HTTP struct {
	Listen         string   `json:"listen"`          // 监听地址
	AllowedOrigins []string `json:"allowed_origins"` // 跨域来源白名单(开发环境前端地址)
}

// Database SQLite 配置。
type Database struct {
	Path string `json:"path"`
}

// Rooms 房间管理配置。
type Rooms struct {
	MaxOnline         int `json:"max_online"`             // 在线房间上限(服务器权威房间),默认 3
	IdleTTLSeconds    int `json:"idle_ttl_seconds"`       // 空房清理时间
	SweepIntervalSecs int `json:"sweep_interval_seconds"` // 空房扫描间隔
}

// IdleTTL 空房存活时长。
func (r Rooms) IdleTTL() time.Duration { return time.Duration(r.IdleTTLSeconds) * time.Second }

// SweepInterval 扫描间隔。
func (r Rooms) SweepInterval() time.Duration {
	return time.Duration(r.SweepIntervalSecs) * time.Second
}

// Limits WS 消息限制配置。
type Limits struct {
	MaxMessageBytes int     `json:"max_message_bytes"` // 单条消息字节上限
	MessageBurst    int     `json:"message_burst"`     // 令牌桶容量
	MessagesPerSec  float64 `json:"messages_per_second"`
}

// Log 日志配置。
type Log struct {
	Level string `json:"level"`
}

// DIY 自定义牌面主题配置。
type DIY struct {
	// Dir 自定义图片存放目录(相对路径按进程工作目录解析)。
	Dir string `json:"dir"`
	// MaxImageBytes 单张图片字节上限。
	MaxImageBytes int `json:"max_image_bytes"`
	// Password 初始管理密码; 仅在数据库尚未保存过密码时生效, 之后由设置页维护。
	Password string `json:"password"`
}

// Config 服务器总配置。
type Config struct {
	HTTP     HTTP     `json:"http"`
	Database Database `json:"database"`
	Rooms    Rooms    `json:"rooms"`
	Limits   Limits   `json:"limits"`
	Log      Log      `json:"log"`
	DIY      DIY      `json:"diy"`
}

// Default 默认配置(与 config.example.json 一致)。
func Default() Config {
	return Config{
		HTTP: HTTP{
			Listen: "127.0.0.1:8080",
			AllowedOrigins: []string{
				"http://localhost:5173",
				"http://127.0.0.1:5173",
			},
		},
		Database: Database{Path: "data/majiang.db"},
		Rooms:    Rooms{MaxOnline: 3, IdleTTLSeconds: 900, SweepIntervalSecs: 60},
		Limits:   Limits{MaxMessageBytes: 64 * 1024, MessageBurst: 30, MessagesPerSec: 10},
		Log:      Log{Level: "info"},
		DIY:      DIY{Dir: "data/diy", MaxImageBytes: 2 * 1024 * 1024},
	}
}

// Load 依次应用默认值、JSON 配置文件(可选)与环境变量,并校验。
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("读取配置文件: %w", err)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("解析配置文件 %s: %w", path, err)
		}
	}
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("MAJIANG_HTTP_LISTEN"); v != "" {
		c.HTTP.Listen = v
	}
	if v := os.Getenv("MAJIANG_DB_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("MAJIANG_ALLOWED_ORIGINS"); v != "" {
		var list []string
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				list = append(list, p)
			}
		}
		if list != nil {
			c.HTTP.AllowedOrigins = list
		}
	}
	setEnvInt("MAJIANG_MAX_ONLINE_ROOMS", &c.Rooms.MaxOnline)
	setEnvInt("MAJIANG_ROOM_IDLE_TTL_SECONDS", &c.Rooms.IdleTTLSeconds)
	setEnvInt("MAJIANG_SWEEP_INTERVAL_SECONDS", &c.Rooms.SweepIntervalSecs)
	setEnvInt("MAJIANG_MAX_MESSAGE_BYTES", &c.Limits.MaxMessageBytes)
	setEnvInt("MAJIANG_MESSAGE_BURST", &c.Limits.MessageBurst)
	if v := os.Getenv("MAJIANG_MESSAGES_PER_SECOND"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Limits.MessagesPerSec = f
		}
	}
	if v := os.Getenv("MAJIANG_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
	if v := os.Getenv("MAJIANG_DIY_DIR"); v != "" {
		c.DIY.Dir = v
	}
	if v := os.Getenv("MAJIANG_DIY_PASSWORD"); v != "" {
		c.DIY.Password = v
	}
	setEnvInt("MAJIANG_DIY_MAX_IMAGE_BYTES", &c.DIY.MaxImageBytes)
}

func setEnvInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

// Validate 校验配置合法性。
func (c Config) Validate() error {
	switch {
	case c.HTTP.Listen == "":
		return errors.New("http.listen 不能为空")
	case c.Database.Path == "":
		return errors.New("database.path 不能为空")
	case c.Rooms.MaxOnline < 1:
		return errors.New("rooms.max_online 必须 >= 1")
	case c.Rooms.IdleTTLSeconds <= 0:
		return errors.New("rooms.idle_ttl_seconds 必须 > 0")
	case c.Rooms.SweepIntervalSecs <= 0:
		return errors.New("rooms.sweep_interval_seconds 必须 > 0")
	case c.Limits.MaxMessageBytes < 1024:
		return errors.New("limits.max_message_bytes 必须 >= 1024")
	case c.Limits.MessageBurst < 1:
		return errors.New("limits.message_burst 必须 >= 1")
	case c.Limits.MessagesPerSec <= 0:
		return errors.New("limits.messages_per_second 必须 > 0")
	case c.DIY.Dir == "":
		return errors.New("diy.dir 不能为空")
	case c.DIY.MaxImageBytes < 64*1024:
		return errors.New("diy.max_image_bytes 必须 >= 65536")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level 非法: %q", c.Log.Level)
	}
	seen := map[string]bool{}
	for _, o := range c.HTTP.AllowedOrigins {
		if seen[o] {
			return fmt.Errorf("http.allowed_origins 含重复项: %s", o)
		}
		seen[o] = true
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("http.allowed_origins 项 %q 必须是 http(s)://host[:port] 形式", o)
		}
		if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("http.allowed_origins 项 %q 不应包含路径/查询/片段", o)
		}
	}
	return nil
}

// IsOriginAllowed 判断跨域来源是否在白名单。
func (c Config) IsOriginAllowed(origin string) bool {
	for _, o := range c.HTTP.AllowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}
