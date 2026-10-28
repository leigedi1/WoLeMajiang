package sig

import "testing"

// 线上事故回归: 反向代理把服务挂到公网域名/子路径后, 浏览器 Origin 是公网域名
// (当时 https://tsapi.wole110.xyz, 现 https://mj.wole110.xyz), 旧实现只认
// localhost/局域网前缀 → 403 → 浏览器 1006 → UI「信令连接失败」, P2P/局域网线上全不可用。
func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		host    string
		extra   []string
		allowed bool
	}{
		{"无 Origin(脚本客户端)", "", "tsapi.wole110.xyz", nil, true},
		{"公网同源必须放行", "https://tsapi.wole110.xyz", "tsapi.wole110.xyz", nil, true},
		{"同源的 http 与 https 都算同源", "http://tsapi.wole110.xyz", "tsapi.wole110.xyz", nil, true},
		{"带端口同源", "http://127.0.0.1:8080", "127.0.0.1:8080", nil, true},
		{"局域网前缀", "http://192.168.1.5:8080", "127.0.0.1:8080", nil, true},
		{"10 网段", "http://10.0.0.7:5173", "localhost:8080", nil, true},
		{"配置白名单兜底", "https://mj.example.com", "proxy-internal:8080", []string{"https://mj.example.com"}, true},
		{"白名单大小写不敏感", "https://MJ.example.com", "proxy-internal:8080", []string{"https://mj.example.com"}, true},
		{"陌生域名拒绝", "https://evil.example", "tsapi.wole110.xyz", nil, false},
		{"陌生域名即使带端口也拒绝", "http://evil.example:8080", "tsapi.wole110.xyz", nil, false},
		{"白名单不含则不兜底", "https://other.example", "proxy-internal:8080", []string{"https://mj.example.com"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := originAllowed(c.origin, c.host, c.extra); got != c.allowed {
				t.Fatalf("originAllowed(%q, %q, %v) = %v, 期望 %v", c.origin, c.host, c.extra, got, c.allowed)
			}
		})
	}
}
