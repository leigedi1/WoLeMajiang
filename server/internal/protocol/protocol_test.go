package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	env := Envelope{
		V:    Version,
		Seq:  42,
		Type: EvRoomState,
		Payload: json.RawMessage(`{"roomId":"abc","players":[]}`),
	}
	data, err := Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(data, 65536)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.V != Version || got.Seq != 42 || got.Type != EvRoomState {
		t.Fatalf("roundtrip 不一致: %+v", got)
	}
	if string(got.Payload) != string(env.Payload) {
		t.Fatalf("payload 不一致: %s vs %s", got.Payload, env.Payload)
	}
}

func TestDecodeRejectsOversize(t *testing.T) {
	big := []byte(`{"v":1,"type":"ping","payload":"` + strings.Repeat("x", 2048) + `"}`)
	if _, err := Decode(big, 1024); err == nil {
		t.Fatal("超限消息应被拒绝")
	}
	// 上限等于消息长度时应通过。
	small := []byte(`{"v":1,"type":"ping"}`)
	if _, err := Decode(small, len(small)); err != nil {
		t.Fatalf("恰好等于上限应通过: %v", err)
	}
}

func TestDecodeRejectsBadVersion(t *testing.T) {
	if _, err := Decode([]byte(`{"v":2,"type":"ping"}`), 1024); err == nil {
		t.Fatal("错误版本应被拒绝")
	}
}

func TestDecodeRejectsMissingType(t *testing.T) {
	if _, err := Decode([]byte(`{"v":1}`), 1024); err == nil {
		t.Fatal("缺失 type 应被拒绝")
	}
}

func TestDecodeRejectsInvalidJSON(t *testing.T) {
	if _, err := Decode([]byte(`not json`), 1024); err == nil {
		t.Fatal("非法 JSON 应被拒绝")
	}
}

func TestNewEvent(t *testing.T) {
	e, err := NewEvent(7, EvHello, HelloOut{GuestID: "g1", Nickname: "游客"})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if e.V != Version || e.Seq != 7 || e.Type != EvHello {
		t.Fatalf("字段错误: %+v", e)
	}
	var out HelloOut
	if err := json.Unmarshal(e.Payload, &out); err != nil {
		t.Fatalf("负载解析: %v", err)
	}
	if out.GuestID != "g1" || out.Nickname != "游客" {
		t.Fatalf("负载内容错误: %+v", out)
	}
}

func TestRoomStateViewHidesOtherHands(t *testing.T) {
	// 其他玩家投影不含任何手牌字段; 仅 You 携带 hand。
	view := RoomStateView{
		RoomID: "r1", Code: "ABC234", Mode: "online", Status: "lobby", Theme: "classic", Seq: 3,
		Players: []PlayerView{{GuestID: "g1", Nickname: "甲", Seat: 0, Connected: true, TileCount: 13}},
		You:     &YouView{GuestID: "g2", Seat: 1, Hand: []string{"wan1", "wan2"}},
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"hand":["wan1","wan2"]`) {
		t.Fatalf("自己的手牌应可见: %s", s)
	}
	// PlayerView 类型没有 hand 字段, 序列化其他玩家必然不含手牌。
	var pv PlayerView
	if _, err := json.Marshal(pv); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(t, view.Players[0])), "hand") {
		t.Fatal("其他玩家投影不得包含 hand 字段")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
