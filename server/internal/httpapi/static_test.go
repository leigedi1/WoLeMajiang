package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 静态托管: 命中文件直出, 未知路径回退 index.html, API/WS 交给后端, 目录穿越被拦。
func TestWithStatic(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>app</html>"), 0o600); err != nil {
		t.Fatalf("写入 index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatalf("写入 app.js: %v", err)
	}

	backendHit := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHit++
		w.WriteHeader(http.StatusTeapot)
	})
	h := WithStatic(root, backend)

	cases := []struct {
		name   string
		path   string
		status int
		body   string
	}{
		{"命中静态文件", "/app.js", http.StatusOK, "console.log(1)"},
		{"根路径回退 index", "/", http.StatusOK, "<html>app</html>"},
		{"未知路径回退 index", "/room/ABCDEF", http.StatusOK, "<html>app</html>"},
		{"API 交给后端", "/api/v1/health", http.StatusTeapot, ""},
		{"WS 交给后端", "/ws/signal", http.StatusTeapot, ""},
		// 穿越路径不会被解析到 root 之外: 不存在的路径一律回退 index.html(不泄露文件)。
		{"穿越被拦", "/../etc/passwd", http.StatusOK, "<html>app</html>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			if rec.Code != c.status {
				t.Fatalf("状态码 = %d, 期望 %d", rec.Code, c.status)
			}
			if c.body != "" && rec.Body.String() != c.body {
				t.Fatalf("响应体 = %q, 期望 %q", rec.Body.String(), c.body)
			}
		})
	}

	if backendHit < 2 {
		t.Fatalf("API/WS 请求未交给后端, hits=%d", backendHit)
	}

	// 安全响应头: 静态直出与 SPA 回退都必须带上(阶段9)。
	for _, p := range []string{"/app.js", "/", "/room/ABCDEF"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s: X-Content-Type-Options = %q, 期望 nosniff", p, got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("%s: X-Frame-Options = %q, 期望 DENY", p, got)
		}
	}
}

// 空 root 时原样透传(纯 API 部署不受影响)。
func TestWithStaticEmptyRoot(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend"))
	})
	rec := httptest.NewRecorder()
	WithStatic("", backend).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if rec.Body.String() != "backend" {
		t.Fatalf("期望透传后端, 实际 %q", rec.Body.String())
	}
}
