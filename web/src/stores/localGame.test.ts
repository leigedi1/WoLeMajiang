/**
 * 单机 store 的调度测试。
 *
 * 这里用**假引擎**替换 wasm, 只验证 store 自身的职责:
 *   - 开局后把引擎投影放进 view/players;
 *   - 轮到真人时停下等待输入, 不会自己替真人出牌;
 *   - 真人动作原样转交 engine.act;
 *   - AI 连续推进直到终局, 并把结算带进 view。
 * 真实 wasm 的对局正确性由 scripts/e2e-wasm.mjs 覆盖。
 */
import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { __setEngineForTest, useLocalGameStore } from './localGame'
import type { Engine, NewMatchReply, ActReply, AiStepReply } from '../engine/engine'

/** 构造一个按脚本推进的假引擎。 */
function fakeEngine(script: 'human_first' | 'ai_then_finish') {
  const calls: string[] = []
  let step = 0
  let phase = 'playing'
  const view = (seat: number) => ({
    seat,
    phase,
    turnSeat: step === 0 ? 0 : 1,
    turnStage: 'discard',
    wallLeft: 80,
    hand: ['wan1', 'wan2', 'tiao3'],
    melds: [],
    discards: [[], [], [], []],
    othersHandCounts: [13, 13, 13, 13],
    othersMelds: [[], [], [], []],
    actions: step === 0 ? ['discard', 'hu.self'] : [],
    result:
      phase === 'finished'
        ? { winner: 0, byDraw: true, fan: 2, pay: 4, scores: [12, -4, -4, -4] }
        : undefined,
  })

  const engine: Engine = {
    version: 'test',
    newMatch: (): NewMatchReply => {
      calls.push('newMatch')
      return { ok: true, seatCount: 4, seq: 1, dealer: 0 }
    },
    act: (_seat, action): ActReply => {
      calls.push(`act:${_seat}:${action}`)
      step++
      return { ok: true, seq: step + 1, events: [{ kind: action, seat: 0, seq: step + 1 }], finished: false }
    },
    aiStep: (): AiStepReply => {
      calls.push('aiStep')
      step++
      if (script === 'ai_then_finish' && step >= 2) {
        phase = 'finished'
        return { ok: true, acted: true, seat: 1, seq: step + 1, events: [], finished: phase === 'finished' }
      }
      return { ok: true, acted: true, seat: 1, seq: step + 1, events: [], finished: phase === 'finished' }
    },
    view: <T,>(seat: number) => view(seat) as unknown as T,
    tick: () => ({ ok: true, changed: false, seq: step, finished: phase === 'finished', events: [] }),
    state: () => ({
      ok: true,
      seq: step,
      finished: phase === 'finished',
      seatCount: 4,
    }),
  }
  return { engine, calls }
}

describe('useLocalGameStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    __setEngineForTest(null)
  })

  it('开局后展示自己的投影, 并合成四家玩家列表', async () => {
    const { engine } = fakeEngine('human_first')
    __setEngineForTest(engine)
    const store = useLocalGameStore()
    store.aiDelayMs = 0

    await store.start({ players: 4, difficulty: 'normal', humanSeat: 0, nickname: '我' })

    expect(store.playing).toBe(true)
    expect(store.view?.hand).toEqual(['wan1', 'wan2', 'tiao3'])
    expect(store.players).toHaveLength(4)
    expect(store.players[0].nickname).toBe('我')
    expect(store.myActions).toContain('discard')
  })

  it('真人动作原样转交引擎, 不自行判定合法性', async () => {
    const { engine, calls } = fakeEngine('human_first')
    __setEngineForTest(engine)
    const store = useLocalGameStore()
    store.aiDelayMs = 0
    await store.start({ players: 4, humanSeat: 0, nickname: '我' })

    store.act('discard', { tile: 'wan1' })
    await new Promise((r) => setTimeout(r, 30))

    expect(calls).toContain('act:0:discard')
  })

  it('只在自己回合给出可选动作, 且 AI 推进到终局后带出结算', async () => {
    const { engine, calls } = fakeEngine('ai_then_finish')
    __setEngineForTest(engine)
    const store = useLocalGameStore()
    store.aiDelayMs = 0
    await store.start({ players: 4, humanSeat: 0, nickname: '我' })

    // 让 store 完成一次真人动作, 之后交给 AI 自行推进到终局。
    store.act('discard', { tile: 'wan1' })
    await new Promise((r) => setTimeout(r, 80))

    expect(calls.some((c) => c === 'aiStep')).toBe(true)
    store.stop()
    expect(store.playing).toBe(false)
  })

  it('多真人座位(P2P 房主): 各座位独立等待决策, 远端动作按其座位执行', async () => {
    const { engine, calls } = fakeEngine('human_first')
    __setEngineForTest(engine)
    const store = useLocalGameStore()
    store.aiDelayMs = 0

    await store.start({
      seats: [
        { name: '丙', ai: false },
        { name: '丁', ai: false },
      ],
      hostSeat: 0,
      seatOwners: ['guest-a', 'guest-b'],
    })

    expect(store.humanSeats()).toEqual([0, 1])
    expect(store.players.map((p) => p.nickname)).toEqual(['丙', '丁'])
    expect(store.players[1]?.guestId).toBe('guest-b')
    // 两个真人都在等自己决定 → store 不能替任何一方出牌, 也不能调 AI
    expect(calls).not.toContain('aiStep')

    store.actAs(1, 'discard', { tile: 'wan2' })
    await new Promise((r) => setTimeout(r, 30))
    expect(calls).toContain('act:1:discard')

    // 越界座位不接受远端真人驱动
    store.actAs(3, 'discard', {})
    await new Promise((r) => setTimeout(r, 20))
    expect(calls.filter((c) => c === 'act:3:discard')).toHaveLength(0)
  })
})
