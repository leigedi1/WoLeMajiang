//go:build js && wasm

// 单机模式引擎桥: 把 internal/local(权威规则引擎 + AI)编译成 WebAssembly,
// 让浏览器在没有后端的情况下也能跑完整对局。
//
// 设计要点:
//   - 只暴露"开局 / 行动 / AI 走一步 / 取视图"四类入口, 规则判定全部在 Go 侧;
//   - 事件流与 WS 协议同构(kind/seat/tiles), 前端两条链路共用同一套渲染;
//   - 视图一律经 local.ViewFor 投影, 桥接层不做任何裁剪或补全;
//   - 所有返回值都是 JSON 字符串, 避免 syscall/js 的对象构建开销与泄漏面。
package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"majiang-server/internal/game"
	"majiang-server/internal/local"
)

// version 桥接协议版本, 前端据此拒绝不匹配的引擎。
const version = "1"

var (
	table *local.Table
	// 保持 js.Func 存活, 否则会被 GC 回收导致回调失效。
	keepAlive []js.Func
)

func main() {
	api := map[string]any{
		"version":  bind(apiVersion),
		"newMatch": bind(apiNewMatch),
		"act":      bind(apiAct),
		"aiStep":   bind(apiAIStep),
		"view":     bind(apiView),
		"state":    bind(apiState),
		"rules":    bind(apiRules),
		"tick":     bind(apiTick),
	}
	js.Global().Set("WoleEngine", js.ValueOf(api))
	select {} // 保持 Go 运行时存活
}

func bind(fn func(js.Value, []js.Value) any) js.Func {
	f := js.FuncOf(func(this js.Value, args []js.Value) any { return fn(this, args) })
	keepAlive = append(keepAlive, f)
	return f
}

func okOf(v map[string]any) string {
	v["ok"] = true
	b, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error":"结果编码失败"}`
	}
	return string(b)
}

func errOf(err error) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
	return string(b)
}

// jsonOf 生成 {ok, seq, finished, events} 形式的统一回复。
func jsonOf(extra map[string]any, since int) string {
	if table == nil {
		return errOf(local.ErrNoMatch)
	}
	out := map[string]any{
		"seq":      table.Seq(),
		"finished": table.Finished(),
		"events":   table.EventsSince(since),
	}
	for k, v := range extra {
		out[k] = v
	}
	return okOf(out)
}

func apiVersion(js.Value, []js.Value) any { return version }

func apiRules(js.Value, []js.Value) any {
	b, _ := json.Marshal(game.DefaultRuleSet())
	return string(b)
}

// newMatch(cfgJSON) -> {ok, seatCount, seq, dealer}
func apiNewMatch(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errOf(fmt.Errorf("newMatch 需要配置 JSON"))
	}
	var cfg local.Config
	if err := json.Unmarshal([]byte(args[0].String()), &cfg); err != nil {
		return errOf(fmt.Errorf("配置解析失败: %w", err))
	}
	tb, err := local.New(cfg)
	if err != nil {
		return errOf(err)
	}
	table = tb
	return okOf(map[string]any{
		"seatCount": tb.SeatCount(),
		"seq":       tb.Seq(),
		"dealer":    cfg.Dealer,
	})
}

// act(seat, action, payloadJSON, since) -> {ok, seq, finished, events}
func apiAct(_ js.Value, args []js.Value) any {
	if table == nil {
		return errOf(local.ErrNoMatch)
	}
	if len(args) < 2 {
		return errOf(fmt.Errorf("act 需要 seat 与 action"))
	}
	seat := args[0].Int()
	action := args[1].String()
	payload := "{}"
	if len(args) > 2 {
		payload = args[2].String()
	}
	since := 0
	if len(args) > 3 {
		since = args[3].Int()
	}
	if err := table.Apply(seat, action, json.RawMessage(payload)); err != nil {
		return errOf(err)
	}
	return jsonOf(map[string]any{"seat": seat, "action": action}, since)
}

// aiStep(since) -> {ok, acted, seat, seq, finished, events}
func apiAIStep(_ js.Value, args []js.Value) any {
	if table == nil {
		return errOf(local.ErrNoMatch)
	}
	since := 0
	if len(args) > 0 {
		since = args[0].Int()
	}
	seat, acted, err := table.StepAI()
	if err != nil {
		return errOf(err)
	}
	return jsonOf(map[string]any{"acted": acted, "seat": seat}, since)
}

// view(seat) -> SeatView JSON
func apiView(_ js.Value, args []js.Value) any {
	if table == nil {
		return errOf(local.ErrNoMatch)
	}
	seat := 0
	if len(args) > 0 {
		seat = args[0].Int()
	}
	v, err := table.ViewFor(seat)
	if err != nil {
		return errOf(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return errOf(err)
	}
	return string(b)
}

// state() -> {ok, seq, finished, seatCount}
func apiState(js.Value, []js.Value) any {
	if table == nil {
		return errOf(local.ErrNoMatch)
	}
	return okOf(map[string]any{
		"seq":       table.Seq(),
		"finished":  table.Finished(),
		"seatCount": table.SeatCount(),
	})
}

// tick(nowMs, since) -> {ok, changed, seq, finished, events}
//
// 驱动阶段10 的响应/出牌倒计时: 前端按 200ms 左右轮询调用, 到点的座位
// 由引擎自动"过"或自动打牌。nowMs 省略时取浏览器当前时间。
func apiTick(_ js.Value, args []js.Value) any {
	if table == nil {
		return errOf(local.ErrNoMatch)
	}
	now := time.Now()
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		ms := int64(args[0].Float())
		if ms > 0 {
			now = time.UnixMilli(ms)
		}
	}
	since := 0
	if len(args) > 1 {
		since = args[1].Int()
	}
	changed := table.Tick(now)
	return jsonOf(map[string]any{"changed": changed}, since)
}
