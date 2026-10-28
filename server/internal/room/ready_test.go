package room

import "testing"

// readyAll 让房间内所有人类玩家进入"已准备"状态(机器人加入即为已准备),
// 供需要成功开局的测试复用。
func readyAll(t *testing.T, r *Room) {
	t.Helper()
	r.mu.Lock()
	ids := make([]string, 0, len(r.players))
	for id, p := range r.players {
		if !p.IsBot {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		if err := r.SetReady(id, true); err != nil {
			t.Fatalf("设置准备失败(%s): %v", id, err)
		}
	}
}
