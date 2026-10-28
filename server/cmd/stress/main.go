// Command stress 是可复现的麻将服务器压测 / 滥用探测客户端（阶段 9）。
//
// 用法:
//
//	go run ./cmd/stress -mode normal -base http://127.0.0.1:8790 -rooms 3 -players 4 -timeout 240s
//	go run ./cmd/stress -mode abuse  -base http://127.0.0.1:8790
//
// normal 模式: 每个房间 1 名房主建房，其余玩家用房间码加入，满座后房主开局；
// 每个客户端按自己的私有视图自动出牌（draw/discard/pass/hu），每秒 ping 测 RTT，
// 结束后输出 JSON 汇总，便于 1 核 1GB 实测记录。
//
// abuse 模式: 逐项探测异常输入（坏 token、超长帧、非法 JSON、洪泛、无效房间码、
// 房间数超限、无效邀请令牌、首帧不打招呼），打印每项实际返回码。
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"majiang-server/internal/protocol"
)

// ---------- 协议帧 ----------

type envelope struct {
	V       int             `json:"v"`
	Seq     int64           `json:"seq,omitempty"`
	Ref     string          `json:"ref,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// seatView 是服务端下发的本人视角（隐藏他人手牌）。
type seatView struct {
	Seat             int      `json:"seat"`
	Phase            string   `json:"phase"`
	TurnSeat         int      `json:"turnSeat"`
	TurnStage        string   `json:"turnStage"`
	WallLeft         int      `json:"wallLeft"`
	Hand             []string `json:"hand"`
	Drawn            string   `json:"drawn"`
	PendingBy        int      `json:"pendingBy"`
	Actions          []string `json:"actions"`
	OthersHandCounts []int    `json:"othersHandCounts"`
	Result           *struct {
		Winner int   `json:"winner"`
		ByDraw bool  `json:"byDraw"`
		Fan    int   `json:"fan"`
		Pay    int   `json:"pay"`
		Scores []int `json:"scores"`
	} `json:"result"`
	Scores []int `json:"scores"`
}

type roomState struct {
	RoomID  string        `json:"roomId"`
	Code    string        `json:"code"`
	Status  string        `json:"status"`
	Players []roomSeatSum `json:"players"`
}

type roomSeatSum struct {
	Seat     int    `json:"seat"`
	Nickname string `json:"nickname"`
	IsBot    bool   `json:"isBot"`
	Ready    bool   `json:"ready"`
}

// ---------- 统计 ----------

type stats struct {
	mu       sync.Mutex
	in, out  int
	acts     int
	finished int
	errs     map[string]int
	rtt      []time.Duration
	actLag   []time.Duration // 准点视图到达 → 发出动作
	sendGap  []time.Duration // 发出动作 → 收到下一条对局事件
}

func newStats() *stats { return &stats{errs: map[string]int{}} }

func (s *stats) addIn()  { s.mu.Lock(); s.in++; s.mu.Unlock() }
func (s *stats) addOut() { s.mu.Lock(); s.out++; s.mu.Unlock() }
func (s *stats) addErr(code string) {
	s.mu.Lock()
	s.errs[code]++
	s.mu.Unlock()
}
func (s *stats) addAct() { s.mu.Lock(); s.acts++; s.mu.Unlock() }
func (s *stats) addRTT(d time.Duration) {
	s.mu.Lock()
	s.rtt = append(s.rtt, d)
	s.mu.Unlock()
}
func (s *stats) addLag(d time.Duration) {
	s.mu.Lock()
	s.actLag = append(s.actLag, d)
	s.mu.Unlock()
}
func (s *stats) addSendGap(d time.Duration) {
	s.mu.Lock()
	s.sendGap = append(s.sendGap, d)
	s.mu.Unlock()
}

func percentiles(ds []time.Duration) map[string]float64 {
	if len(ds) == 0 {
		return nil
	}
	cp := append([]time.Duration(nil), ds...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	at := func(p float64) float64 { return float64(cp[int(float64(len(cp)-1)*p)].Microseconds()) / 1000.0 }
	return map[string]float64{"p50": at(0.5), "p95": at(0.95), "p99": at(0.99), "max": float64(cp[len(cp)-1].Microseconds()) / 1000.0}
}

func (s *stats) rttMs() map[string]float64  { s.mu.Lock(); defer s.mu.Unlock(); return percentiles(s.rtt) }
func (s *stats) lagMs() map[string]float64  { s.mu.Lock(); defer s.mu.Unlock(); return percentiles(s.actLag) }
func (s *stats) gapMs() map[string]float64  { s.mu.Lock(); defer s.mu.Unlock(); return percentiles(s.sendGap) }
func (s *stats) errCopy() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.errs {
		out[k] = v
	}
	return out
}

// ---------- 客户端 ----------

type player struct {
	idx  int
	name string
	s    *stats
	conn *websocket.Conn
	wmu  sync.Mutex

	mu         sync.Mutex
	view       seatView
	viewAt     time.Time
	lastAct    time.Time
	retry      bool
	lastSeq    int64
	lastSendAt time.Time
	lastWrite  time.Time
	acts       int
	ended      bool

	room chans
}

// chans 收集房间级信号（房间码 / 开局 / 对局结束）。
type chans struct {
	roomCode chan string
	state    chan roomState
	done     chan struct{}
	once     sync.Once
}

func newChans() chans {
	return chans{roomCode: make(chan string, 4), state: make(chan roomState, 32), done: make(chan struct{})}
}

func (c *chans) finish() { c.once.Do(func() { close(c.done) }) }

func wsURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("无法识别的 scheme: %s", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	return u.String(), nil
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// newGuest 通过 HTTP 创建游客，返回 (guestId, token)。
func newGuest(base, nickname string) (string, string, error) {
	body, _ := json.Marshal(map[string]string{"nickname": nickname})
	resp, err := httpClient.Post(strings.TrimRight(base, "/")+"/api/v1/guests", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("建游客失败 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		GuestID string `json:"guestId"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("解析游客响应: %w", err)
	}
	if out.GuestID == "" || out.Token == "" {
		return "", "", errors.New("游客响应缺少 guestId/token")
	}
	return out.GuestID, out.Token, nil
}

func dialWS(addr string) (*websocket.Conn, error) {
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := d.Dial(addr, nil)
	return conn, err
}

// writeEnv 发送一条上行消息。
func writeEnv(conn *websocket.Conn, typ string, payload any) error {
	return writeEnvRef(conn, typ, payload, "")
}

func writeEnvRef(conn *websocket.Conn, typ string, payload any, ref string) error {
	env := envelope{V: protocol.Version, Type: typ, Ref: ref}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		env.Payload = raw
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func writeRaw(conn *websocket.Conn, data []byte) error {
	return conn.WriteMessage(websocket.TextMessage, data)
}

// readEnvelope 读一帧并解析；超时/断线返回错误。
func readEnvelope(conn *websocket.Conn, timeout time.Duration) (envelope, []byte, error) {
	if timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		return envelope{}, nil, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return envelope{}, data, err
	}
	return env, data, nil
}

// expectErrCode 读到一条错误事件并返回其 code。
func expectErrCode(conn *websocket.Conn, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		env, _, err := readEnvelope(conn, time.Until(deadline))
		if err != nil {
			return "", err
		}
		if env.Type != protocol.EvError {
			continue
		}
		var out struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(env.Payload, &out)
		return out.Code, nil
	}
	return "", errors.New("等待错误事件超时")
}

// newGuestConn 建游客 + 连 WS + 完成 guest.hello。
func newGuestConn(base, nickname string) (*websocket.Conn, string, error) {
	gid, tok, err := newGuest(base, nickname)
	if err != nil {
		return nil, "", err
	}
	addr, err := wsURL(base)
	if err != nil {
		return nil, "", err
	}
	conn, err := dialWS(addr)
	if err != nil {
		return nil, "", err
	}
	if err := writeEnv(conn, protocol.OpHello, protocol.HelloIn{GuestID: gid, Token: tok}); err != nil {
		conn.Close()
		return nil, "", err
	}
	env, _, err := readEnvelope(conn, 10*time.Second)
	if err != nil {
		conn.Close()
		return nil, "", err
	}
	if env.Type != protocol.EvHello {
		conn.Close()
		return nil, "", fmt.Errorf("握手失败, 收到 %s", env.Type)
	}
	return conn, gid, nil
}

func (p *player) send(typ string, payload any) error {
	ref := fmt.Sprintf("c%d-%d", p.idx, time.Now().UnixNano())
	env := envelope{V: protocol.Version, Type: typ, Ref: ref}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		env.Payload = raw
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	// 人为节流: 真实玩家每步都有人类反应时间, 压测客户端不应打满限流桶(10 条/秒)。
	if strings.HasPrefix(typ, "game.") {
		if wait := 120*time.Millisecond - time.Since(p.lastWrite); wait > 0 {
			time.Sleep(wait)
		}
		p.lastWrite = time.Now()
	}
	if err := p.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}
	p.s.addOut()
	if strings.HasPrefix(typ, "game.") {
		p.mu.Lock()
		p.lastSendAt = time.Now()
		p.mu.Unlock()
	}
	return nil
}

// readLoop 消费服务端事件：房间状态、对局视图、错误、保活回复。
func (p *player) readLoop() error {
	for {
		env, _, err := readEnvelope(p.conn, 0)
		if err != nil {
			return err
		}
		p.s.addIn()
		switch env.Type {
		case protocol.EvRoomState:
			var rs roomState
			if json.Unmarshal(env.Payload, &rs) == nil {
				select {
				case p.room.state <- rs:
				default:
				}
				if rs.Code != "" {
					select {
					case p.room.roomCode <- rs.Code:
					default:
					}
				}
			}

		case protocol.EvGameEvent:
			// 广播延迟: 本客户端发动作 → 收到下一条对局事件。
			p.mu.Lock()
			sentAt := p.lastSendAt
			p.lastSendAt = time.Time{}
			p.mu.Unlock()
			if !sentAt.IsZero() {
				p.s.addSendGap(time.Since(sentAt))
			}
			var ev struct {
				Kind string          `json:"kind"`
				Seat int             `json:"seat"`
				View json.RawMessage `json:"view"`
			}
			if json.Unmarshal(env.Payload, &ev) != nil || len(ev.View) == 0 {
				continue
			}
			var v seatView
			if json.Unmarshal(ev.View, &v) != nil {
				continue
			}
			p.mu.Lock()
			if env.Seq > 0 && env.Seq <= p.lastSeq {
				p.mu.Unlock()
				continue
			}
			if env.Seq > 0 {
				p.lastSeq = env.Seq
			}
			p.view = v
			p.viewAt = time.Now()
			ended := v.Result != nil || v.Phase == "finished"
			first := ended && !p.ended
			if ended {
				p.ended = true
			}
			p.mu.Unlock()
			if ended {
				if first {
					p.s.mu.Lock()
					p.s.finished++
					p.s.mu.Unlock()
				}
				continue
			}
			p.maybeAct()

		case protocol.EvError:
			var out struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(env.Payload, &out)
			p.s.addErr(out.Code)

		case protocol.EvPong:
			var out struct {
				TS int64 `json:"ts"`
			}
			if json.Unmarshal(env.Payload, &out) == nil && out.TS > 0 {
				p.s.addRTT(time.Since(time.UnixMilli(out.TS)))
			}
		}
	}
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// maybeAct 依据本人视图选一个动作；节流窗口内的准点视图不会丢（稍后按最新视图重试）。
func (p *player) maybeAct() {
	p.mu.Lock()
	v := p.view
	last := p.lastAct
	viewAt := p.viewAt
	p.mu.Unlock()
	if len(v.Actions) == 0 {
		return
	}
	if time.Since(last) < 30*time.Millisecond {
		p.mu.Lock()
		queued := p.retry
		p.retry = true
		p.mu.Unlock()
		if !queued {
			go func() {
				time.Sleep(35 * time.Millisecond)
				p.mu.Lock()
				p.retry = false
				p.mu.Unlock()
				p.maybeAct()
			}()
		}
		return
	}
	p.mu.Lock()
	p.lastAct = time.Now()
	p.mu.Unlock()
	p.s.addLag(time.Since(viewAt))

	switch {
	case has(v.Actions, "hu"), has(v.Actions, "hu.self"), has(v.Actions, "hu.robbing"):
		_ = p.send(protocol.OpGameAction, map[string]any{"action": "hu"})
	case has(v.Actions, "discard"):
		tile := v.Drawn
		if tile == "" && len(v.Hand) > 0 {
			tile = v.Hand[len(v.Hand)-1]
		}
		_ = p.send(protocol.OpGameAction, map[string]any{"action": "discard", "payload": map[string]string{"tile": tile}})
	case has(v.Actions, "draw"):
		_ = p.send(protocol.OpGameAction, map[string]any{"action": "draw"})
	case has(v.Actions, "pass"):
		_ = p.send(protocol.OpGameAction, map[string]any{"action": "pass"})
	default:
		return
	}
	p.s.addAct()
	p.mu.Lock()
	p.acts++
	p.mu.Unlock()
}

// pingLoop 每秒一次保活 + 延迟采样。
func (p *player) pingLoop(stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			_ = p.send(protocol.OpPing, map[string]int64{"ts": time.Now().UnixMilli()})
		}
	}
}

// ---------- normal 模式 ----------

type roomRun struct {
	code    string
	players []*player
	scores  [][]int
}

func runNormal(base string, rooms, perRoom int, timeout time.Duration) error {
	s := newStats()
	start := time.Now()
	var runs []*roomRun

	for r := 0; r < rooms; r++ {
		run := &roomRun{}
		for i := 0; i < perRoom; i++ {
			name := fmt.Sprintf("压测%d-%d", r, i)
			p := &player{idx: r*perRoom + i, name: name, s: s, room: newChans()}
			conn, _, err := newGuestConn(base, name)
			if err != nil {
				return fmt.Errorf("[room %d] 玩家 %d 连接失败: %w", r, i, err)
			}
			p.conn = conn
			go func(p *player) { _ = p.readLoop() }(p)
			run.players = append(run.players, p)
			time.Sleep(20 * time.Millisecond)
		}

		host := run.players[0]
		if err := host.send(protocol.OpRoomCreate, map[string]any{
			"name": "压测房", "players": perRoom, "public": false,
		}); err != nil {
			return fmt.Errorf("[room %d] 建房失败: %w", r, err)
		}
		var state roomState
		code := ""
		deadline := time.After(10 * time.Second)
		wait := true
		for wait {
			select {
			case state = <-host.room.state:
				if state.Code != "" && len(state.Players) > 0 {
					code, wait = state.Code, false
				}
			case <-deadline:
				return fmt.Errorf("[room %d] 建房超时", r)
			}
		}
		run.code = code
		fmt.Printf("[room %d] 房间码 %s, 加入 %d 名玩家…\n", r, code, perRoom-1)

		for _, p := range run.players[1:] {
			if err := p.send(protocol.OpRoomJoin, map[string]any{"code": code}); err != nil {
				return fmt.Errorf("[room %d] 加入失败: %w", r, err)
			}
			time.Sleep(30 * time.Millisecond)
		}

		// 等满座后开局。
		full := false
		for !full {
			select {
			case st := <-host.room.state:
				if st.Status == "lobby" && len(st.Players) >= perRoom {
					full = true
				}
			case <-time.After(10 * time.Second):
				return fmt.Errorf("[room %d] 等待满座超时", r)
			}
		}
		if err := host.send(protocol.OpGameStart, map[string]any{}); err != nil {
			return fmt.Errorf("[room %d] 开局失败: %w", r, err)
		}
		runs = append(runs, run)
	}

	stopPing := make(chan struct{})
	for _, run := range runs {
		for _, p := range run.players {
			go p.pingLoop(stopPing)
		}
	}

	// 等待超时：到点后收尾，报告已完成的局数。
	time.Sleep(timeout)
	close(stopPing)

	for _, run := range runs {
		for _, p := range run.players {
			p.mu.Lock()
			v := p.view
			acts := p.acts
			p.mu.Unlock()
			fmt.Fprintf(os.Stderr, "[room %s] 客户端 %d(%s) 动作=%d 牌墙=%d\n", run.code, p.idx, p.name, acts, v.WallLeft)
			if v.Result != nil && len(v.Result.Scores) > 0 {
				run.scores = append(run.scores, v.Result.Scores)
			}
			_ = p.conn.Close()
		}
	}

	consistent := true
	companions := 0
	for _, run := range runs {
		if len(run.scores) == 0 {
			continue
		}
		companions++
		first := run.scores[0]
		for _, sc := range run.scores[1:] {
			if len(sc) != len(first) {
				consistent = false
				continue
			}
			for i := range sc {
				if sc[i] != first[i] {
					consistent = false
				}
			}
		}
	}

	out := map[string]any{
		"mode":             "normal",
		"base":             base,
		"rooms":            rooms,
		"playersPerRoom":   perRoom,
		"durationSec":      int(time.Since(start).Seconds()),
		"msgsIn":           s.in,
		"msgsOut":          s.out,
		"actionsApplied":   s.acts,
		"gamesFinished":    s.finished,
		"roomsWithScores":  companions,
		"scoresConsistent": consistent,
		"errorsByCode":     s.errCopy(),
		"rttMs":            s.rttMs(),
		"actLagMs":         s.lagMs(),
		"sendGapMs":        s.gapMs(),
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}

// ---------- abuse 模式 ----------

type probe struct {
	name string
	pass bool
	note string
}

func runAbuse(base string) []probe {
	var out []probe
	add := func(name string, pass bool, note string) {
		out = append(out, probe{name: name, pass: pass, note: note})
		fmt.Printf("%-28s %v  %s\n", name, pass, note)
	}

	// 1. 坏 token 握手。
	{
		conn, err := dialWSOrErr(base)
		if err != nil {
			add("bad-token-hello", false, err.Error())
		} else {
			_ = writeEnv(conn, protocol.OpHello, protocol.HelloIn{GuestID: "nope", Token: "nope"})
			code, err := expectErrCode(conn, 5*time.Second)
			add("bad-token-hello", err == nil && code == "auth_failed", "code="+code)
			conn.Close()
		}
	}

	// 2. 首帧不是 hello。
	{
		conn, err := dialWSOrErr(base)
		if err != nil {
			add("first-frame-not-hello", false, err.Error())
		} else {
			_ = writeEnv(conn, protocol.OpRoomList, map[string]any{})
			code, err := expectErrCode(conn, 5*time.Second)
			add("first-frame-not-hello", err == nil && code == "expected_hello", "code="+code)
			conn.Close()
		}
	}

	// 3. 超长帧（2MB，超过 64KB 上限）。
	{
		conn, _, err := newGuestConn(base, "滥用-超长")
		if err != nil {
			add("oversized-frame", false, err.Error())
		} else {
			big := strings.Repeat("a", 2<<20)
			_ = writeEnv(conn, protocol.OpPing, map[string]string{"pad": big})
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, rerr := conn.ReadMessage()
			add("oversized-frame", rerr != nil, fmt.Sprintf("连接被关闭 err=%v", rerr))
			conn.Close()
		}
	}

	// 4. 非法 JSON。
	{
		conn, _, err := newGuestConn(base, "滥用-坏JSON")
		if err != nil {
			add("malformed-json", false, err.Error())
		} else {
			_ = writeRaw(conn, []byte("{not json"))
			code, cerr := expectErrCode(conn, 5*time.Second)
			add("malformed-json", cerr == nil && code == "bad_message", "code="+code)
			conn.Close()
		}
	}

	// 5. 洪泛：1 秒内 60 条，应触发限流。
	{
		conn, _, err := newGuestConn(base, "滥用-洪泛")
		if err != nil {
			add("flood-rate-limit", false, err.Error())
		} else {
			got := false
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for i := 0; i < 60; i++ {
				_ = writeEnv(conn, protocol.OpPing, map[string]int64{"ts": time.Now().UnixMilli()})
			}
			for {
				env, _, rerr := readEnvelope(conn, 5*time.Second)
				if rerr != nil {
					break
				}
				if env.Type == protocol.EvError {
					var e struct {
						Code string `json:"code"`
					}
					_ = json.Unmarshal(env.Payload, &e)
					if e.Code == "rate_limited" {
						got = true
						break
					}
				}
			}
			add("flood-rate-limit", got, "期望 rate_limited")
			conn.Close()
		}
	}

	// 6. 无效房间码。
	{
		conn, _, err := newGuestConn(base, "滥用-房号")
		if err != nil {
			add("join-invalid-code", false, err.Error())
		} else {
			_ = writeEnv(conn, protocol.OpRoomJoin, map[string]any{"code": "ZZZZZZ"})
			code, cerr := expectErrCode(conn, 5*time.Second)
			add("join-invalid-code", cerr == nil && code != "", "code="+code)
			conn.Close()
		}
	}

	// 7. 房间数超限（默认 max_online=3，连建 4 个应被拒）。
	{
		var conns []*websocket.Conn
		limitCode := ""
		for i := 0; i < 4; i++ {
			conn, _, err := newGuestConn(base, fmt.Sprintf("滥用-建%d", i))
			if err != nil {
				conns = append(conns, nil)
				continue
			}
			conns = append(conns, conn)
			_ = writeEnv(conn, protocol.OpRoomCreate, map[string]any{"name": "x", "players": 2, "public": false})
			// 读一条状态或错误即可。
			env, _, rerr := readEnvelope(conn, 5*time.Second)
			if rerr == nil && env.Type == protocol.EvError {
				var e struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(env.Payload, &e)
				limitCode = e.Code
			}
		}
		add("room-limit", limitCode == "max_online_rooms", "code="+limitCode)
		for _, c := range conns {
			if c != nil {
				c.Close()
			}
		}
	}

	// 8. 无效邀请令牌。
	{
		conn, _, err := newGuestConn(base, "滥用-邀请")
		if err != nil {
			add("invite-invalid-token", false, err.Error())
		} else {
			_ = writeEnv(conn, protocol.OpInviteJoin, map[string]any{"token": randomHex(16)})
			code, cerr := expectErrCode(conn, 5*time.Second)
			add("invite-invalid-token", cerr == nil && code != "", "code="+code)
			conn.Close()
		}
	}

	return out
}

func dialWSOrErr(base string) (*websocket.Conn, error) {
	addr, err := wsURL(base)
	if err != nil {
		return nil, err
	}
	return dialWS(addr)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- main ----------

func main() {
	mode := flag.String("mode", "normal", "normal|abuse")
	base := flag.String("base", "http://127.0.0.1:8790", "服务地址")
	rooms := flag.Int("rooms", 3, "房间数")
	players := flag.Int("players", 4, "每房人数")
	timeout := flag.Duration("timeout", 120*time.Second, "压测时长")
	flag.Parse()

	switch *mode {
	case "normal":
		if err := runNormal(*base, *rooms, *players, *timeout); err != nil {
			fmt.Fprintln(os.Stderr, "失败:", err)
			os.Exit(1)
		}
	case "abuse":
		probes := runAbuse(*base)
		failed := 0
		for _, p := range probes {
			if !p.pass {
				failed++
			}
		}
		b, _ := json.MarshalIndent(probes, "", "  ")
		fmt.Println(string(b))
		if failed > 0 {
			fmt.Fprintf(os.Stderr, "失败: %d 项滥用探测未达预期\n", failed)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "未知 mode:", *mode)
		os.Exit(2)
	}
}
