package sig

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// upgrader 与 httpapi/ws.go 同库, 与在线托管共享同一 gorilla/websocket;
// 信令层只中继, 不做业务状态管理.
// CheckOrigin 不在这里固定: 白名单来自配置, 每次 Upgrade 前按请求注入(见 upgrade).
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// lanOriginPrefixes 局域网/本机来源前缀.
// 这些地址不可能预先写进配置(每台机器的局域网 IP 都不同), 但局域网模式恰恰需要它们,
// 因此按前缀放行。
var lanOriginPrefixes = []string{
	"http://localhost", "https://localhost",
	"http://127.0.0.1", "https://127.0.0.1",
	"http://0.0.0.0", "https://0.0.0.0",
	"http://192.168.", "https://192.168.",
	"http://10.", "https://10.",
	"http://172.16.", "https://172.16.",
	"http://172.17.", "https://172.17.",
	"http://172.18.", "https://172.18.",
	"http://172.19.", "https://172.19.",
	"http://172.2", "https://172.2",
	"http://172.3", "https://172.3",
}

// originAllowed 判定信令握手的 Origin 是否可信。
//
// 关键: **同源必须放行**。反向代理把服务挂到公网域名/子路径后(线上
// https://mj.wole110.xyz/, 早年是 https://tsapi.wole110.xyz/mj/), 浏览器发来的
// Origin 是 https://mj.wole110.xyz, 它既不在局域网前缀里, 也未必写进配置白名单;
// 只靠硬编码前缀会让线上 P2P/局域网信令全部 403 —— 浏览器侧只看到 1006,
// UI 报「信令连接失败」(曾真实踩过)。
func originAllowed(origin, requestHost string, extra []string) bool {
	if origin == "" {
		// 非浏览器客户端(脚本/原生)不带 Origin, 与在线 WS 的处理保持一致。
		return true
	}
	if u, err := url.Parse(origin); err == nil && u.Host != "" && strings.EqualFold(u.Host, requestHost) {
		return true
	}
	for _, a := range extra {
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	lower := strings.ToLower(origin)
	for _, p := range lanOriginPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// upgrade 按本次请求的 Host 与配置白名单执行握手。
func (h *Handler) upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	up := upgrader
	up.CheckOrigin = func(req *http.Request) bool {
		return originAllowed(req.Header.Get("Origin"), req.Host, h.cfg.AllowedOrigins)
	}
	return up.Upgrade(w, r, nil)
}

// Handler 信令 WebSocket handler.
type Handler struct {
	Registry *Registry
	cfg      Config
}

// NewHandler 构造.
func NewHandler(reg *Registry) *Handler {
	return &Handler{Registry: reg, cfg: reg.cfg}
}

// ServeHTTP Upgrade → 读消息 → dispatch → 循环.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrade(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	peerID, _ := readQuery(r, "peer")
	if peerID == "" {
		peerID = "peer-" + newPeerSuffix()
	}
	if len(peerID) > 64 {
		peerID = peerID[:64]
	}

	peer := &Peer{
		ID:       peerID,
		JoinedAt: time.Now(),
		LastSeen: time.Now(),
		Send: func(payload []byte) error {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			return conn.WriteMessage(websocket.TextMessage, payload)
		},
	}

	// 先进入 registry 的 peers 表 (未在任何房间).
	h.Registry.mu.Lock()
	h.Registry.peers[peerID] = peer
	h.Registry.mu.Unlock()
	defer h.Registry.RemovePeer(peerID)

	// 读写协程: 写一个独立的 goroutine 避免并发写.
	sendCh := make(chan []byte, 16)
	sendClosed := atomic.Bool{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for payload := range sendCh {
			_ = conn.WriteMessage(websocket.TextMessage, payload)
		}
	}()
	peer.Send = func(payload []byte) error {
		if sendClosed.Load() {
			return errors.New("peer closed")
		}
		select {
		case sendCh <- payload:
			return nil
		default:
			return errors.New("send queue full")
		}
	}

	// 写一个 peer 上线通知 (含 server time).
	writePeerInfo(peer, Envelope{
		Type: OpJoinOK, Peer: peerID,
		Data: rawJSON(map[string]any{"serverTime": time.Now().UnixMilli(), "peer": peerID, "msg": "signaling connected"}),
	})

	// 速率窗口 (每 peer): 1 分钟内 maxMsgs 次; 超限踢掉.
	var (
		window     = make([]time.Time, 0, 32)
		lastClean  = time.Now()
	)

	// 读循环
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if len(raw) == 0 || len(raw) > h.cfg.MaxMessageBytes {
			writeErr(peer, nil, ErrInvalidMessage)
			continue
		}
		// 限流窗口
		now := time.Now()
		if now.Sub(lastClean) > 10*time.Second {
			// 清理过期窗口
			cutoff := now.Add(-time.Minute)
			i := 0
			for i < len(window) && window[i].Before(cutoff) {
				i++
			}
			window = window[i:]
			lastClean = now
		}
		if len(window) >= h.cfg.MsgsPerMinute {
			writeErr(peer, nil, ErrRateLimited)
			break
		}
		window = append(window, now)

		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			writeErr(peer, nil, ErrInvalidMessage)
			continue
		}
		if env.Peer == "" {
			env.Peer = peerID
		}
		if err := h.dispatch(peer, &env); err != nil {
			writeErr(peer, &env, err)
		}
	}

	close(sendCh)
	sendClosed.Store(true)
	wg.Wait()
}

func (h *Handler) dispatch(peer *Peer, env *Envelope) error {
	reg := h.Registry
	reg.OnPeerSeen(peer.ID)

	switch env.Type {
	case OpCreate:
		var data struct {
			Name     string `json:"name"`
			Password string `json:"password,omitempty"`
		}
		_ = json.Unmarshal(env.Data, &data)
		rm, err := reg.CreateRoom(data.Name, data.Password, peer.ID)
		if err != nil {
			return err
		}
		// 加入房
		_, _, _ = reg.JoinRoom(rm.Code, data.Password, peer.ID)
		writePeerInfo(peer, Envelope{
			Type: OpCreateOK, Peer: peer.ID, Room: rm.Code,
			Data: rawJSON(map[string]any{
				"code":      rm.Code,
				"name":      rm.Name,
				"host":      rm.HostID,
				"isHost":    true,
				"peers":     peer.ID,
				"password":  rm.PasswordOK,
				"hostToken": rm.HostToken,
			}),
		})

	case OpRehost:
		// 房主的信令连接断开后重连: 凭 hostToken 用原房间码回来,
		// 这样加入端解析到的还是同一个码, 不用重新发码。
		var data struct {
			Code      string `json:"code"`
			HostToken string `json:"hostToken"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return ErrInvalidMessage
		}
		rm, _, err := reg.Rehost(data.Code, data.HostToken, peer.ID)
		if err != nil {
			return err
		}
		writePeerInfo(peer, Envelope{
			Type: OpRehostOK, Peer: peer.ID, Room: rm.Code,
			Data: rawJSON(map[string]any{
				"code":      rm.Code,
				"name":      rm.Name,
				"host":      rm.HostID,
				"isHost":    true,
				"password":  rm.PasswordOK,
				"hostToken": rm.HostToken,
				"peers":     peersSnapshot(rm, peer.ID),
			}),
		})
		payload, _ := json.Marshal(Envelope{
			Type: OpPeers, Room: rm.Code, Peer: peer.ID,
			Data: rawJSON(peersSnapshot(rm, "")),
		})
		_ = rm.Broadcast(payload, "")

	case OpPing:
		writePeerInfo(peer, Envelope{
			Type: OpPong, Peer: peer.ID, Room: peer.Room,
			Data: rawJSON(map[string]any{"serverTime": time.Now().UnixMilli()}),
		})

	case OpJoin:
		var data struct {
			Code     string `json:"code"`
			Password string `json:"password,omitempty"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return ErrInvalidMessage
		}
		rm, _, err := reg.JoinRoom(data.Code, data.Password, peer.ID)
		if err != nil {
			return err
		}
		// 通知房主有新人加入, 并回给 join 者 peer 列表
		writePeerInfo(peer, Envelope{
			Type: OpJoinOK, Peer: peer.ID, Room: rm.Code,
			Data: rawJSON(peersSnapshot(rm, peer.ID)),
		})
		// 广播 room.peers 更新给所有 peer
		payload, _ := json.Marshal(Envelope{
			Type: OpPeers, Room: rm.Code, Peer: peer.ID,
			Data: rawJSON(peersSnapshot(rm, "")),
		})
		_ = rm.Broadcast(payload, "")

	case OpOffer, OpAnswer, OpICE:
		if env.To == "" {
			return errors.New("sig: missing 'to' field")
		}
		if rm := reg.LookupRoom(peer.Room); rm != nil {
			// 把完整 envelope 原样转发给 to (payload 不变)
			env.Peer = peer.ID
			b, _ := json.Marshal(env)
			_ = rm.SendTo(env.To, b)
			// 同时广播自己的 peer 身份给对方以便对方返回
		} else {
			return ErrRoomNotFound
		}

	case OpLeave:
		if rm := reg.LookupRoom(peer.Room); rm != nil {
			reg.RemovePeer(peer.ID)
			payload, _ := json.Marshal(Envelope{
				Type: OpPeers, Room: rm.Code,
				Data: rawJSON(peersSnapshot(rm, "")),
			})
			_ = rm.Broadcast(payload, "")
			// 如果房主主动离开, 解散房间
			if rm.HostID == peer.ID {
				// 广播解散
				disp, _ := json.Marshal(Envelope{Type: OpClose, Room: rm.Code, Data: rawJSON(map[string]string{"reason": "host-left"})})
				_ = rm.Broadcast(disp, "")
				reg.DeleteRoom(rm.Code)
			}
		}

	case OpList:
		// 返回活跃 P2P 房间码列表 + 简要信息 (不含密码)
		codes := reg.RoomCodes()
		type roomLite struct {
			Code    string `json:"code"`
			Name    string `json:"name"`
			Peers   int    `json:"peers"`
			Host    string `json:"host"`
			Public  bool   `json:"public"`
		}
		lites := make([]roomLite, 0, len(codes))
		for _, c := range codes {
			rm := reg.LookupRoom(c)
			if rm == nil {
				continue
			}
			// 空房间(房主暂时断线, 仍在重连宽限期内)不对外展示。
			if rm.PeerCount() == 0 {
				continue
			}
			lites = append(lites, roomLite{
				Code:   rm.Code,
				Name:   rm.Name,
				Peers:  rm.PeerCount(),
				Host:   rm.HostID,
				Public: !rm.PasswordOK,
			})
		}
		writePeerInfo(peer, Envelope{
			Type: OpList, Peer: peer.ID,
			Data: rawJSON(lites),
		})

	case OpPeers:
		if rm := reg.LookupRoom(peer.Room); rm != nil {
			writePeerInfo(peer, Envelope{
				Type: OpPeers, Peer: peer.ID, Room: rm.Code,
				Data: rawJSON(peersSnapshot(rm, peer.ID)),
			})
		}

	default:
		return errors.New("sig: unknown op: " + string(env.Type))
	}
	return nil
}

func peersSnapshot(rm *Room, except string) map[string]any {
	items := rm.Peers()
	out := make([]map[string]any, 0, len(items))
	for _, p := range items {
		if p.ID == except {
			continue
		}
		out = append(out, map[string]any{
			"id":     p.ID,
			"isHost": p.ID == rm.HostID,
			"room":   rm.Code,
		})
	}
	return map[string]any{
		"room":  rm.Code,
		"host":  rm.HostID,
		"peers": out,
	}
}

func writePeerInfo(peer *Peer, env Envelope) {
	b, _ := json.Marshal(env)
	_ = peer.Send(b)
}

func writeErr(peer *Peer, src *Envelope, err error) {
	env := Envelope{
		Type: OpError, Peer: peer.ID,
		Err: &ErrorPayload{Code: err.Error(), Message: err.Error()},
	}
	if src != nil {
		env.Room = src.Room
		env.Seq = src.Seq
	}
	b, _ := json.Marshal(env)
	_ = peer.Send(b)
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func readQuery(r *http.Request, key string) (string, bool) {
	v := r.URL.Query().Get(key)
	return v, v != ""
}
