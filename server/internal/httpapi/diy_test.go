package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// tinyPNG 是最小的合法 PNG 头 + 少量填充, 用于验证按文件头嗅探类型。
var tinyPNG = append(
	[]byte("\x89PNG\r\n\x1a\n"),
	bytes.Repeat([]byte{0x00}, 32)...,
)

func dataURLPNG() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyPNG)
}

// doJSON 发一个 JSON POST 并返回响应。
func doJSON(t *testing.T, srv *httptest.Server, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestDIYStateInitiallyEmpty(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/diy")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/diy 期望 200, 实际 %d", resp.StatusCode)
	}
	var st diyStateView
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.PasswordSet {
		t.Error("全新数据库不应已设置密码")
	}
	if len(st.Tiles) != 0 {
		t.Errorf("初始应无自定义牌面, 实际 %d", len(st.Tiles))
	}
	if len(st.Fits) == 0 {
		t.Error("应返回可选填充方式")
	}
}

func TestDIYPasswordFlow(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()

	// 未设密码时写操作一律拒绝。
	resp, _ := doJSON(t, srv, "/api/v1/diy/auth", map[string]string{"password": "whatever"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未设密码时校验应 401, 实际 %d", resp.StatusCode)
	}

	// 首次设置密码(无需旧密码)。
	resp, _ = doJSON(t, srv, "/api/v1/diy/password", map[string]string{
		"currentPassword": "", "newPassword": "abcd1234",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("首次设置密码应 200, 实际 %d", resp.StatusCode)
	}

	// 正确密码可进入。
	resp, _ = doJSON(t, srv, "/api/v1/diy/auth", map[string]string{"password": "abcd1234"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("正确密码应 200, 实际 %d", resp.StatusCode)
	}
	// 错误密码拒绝。
	resp, _ = doJSON(t, srv, "/api/v1/diy/auth", map[string]string{"password": "wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401, 实际 %d", resp.StatusCode)
	}

	// 改密码必须带对旧密码。
	resp, _ = doJSON(t, srv, "/api/v1/diy/password", map[string]string{
		"currentPassword": "wrong", "newPassword": "next1234",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("旧密码错误应 401, 实际 %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, srv, "/api/v1/diy/password", map[string]string{
		"currentPassword": "abcd1234", "newPassword": "next1234",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("改密码应 200, 实际 %d", resp.StatusCode)
	}
	// 旧密码失效, 新密码可用。
	resp, _ = doJSON(t, srv, "/api/v1/diy/auth", map[string]string{"password": "abcd1234"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("旧密码应失效, 实际 %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, srv, "/api/v1/diy/auth", map[string]string{"password": "next1234"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("新密码应可用, 实际 %d", resp.StatusCode)
	}

	// 过短的新密码拒绝。
	resp, _ = doJSON(t, srv, "/api/v1/diy/password", map[string]string{
		"currentPassword": "next1234", "newPassword": "abc",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("过短密码应 400, 实际 %d", resp.StatusCode)
	}
}

func TestDIYSaveAndDeleteTile(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()

	_, _ = doJSON(t, srv, "/api/v1/diy/password", map[string]string{
		"currentPassword": "", "newPassword": "abcd1234",
	})

	// 密码错误不能保存。
	resp, _ := doJSON(t, srv, "/api/v1/diy/tiles", map[string]string{
		"password": "wrong", "code": "wan1", "fit": "cover", "dataUrl": dataURLPNG(),
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误密码保存应 401, 实际 %d", resp.StatusCode)
	}

	// 未知牌面拒绝。
	resp, _ = doJSON(t, srv, "/api/v1/diy/tiles", map[string]string{
		"password": "abcd1234", "code": "nope9", "fit": "cover", "dataUrl": dataURLPNG(),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知牌面应 400, 实际 %d", resp.StatusCode)
	}

	// 非图片 data URL 拒绝。
	resp, _ = doJSON(t, srv, "/api/v1/diy/tiles", map[string]string{
		"password": "abcd1234", "code": "wan1", "fit": "cover",
		"dataUrl": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("hi")),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非图片应 400, 实际 %d", resp.StatusCode)
	}

	// 正常保存。
	resp, out := doJSON(t, srv, "/api/v1/diy/tiles", map[string]string{
		"password": "abcd1234", "code": "wan1", "fit": "contain", "dataUrl": dataURLPNG(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存应 200, 实际 %d (%v)", resp.StatusCode, out)
	}
	url, _ := out["url"].(string)
	if url == "" {
		t.Fatal("应返回图片 URL")
	}
	if fit, _ := out["fit"].(string); fit != "contain" {
		t.Errorf("填充方式应为 contain, 实际 %q", fit)
	}

	// 公开状态里能看到这张牌。
	getResp, err := http.Get(srv.URL + "/api/v1/diy")
	if err != nil {
		t.Fatal(err)
	}
	var st diyStateView
	_ = json.NewDecoder(getResp.Body).Decode(&st)
	getResp.Body.Close()
	if len(st.Tiles) != 1 || st.Tiles[0].Code != "wan1" {
		t.Fatalf("状态里应有 wan1, 实际 %+v", st.Tiles)
	}

	// 图片可以匿名取到。
	imgResp, err := http.Get(srv.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	if imgResp.StatusCode != http.StatusOK {
		t.Fatalf("取图片应 200, 实际 %d", imgResp.StatusCode)
	}
	if ct := imgResp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type 应为 image/png, 实际 %q", ct)
	}
	imgResp.Body.Close()

	// 非法文件名不能穿越目录。
	bad, err := http.Get(srv.URL + "/api/v1/diy/images/..%2f..%2fetc%2fpasswd")
	if err != nil {
		t.Fatal(err)
	}
	if bad.StatusCode == http.StatusOK {
		t.Error("非法文件名不应返回 200")
	}
	bad.Body.Close()

	// 删除后回到空状态。
	resp, _ = doJSON(t, srv, "/api/v1/diy/tiles/wan1/delete", map[string]string{"password": "abcd1234"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除应 200, 实际 %d", resp.StatusCode)
	}
	getResp2, _ := http.Get(srv.URL + "/api/v1/diy")
	var st2 diyStateView
	_ = json.NewDecoder(getResp2.Body).Decode(&st2)
	getResp2.Body.Close()
	if len(st2.Tiles) != 0 {
		t.Fatalf("删除后应为空, 实际 %+v", st2.Tiles)
	}
}

func TestDIYFitFallsBackToCover(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()
	_, _ = doJSON(t, srv, "/api/v1/diy/password", map[string]string{
		"currentPassword": "", "newPassword": "abcd1234",
	})
	resp, out := doJSON(t, srv, "/api/v1/diy/tiles", map[string]string{
		"password": "abcd1234", "code": "tong3", "fit": "bogus", "dataUrl": dataURLPNG(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存应 200, 实际 %d", resp.StatusCode)
	}
	if fit, _ := out["fit"].(string); fit != "cover" {
		t.Errorf("非法填充方式应回退 cover, 实际 %q", fit)
	}
}
