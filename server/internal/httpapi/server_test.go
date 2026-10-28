package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"majiang-server/internal/config"
	"majiang-server/internal/logging"
	"majiang-server/internal/room"
	"majiang-server/internal/store"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Default()
	// DIY 图片目录放到临时目录, 避免测试把图片写进仓库。
	cfg.DIY.Dir = t.TempDir() + "/diy"
	log := logging.New("error")
	rooms := room.NewRegistry(cfg.Rooms.MaxOnline, cfg.Rooms.IdleTTL(), nil)
	return NewRouter(cfg, log, st, rooms)
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz 应 200, 实际 %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("healthz 状态错误: %v", body)
	}
}

func TestReadyz(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz 应 200, 实际 %d", resp.StatusCode)
	}
}

func TestAPIHealth(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health 应 200, 实际 %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"status", "service", "go", "uptimeSeconds", "onlineRooms", "maxOnlineRooms", "guests", "protocolVersion"} {
		if _, ok := body[key]; !ok {
			t.Errorf("health 缺少字段 %s: %v", key, body)
		}
	}
	if body["maxOnlineRooms"] != float64(3) {
		t.Errorf("maxOnlineRooms 应为 3: %v", body["maxOnlineRooms"])
	}
}

func TestCreateGuestFlow(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()

	post := func(body string) *http.Response {
		resp, err := http.Post(srv.URL+"/api/v1/guests", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// 合法昵称(含首尾空白)。
	resp := post(`{"nickname":"  阿豪  "}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建游客应 201, 实际 %d", resp.StatusCode)
	}
	var created map[string]string
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created["guestId"] == "" || len(created["token"]) != 64 || created["nickname"] != "阿豪" {
		t.Fatalf("创建响应字段错误: %v", created)
	}

	// 重连验证。
	recResp, err := http.Post(srv.URL+"/api/v1/reconnect", "application/json",
		strings.NewReader(`{"guestId":"`+created["guestId"]+`","token":"`+created["token"]+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if recResp.StatusCode != http.StatusOK {
		t.Fatalf("重连应 200, 实际 %d", recResp.StatusCode)
	}
	recResp.Body.Close()

	// 错误令牌。
	badResp, err := http.Post(srv.URL+"/api/v1/reconnect", "application/json",
		strings.NewReader(`{"guestId":"`+created["guestId"]+`","token":"bad"}`))
	if err != nil {
		t.Fatal(err)
	}
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误令牌应 401, 实际 %d", badResp.StatusCode)
	}
	badResp.Body.Close()

	// 非法昵称。
	if resp := post(`{"nickname":"   "}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空昵称应 400, 实际 %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// 坏 JSON。
	if resp := post(`{bad`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应 400, 实际 %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// 未知字段。
	if resp := post(`{"nickname":"甲","extra":1}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知字段应 400, 实际 %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func TestCORS(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()

	// 白名单内 origin。
	req, _ := http.NewRequest("OPTIONS", srv.URL+"/api/v1/guests", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("CORS 头错误: %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}

	// 白名单外 origin。
	req2, _ := http.NewRequest("GET", srv.URL+"/healthz", nil)
	req2.Header.Set("Origin", "http://evil.example")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("非白名单 origin 不应返回 CORS 头: %q", resp2.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/healthz", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET 端点上的 POST 应 405, 实际 %d", resp.StatusCode)
	}
}

func TestGuestsPersisted(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/v1/guests", "application/json", strings.NewReader(`{"nickname":"甲"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]string
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	resp2, _ := http.Get(srv.URL + "/api/v1/health")
	var body map[string]any
	json.NewDecoder(resp2.Body).Decode(&body)
	resp2.Body.Close()
	if body["guests"] != float64(1) {
		t.Fatalf("guests 应为 1: %v", body["guests"])
	}
}
