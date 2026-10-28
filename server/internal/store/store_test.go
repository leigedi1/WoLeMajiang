package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("首次 Open: %v", err)
	}
	s1.Close()
	s2, err := Open(path) // 二次打开不应重复执行迁移
	if err != nil {
		t.Fatalf("二次 Open: %v", err)
	}
	defer s2.Close()
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("查询迁移数: %v", err)
	}
	// 迁移文件数随阶段增长, 用内嵌目录的实际文件数作为期望值(而非写死)。
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("读取迁移目录: %v", err)
	}
	want := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			want++
		}
	}
	if n != want {
		t.Fatalf("应恰好应用 %d 个迁移, 实际 %d", want, n)
	}
}

func TestOpenCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "nested", "deep", "m.db"))
	if err != nil {
		t.Fatalf("应自动创建父目录: %v", err)
	}
	s.Close()
}

func TestOpenEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := Open("   "); err == nil {
		t.Fatal("空白路径应报错")
	}
}

func TestGuestCreateAndVerify(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	g, token, err := s.CreateGuest(ctx, "  阿豪  ")
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	if g.Nickname != "阿豪" {
		t.Errorf("昵称应去除首尾空白, 实际 %q", g.Nickname)
	}
	if len(g.ID) != 32 || len(token) != 64 {
		t.Errorf("ID 应 32 字符、令牌应 64 字符, 实际 %d/%d", len(g.ID), len(token))
	}

	got, ok, err := s.VerifyGuest(ctx, g.ID, token)
	if err != nil || !ok {
		t.Fatalf("正确令牌应验证通过: %v %v", ok, err)
	}
	if got.Nickname != "阿豪" {
		t.Errorf("验证返回昵称错误: %q", got.Nickname)
	}
	if _, ok, _ := s.VerifyGuest(ctx, g.ID, "wrong-token"); ok {
		t.Fatal("错误令牌不应通过")
	}
	if _, ok, _ := s.VerifyGuest(ctx, "no-such-id", token); ok {
		t.Fatal("不存在的游客不应通过")
	}
	if _, ok, _ := s.VerifyGuest(ctx, "", token); ok {
		t.Fatal("空 ID 不应通过")
	}
}

func TestGuestTokenNotStoredInPlaintext(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, token, err := s.CreateGuest(ctx, "小明")
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := s.db.QueryRow(`SELECT token_hash FROM guests LIMIT 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == token {
		t.Fatal("令牌本体不得明文落库")
	}
	if len(hash) != 64 {
		t.Errorf("哈希应为 64 字符 hex, 实际 %d", len(hash))
	}
}

func TestNicknameValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	bad := []string{"", "   ", string(make([]rune, 25)), "ab\ncd", "a\x00b"}
	for _, nick := range bad {
		if _, _, err := s.CreateGuest(ctx, nick); err == nil {
			t.Errorf("昵称 %q 应被拒绝", nick)
		}
	}
	// 恰好 24 字符合法。
	long := make([]rune, 24)
	for i := range long {
		long[i] = '客'
	}
	if _, _, err := s.CreateGuest(ctx, string(long)); err != nil {
		t.Errorf("24 字符昵称应合法: %v", err)
	}
	// 表情符号合法。
	if _, _, err := s.CreateGuest(ctx, "🀄雀神"); err != nil {
		t.Errorf("含 emoji 昵称应合法: %v", err)
	}
}

func TestTouchAndCountGuests(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	g1, _, _ := s.CreateGuest(ctx, "一号")
	g2, _, _ := s.CreateGuest(ctx, "二号")
	if n, _ := s.CountGuests(ctx); n != 2 {
		t.Fatalf("游客数应为 2, 实际 %d", n)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := s.TouchGuest(ctx, g1.ID); err != nil {
		t.Fatal(err)
	}
	var seen1, seen2 int64
	row := s.db.QueryRow(`SELECT last_seen_at FROM guests WHERE id=?`, g1.ID)
	row.Scan(&seen1)
	row = s.db.QueryRow(`SELECT last_seen_at FROM guests WHERE id=?`, g2.ID)
	row.Scan(&seen2)
	if seen1 <= seen2 {
		t.Fatalf("Touch 后活跃时间应更新: %d vs %d", seen1, seen2)
	}
}

func TestRoomRowCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 外键约束: 房主必须是已存在的游客。
	host, _, err := s.CreateGuest(ctx, "房主小明")
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	now := time.Now().Unix()
	row := RoomRow{
		ID: "room0001", Code: "ABC234", HostGuestID: host.ID, Mode: "online",
		RuleSet: `{"players":4}`, Theme: "classic", Status: "lobby",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateRoom(ctx, row); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	// 邀请码唯一约束。
	if err := s.CreateRoom(ctx, row); err == nil {
		t.Fatal("重复邀请码应失败")
	}
	// 外键约束: 不存在的房主应被拒绝。
	bad := row
	bad.ID = "room0002"
	bad.Code = "ZZZ999"
	bad.HostGuestID = "no-such-guest"
	if err := s.CreateRoom(ctx, bad); err == nil {
		t.Fatal("不存在的房主应触发外键约束失败")
	}
	if err := s.UpdateRoomActivity(ctx, row.ID, "playing"); err != nil {
		t.Fatalf("UpdateRoomActivity: %v", err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM rooms WHERE id=?`, row.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "playing" {
		t.Fatalf("状态应为 playing, 实际 %s", status)
	}
	if err := s.DeleteRoom(ctx, row.ID); err != nil {
		t.Fatalf("DeleteRoom: %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&n)
	if n != 0 {
		t.Fatalf("删除后房间数应为 0, 实际 %d", n)
	}
}
