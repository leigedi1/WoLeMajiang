// Package logging 基于 slog 的 JSON 日志器。
package logging

import (
	"log/slog"
	"os"
)

// New 按级别创建输出到 stdout 的 JSON 日志器。
func New(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}
