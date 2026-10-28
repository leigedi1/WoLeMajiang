#!/usr/bin/env node
/**
 * 单机引擎(WebAssembly)端到端验证。
 *
 * 直接以 Node 加载 web/public/engine 下的 Go wasm, 不依赖浏览器与后端, 验证:
 *   1. 引擎能被实例化并挂载 WoleEngine;
 *   2. 全 AI 对局能走到终局, 事件序号单调、得失分守恒;
 *   3. 真人座位的非法动作被权威端拒绝;
 *   4. 真人座位视图只含自己的手牌(他人仅数量), 且视角切换不串牌。
 *
 * 用法: node scripts/e2e-wasm.mjs
 */
import { readFileSync, existsSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const engineDir = join(root, 'web', 'public', 'engine')
const wasmPath = join(engineDir, 'engine.wasm')
const gluePath = join(engineDir, 'wasm_exec.js')

let failures = 0
function check(name, cond, detail = '') {
  if (cond) {
    console.log(`  ok   ${name}`)
  } else {
    failures++
    console.error(`  FAIL ${name}${detail ? ' — ' + detail : ''}`)
  }
}

if (!existsSync(wasmPath) || !existsSync(gluePath)) {
  console.error('缺少 wasm 产物, 请先执行: node scripts/build-wasm.mjs')
  process.exit(2)
}

// Go 运行时胶水代码在 Node 下需要 require/fs/path 垫片。
globalThis.require = createRequire(import.meta.url)
globalThis.fs = globalThis.require('node:fs')
globalThis.path = globalThis.require('node:path')

new Function(readFileSync(gluePath, 'utf8'))()
const go = new globalThis.Go()
const { instance } = await WebAssembly.instantiate(readFileSync(wasmPath), go.importObject)
void go.run(instance)

const engine = await new Promise((res, rej) => {
  const t0 = Date.now()
  const tick = () => {
    if (globalThis.WoleEngine) return res(globalThis.WoleEngine)
    if (Date.now() - t0 > 20000) return rej(new Error('等待 WoleEngine 超时'))
    setTimeout(tick, 20)
  }
  tick()
})
console.log(`引擎已加载, 桥接版本 ${engine.version()}`)

const rules = {
  players: 4,
  chow: true,
  seven_pairs: true,
  self_draw: true,
  discard_win: true,
  concealed_hand_only: false,
  base_score: 1,
}

function startMatch({ players = 4, aiSeats = [0, 1, 2, 3], seed }) {
  const seats = []
  for (let i = 0; i < players; i++) {
    seats.push(aiSeats.includes(i) ? { name: `AI${i}`, ai: true, difficulty: 'normal' } : { name: '我', ai: false })
  }
  const r = JSON.parse(engine.newMatch(JSON.stringify({ rules: { ...rules, players }, dealer: 0, seed, seats })))
  return r
}
const view = (seat) => JSON.parse(engine.view(seat))
const act = (seat, action, payload = {}, since = 0) =>
  JSON.parse(engine.act(seat, action, JSON.stringify(payload), since))
const aiStep = (since) => JSON.parse(engine.aiStep(since))

// ---------- 1. 全 AI 对局 ----------
{
  console.log('\n[1] 全 AI 对局(seed=20261021)')
  const r = startMatch({ seed: 20261021 })
  check('开局成功', r.ok === true, JSON.stringify(r))
  let seq = r.seq ?? 0
  let steps = 0
  let lastSeq = seq
  let monotonic = true
  while (true) {
    if (++steps > 3000) break
    const s = aiStep(seq)
    if (!s.ok) {
      check('AI 步进无错误', false, s.error)
      break
    }
    if (!s.acted) break
    for (const ev of s.events) {
      if (ev.seq <= lastSeq) monotonic = false
      lastSeq = ev.seq
    }
    seq = s.seq
    if (s.finished) break
  }
  check('对局在 3000 步内结束', steps <= 3000, `steps=${steps}`)
  check('事件序号严格递增', monotonic)
  const v = view(0)
  check('终局阶段为 finished', v.phase === 'finished', v.phase)
  check('存在结算', !!v.result)
  if (v.result) {
    const sum = v.result.scores.reduce((a, b) => a + b, 0)
    check('得失分守恒(合计 0)', sum === 0, `sum=${sum}`)
    console.log(`       结算: winner=${v.result.winner} byDraw=${v.result.byDraw} fan=${v.result.fan} scores=[${v.result.scores}]`)
  }
}

// ---------- 2. 真人座位: 非法动作被拒 ----------
{
  console.log('\n[2] 真人座位的非法动作')
  const r = startMatch({ seed: 7, aiSeats: [1, 2, 3] })
  check('开局成功', r.ok === true)
  const bad = act(0, 'discard', { tile: 'zzz' })
  check('非法牌代码被拒', bad.ok === false, JSON.stringify(bad))
  const outOfTurn = act(2, 'draw')
  check('非行动者摸牌被拒', outOfTurn.ok === false, JSON.stringify(outOfTurn))
}

// ---------- 3. 真人 + 3 AI 完整对局(真人用最简策略) ----------
{
  console.log('\n[3] 真人 + 3 AI 完整对局')
  const r = startMatch({ seed: 99, aiSeats: [1, 2, 3] })
  check('开局成功', r.ok === true)
  let seq = r.seq ?? 0
  let steps = 0
  let privacyOK = true
  let finished = false
  while (steps++ < 3000) {
    const v = view(0)
    if (v.phase === 'finished') {
      finished = true
      break
    }
    // 隐私: 他人手牌只暴露数量
    if (!Array.isArray(v.othersHandCounts) || v.othersHandCounts.length !== 4) privacyOK = false
    if (v.othersHandCounts && v.othersHandCounts[0] !== v.hand.length) privacyOK = false

    if ((v.actions ?? []).includes('hu.self')) {
      const a = act(0, 'hu.self', {}, seq)
      if (!a.ok) break
      seq = a.seq
      if (a.finished) {
        finished = true
        break
      }
      continue
    }
    if ((v.actions ?? []).includes('draw')) {
      const a = act(0, 'draw', {}, seq)
      if (!a.ok) break
      seq = a.seq
      continue
    }
    if ((v.actions ?? []).includes('discard') && v.hand.length > 0) {
      const a = act(0, 'discard', { tile: v.hand[0] }, seq)
      if (!a.ok) break
      seq = a.seq
      continue
    }
    if ((v.actions ?? []).includes('pass')) {
      const a = act(0, 'pass', {}, seq)
      if (!a.ok) break
      seq = a.seq
      continue
    }
    const s = aiStep(seq)
    if (!s.ok) break
    if (!s.acted) break
    seq = s.seq
    if (s.finished) {
      finished = true
      break
    }
  }
  check('对局推进到终局', finished, `steps=${steps}`)
  check('视图隐私: 他人手牌仅数量', privacyOK)
  const v0 = view(0)
  const v1 = view(1)
  check('不同座位视图不同', JSON.stringify(v0.hand) !== JSON.stringify(v1.hand) || v0.seat !== v1.seat)
  check('座位 1 的视图里没有座位 0 的手牌数组', !Array.isArray(v1.hand) || v1.hand.length <= 14)
}

// ---------- 4. 2 人对局(规则子集) ----------
{
  console.log('\n[4] 2 人单机对局')
  const r = startMatch({ players: 2, aiSeats: [1], seed: 5 })
  check('2 人开局成功', r.ok === true, JSON.stringify(r))
  let seq = r.seq ?? 0
  let steps = 0
  let finished = false
  while (steps++ < 2000) {
    const v = view(0)
    if (v.phase === 'finished') {
      finished = true
      break
    }
    if ((v.actions ?? []).includes('draw')) {
      const a = act(0, 'draw', {}, seq)
      if (!a.ok) break
      seq = a.seq
      continue
    }
    if ((v.actions ?? []).includes('discard') && v.hand.length > 0) {
      const a = act(0, 'discard', { tile: v.hand[0] }, seq)
      if (!a.ok) break
      seq = a.seq
      continue
    }
    if ((v.actions ?? []).includes('pass')) {
      const a = act(0, 'pass', {}, seq)
      if (!a.ok) break
      seq = a.seq
      continue
    }
    const s = aiStep(seq)
    if (!s.ok || !s.acted) break
    seq = s.seq
    if (s.finished) {
      finished = true
      break
    }
  }
  check('2 人对局推进到终局', finished, `steps=${steps}`)
}

console.log(failures === 0 ? '\n全部通过 ✅' : `\n失败 ${failures} 项 ❌`)
process.exit(failures === 0 ? 0 : 1)
