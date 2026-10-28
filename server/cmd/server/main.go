// majiang-server 服务器入口: 加载配置 -> 打开数据库 -> 构建房间注册表 -> 启动 HTTP/WS。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"majiang-server/internal/config"
	"majiang-server/internal/httpapi"
	"majiang-server/internal/logging"
	"majiang-server/internal/room"
	"majiang-server/internal/store"
)

func main() {
	cfgPath := flag.String("config", "", "配置文件路径(JSON, 可选)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	logger := logging.New(cfg.Log.Level)
	logger.Info("配置加载完成",
		"listen", cfg.HTTP.Listen,
		"db", cfg.Database.Path,
		"max_online", cfg.Rooms.MaxOnline,
		"limits", cfg.Limits.MaxMessageBytes)

	st, err := store.Open(cfg.Database.Path)
	if err != nil {
		logger.Error("数据库打开失败", "error", err.Error())
		os.Exit(1)
	}
	defer st.Close()

	persister := httpapi.NewStorePersister(st)
	registry := room.NewRegistry(cfg.Rooms.MaxOnline, cfg.Rooms.IdleTTL(), persister)

	// 启动清理: 停机期间超过空房 TTL 的房间元数据一并过期(未过期房间由阶段7的恢复逻辑接管)。
	cutoff := time.Now().Unix() - int64(cfg.Rooms.IdleTTLSeconds)
	if n, err := st.DeleteStaleRooms(context.Background(), cutoff); err != nil {
		logger.Warn("过期房间元数据清理失败", "error", err.Error())
	} else if n > 0 {
		logger.Info("已清理过期房间元数据", "count", n)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go registry.SweepLoop(rootCtx, cfg.Rooms.SweepInterval())

	router, handler := httpapi.NewServer(cfg, logger, st, registry)
	// 可选: 由后端直接托管前端静态资源(单进程部署, 不依赖反代服务静态文件)。
	if webRoot := os.Getenv("MAJIANG_WEB_ROOT"); webRoot != "" {
		handler = httpapi.WithStatic(webRoot, handler)
		logger.Info("已托管前端静态资源", "root", webRoot)
	}
	// 后台：机器人座位自动对局、人类超时托管、公开大厅推送。
	go router.Run(rootCtx)
	srv := &http.Server{
		Addr:              cfg.HTTP.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()
	logger.Info("服务已启动", "addr", "http://"+cfg.HTTP.Listen)

	select {
	case <-rootCtx.Done():
		logger.Info("收到退出信号, 优雅关闭中...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("关闭超时", "error", err.Error())
		}
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("服务异常退出", "error", err.Error())
			os.Exit(1)
		}
	}
	logger.Info("服务已退出")
}
