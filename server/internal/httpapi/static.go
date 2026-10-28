package httpapi

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// WithStatic 给后端挂上前端静态资源(单进程即可对外提供页面 + REST + WS)。
//
// 约定:
//   - API 与 WS 路径一律交给 next, 不做静态处理;
//   - 请求的文件存在则直接返回(带 Last-Modified, 便于浏览器缓存);
//   - 文件不存在时回退到 index.html(SPA 刷新任意路径都能进应用);
//   - 路径做 Clean + 前缀校验, 阻止 ../ 穿越到 root 之外。
//
// root 为空时原样返回 next, 便于测试与纯 API 部署。
func WithStatic(root string, next http.Handler) http.Handler {
	if strings.TrimSpace(root) == "" {
		return next
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return next
	}
	indexFile := filepath.Join(absRoot, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applySecurityHeaders(w.Header())
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if isBackendPath(p) {
			next.ServeHTTP(w, r)
			return
		}

		// 规范化并锁定在 root 之内。
		clean := path.Clean("/" + p)
		full := filepath.Join(absRoot, filepath.FromSlash(clean))
		if !strings.HasPrefix(full, absRoot+string(os.PathSeparator)) && full != absRoot {
			next.ServeHTTP(w, r)
			return
		}
		if st, statErr := os.Stat(full); statErr == nil && !st.IsDir() {
			// 带内容哈希的构建产物可以长缓存; 其它文件交给浏览器协商。
			if strings.HasPrefix(clean, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFile(w, r, full)
			return
		}
		// SPA 回退: 目录请求或未知路径都交给 index.html 由前端路由决定。
		// 直接写字节而不走 ServeFile: ServeFile 会对 /index.html 做 301 规范化, 破坏前端直达路径。
		data, readErr := os.ReadFile(indexFile)
		if readErr != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	})
}

// isBackendPath 判定该请求必须交给后端(API / WebSocket / 健康检查)。
func isBackendPath(p string) bool {
	switch {
	case strings.HasPrefix(p, "/api/"):
		return true
	case p == "/ws", strings.HasPrefix(p, "/ws/"):
		return true
	case p == "/healthz", p == "/readyz":
		return true
	default:
		return false
	}
}
