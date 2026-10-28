package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	def := Default()
	if err := def.Validate(); err != nil {
		t.Fatalf("默认配置应合法: %v", err)
	}
	if def.Rooms.MaxOnline != 3 {
		t.Errorf("默认在线房间上限应为 3,实际 %d", def.Rooms.MaxOnline)
	}
	if def.Limits.MaxMessageBytes != 64*1024 {
		t.Errorf("默认消息大小上限应为 65536,实际 %d", def.Limits.MaxMessageBytes)
	}
	if len(def.HTTP.AllowedOrigins) != 2 {
		t.Errorf("默认跨域白名单应有 2 项, 实际 %v", def.HTTP.AllowedOrigins)
	}
}

func TestLoadFileOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	body := `{"rooms":{"max_online":5},"log":{"level":"debug"},"limits":{"message_burst":8},"http":{"allowed_origins":["http://localhost:3000"]}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Rooms.MaxOnline != 5 {
		t.Errorf("文件覆盖 max_online 应为 5,实际 %d", cfg.Rooms.MaxOnline)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("文件覆盖 log.level 应为 debug,实际 %s", cfg.Log.Level)
	}
	if cfg.Limits.MessageBurst != 8 {
		t.Errorf("文件覆盖 burst 应为 8,实际 %d", cfg.Limits.MessageBurst)
	}
	if !cfg.IsOriginAllowed("http://localhost:3000") {
		t.Errorf("白名单应包含文件覆盖项: %v", cfg.HTTP.AllowedOrigins)
	}
	if cfg.IsOriginAllowed("http://localhost:5173") {
		t.Error("文件整体替换 allowed_origins 后默认项不应保留")
	}
	if cfg.HTTP.Listen != Default().HTTP.Listen {
		t.Errorf("未覆盖字段应保持默认值,实际 %s", cfg.HTTP.Listen)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("MAJIANG_MAX_ONLINE_ROOMS", "7")
	t.Setenv("MAJIANG_MAX_MESSAGE_BYTES", "2048")
	t.Setenv("MAJIANG_ALLOWED_ORIGINS", "http://a.example, https://b.example")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Rooms.MaxOnline != 7 {
		t.Errorf("环境变量覆盖 max_online 应为 7,实际 %d", cfg.Rooms.MaxOnline)
	}
	if cfg.Limits.MaxMessageBytes != 2048 {
		t.Errorf("环境变量覆盖 max_message_bytes 应为 2048,实际 %d", cfg.Limits.MaxMessageBytes)
	}
	if !cfg.IsOriginAllowed("http://a.example") || !cfg.IsOriginAllowed("https://b.example") {
		t.Errorf("环境变量白名单应生效: %v", cfg.HTTP.AllowedOrigins)
	}
}

func TestLoadInvalid(t *testing.T) {
	t.Setenv("MAJIANG_MAX_ONLINE_ROOMS", "0")
	if _, err := Load(""); err == nil {
		t.Fatal("max_online=0 应返回校验错误")
	}
	t.Setenv("MAJIANG_MAX_ONLINE_ROOMS", "3")
	t.Setenv("MAJIANG_ALLOWED_ORIGINS", "ftp://bad")
	if _, err := Load(""); err == nil {
		t.Fatal("非法 origin 协议应返回校验错误")
	}
	t.Setenv("MAJIANG_ALLOWED_ORIGINS", "http://x/path")
	if _, err := Load(""); err == nil {
		t.Fatal("origin 含路径应返回校验错误")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("no-such-file.json"); err == nil {
		t.Fatal("缺失配置文件应返回错误")
	}
}

func TestLoadMalformedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}
