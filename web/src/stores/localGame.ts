/**
 * 单机对局存储: 用浏览器内 WASM 引擎(与联机后端同一套 Go 规则代码)驱动
 * "真人 + AI" 的完整对局。
 *
 * 与联机链路的差异只在传输: 这里是同进程函数调用, 事件流与视图格式完全相同,
 * 因此牌桌组件可以原样复用。
 *
 * 不变量:
 *   - 视图只来自引擎投影(engine.view), 前端不构造、不推测任何牌面;
 *   - 人类玩家自己的动作一律经 engine.act, 由引擎校验合法性;
 *   - AI 决策在引擎内部完成, 前端只负责"叫下一步"与节奏。
 */
import { defineStore } from 'pinia'
import type { PlayerView, SeatView } from '../protocol/types'
import { loadEngine, type Engine, type EngineEvent, type RulesConfig, type SeatSpec } from '../engine/engine'

export type LocalStatus = 'idle' | 'loading' | 'playing' | 'finished' | 'error'

export type LocalDifficulty = 'easy' | 'normal' | 'hard'

export interface StartOptions {
  /** 座位数 2~4 */
  players?: number
  /** AI 难度(全部 AI 共用) */
  difficulty?: LocalDifficulty
  /** 真人座位, 默认 0(东家) */
  humanSeat?: number
  /** 固定牌墙种子(仅测试/复盘用; 0 表示加密随机) */
  seed?: number
  /** 人类昵称 */
  nickname?: string
  /** 自定义牌集: 牌种代码 → 每种张数(省略为标准 136 张) */
  pool?: Record<string, number>
  /**
   * 显式座位表(P2P 房主用): 真实玩家与 AI 混排, 长度即人数。
   * 提供时忽略 players/difficulty/nickname 的座位推导, 只沿用其实例上的 hostSeat。
   */
  seats?: SeatSpec[]
  /** 本机(房主自己)的座位号; 单机模式等于 humanSeat */
  hostSeat?: number
  /** 与 seats 一一对应的归属 guestId(房间投影用; 缺省由 seats 推导) */
  seatOwners?: string[]
  /** 阶段10 细则(计时/截牌/和牌条件), 缺省用引擎默认值 */
  rules?: Partial<RulesConfig>
}

/** 需要人类亲自决定的响应动作(出现即应暂停自动推进)。 */
const RESPOND_ACTIONS = ['pass', 'pong', 'kong.exposed', 'chow', 'hu.robbing']

const DEFAULT_AI_DELAY_MS = 650

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

let engineOverride: Engine | null = null

/** 仅供测试注入假引擎(传 null 恢复真实 wasm 加载)。 */
export function __setEngineForTest(e: Engine | null): void {
  engineOverride = e
}

export const useLocalGameStore = defineStore('localGame', {
  state: () => ({
    status: 'idle' as LocalStatus,
    /** 人类座位号 */
    humanSeat: 0,
    /** 人类座位视角的投影 */
    view: null as SeatView | null,
    /** 牌桌需要的玩家列表(由座位表合成) */
    players: [] as PlayerView[],
    /** 座位描述(昵称/是否 AI) */
    seats: [] as SeatSpec[],
    /** 与 seats 一一对应的归属 guestId(房间投影用) */
    seatOwners: [] as string[],
    difficulty: 'normal' as LocalDifficulty,
    playersCount: 4,
    errorMessage: '',
    /** 事件序号水位线 */
    seq: 0,
    /** 正在思考的 AI 座位(-1 表示无) */
    thinkingSeat: -1,
    /** AI 每步的思考延迟(毫秒), 0 表示不等待 */
    aiDelayMs: DEFAULT_AI_DELAY_MS,
    /** 最近若干事件(用于提示条) */
    recentEvents: [] as EngineEvent[],
    // 内部状态(不参与渲染)
    _engine: null as Engine | null,
    _pumping: false,
    _pending: null as { seat: number; action: string; payload: Record<string, unknown> } | null,
    /** 倒计时轮询定时器(阶段10: 响应/出牌超时由引擎托管) */
    _tickTimer: null as ReturnType<typeof setInterval> | null,
  }),

  getters: {
    playing: (s) => s.status === 'playing' && s.view !== null,
    isFinished: (s) => s.status === 'finished' && s.view?.phase === 'finished',
    myActions(): string[] {
      return this.view?.actions ?? []
    },
    canAct(): boolean {
      return this.status === 'playing' && ((this.view?.actions ?? []).length ?? 0) > 0
    },
    /** 最后一条公共事件, 用于显示"某家摸牌/打牌"提示 */
    lastEvent: (s) => (s.recentEvents.length > 0 ? s.recentEvents[s.recentEvents.length - 1] : null),
  },

  actions: {
    /** 开始一局单机对局 */
    async start(opts: StartOptions = {}): Promise<void> {
      // 显式座位表(P2P 房主): 长度即人数, 真人/AI 由调用方决定。
      const explicit = opts.seats && opts.seats.length >= 2 ? opts.seats.slice(0, 4) : null
      const players = explicit ? explicit.length : Math.min(4, Math.max(2, opts.players ?? 4))
      const humanSeat = Math.min(players - 1, Math.max(0, opts.hostSeat ?? opts.humanSeat ?? 0))
      const difficulty = opts.difficulty ?? 'normal'
      const nickname = opts.nickname?.trim() || '我'

      this.status = 'loading'
      this.errorMessage = ''
      this.view = null
      this.players = []
      this.recentEvents = []
      this.seq = 0
      this.thinkingSeat = -1
      this._pending = null
      this.humanSeat = humanSeat
      this.playersCount = players
      this.difficulty = difficulty

      const seats: SeatSpec[] = []
      if (explicit) {
        seats.push(...explicit)
      } else {
        for (let i = 0; i < players; i++) {
          if (i === humanSeat) {
            seats.push({ name: nickname, ai: false })
          } else {
            seats.push({ name: `电脑 ${i + 1}`, ai: true, difficulty })
          }
        }
      }
      const owners: string[] =
        opts.seatOwners && opts.seatOwners.length === seats.length
          ? opts.seatOwners.slice()
          : seats.map((s, i) => (s.ai ? `local-ai-${i}` : i === humanSeat ? 'local-human' : `p2p-seat-${i}`))

      try {
        const engine = engineOverride ?? (await loadEngine())
        this._engine = engine
        const r = engine.newMatch({
          rules: {
            ...(opts.rules ?? {}),
            players,
            ...(opts.pool ? { pool: opts.pool } : {}),
          },
          dealer: 0,
          seed: opts.seed ?? 0,
          seats,
        })
        if (!r.ok) {
          this.fail(r.error ?? '开局失败')
          return
        }
        this.seats = seats
        this.seatOwners = owners
        this.seq = r.seq ?? 0
        this.status = 'playing'
        this.players = await this.buildPlayers()
        await this.refreshView()
        void this.pump()
        this._startTickTimer()
      } catch (err) {
        this.fail(err instanceof Error ? err.message : String(err))
      }
    },

    /** 人类动作入口(由 ActionControls 发出) */
    act(action: string, payload: Record<string, unknown> = {}): void {
      if (this.status !== 'playing' || !this._engine) return
      this._pending = { seat: this.humanSeat, action, payload }
      void this.pump()
    },

    /**
     * 按座位行动(P2P 房主用): 远端真人座位的动作由此进入同一个权威引擎。
     * 座位必须是非 AI 座位, 否则视为非法调用。
     */
    actAs(seat: number, action: string, payload: Record<string, unknown> = {}): void {
      if (this.status !== 'playing' || !this._engine) return
      if (!Number.isInteger(seat) || seat < 0 || seat >= this.seats.length) return
      if (this.seats[seat]?.ai) return
      this._pending = { seat, action, payload }
      void this.pump()
    },

    /** 全部真人座位号(P2P 房主需要逐座位等待各自决策) */
    humanSeats(): number[] {
      const out: number[] = []
      for (let i = 0; i < this.seats.length; i++) if (!this.seats[i]?.ai) out.push(i)
      return out
    },

    /** 某座位的最新投影(房主自己复用已缓存的 view) */
    viewOf(seat: number): SeatView | null {
      if (seat === this.humanSeat) return this.view
      return this.viewForSeat(seat)
    },

    /** 结束/退出单机对局 */
    stop(): void {
      this._stopTickTimer()
      this.status = 'idle'
      this.view = null
      this.players = []
      this.seats = []
      this.seatOwners = []
      this.recentEvents = []
      this.thinkingSeat = -1
      this.errorMessage = ''
      this._pending = null
      this._engine = null
      this.seq = 0
    },

    /** 清空错误 */
    clearError(): void {
      this.errorMessage = ''
      if (this.status === 'error') this.status = 'idle'
    },

    fail(message: string): void {
      this.status = 'error'
      this.errorMessage = message
      this._pending = null
      this.thinkingSeat = -1
    },

    /** 拉取最新投影 */
    async refreshView(): Promise<SeatView | null> {
      if (!this._engine) return null
      try {
        const v = this._engine.view<SeatView>(this.humanSeat)
        this.view = v
        return v
      } catch (err) {
        this.fail(err instanceof Error ? err.message : String(err))
        return null
      }
    },

    /** 任意座位的私有投影 (P2P 房主按座位单播给加入者用; 含该座位手牌) */
    viewForSeat(seat: number): SeatView | null {
      if (!this._engine) return null
      try {
        return this._engine.view<SeatView>(seat)
      } catch {
        return null
      }
    },

    /** 由座位表 + 当前视图合成牌桌所需的玩家列表 */
    async buildPlayers(): Promise<PlayerView[]> {
      const out: PlayerView[] = []
      const v = this.view
      for (let i = 0; i < this.seats.length; i++) {
        const spec = this.seats[i]!
        out.push({
          guestId: this.seatOwners[i] ?? (spec.ai ? `local-ai-${i}` : i === this.humanSeat ? 'local-human' : `p2p-seat-${i}`),
          nickname: spec.name,
          seat: i,
          connected: true,
          tileCount: v && v.othersHandCounts ? (v.othersHandCounts[i] ?? 0) : 0,
        })
      }
      return out
    },

    /**
     * 自动推进循环: 处理待发动作、人类自动摸牌、AI 逐家行动。
     * 遇到"只有人类能决定"的局面(响应窗口/胡牌选择)即停止, 把控制权交给 UI。
     */
    async pump(): Promise<void> {
      if (this._pumping) return
      if (this.status !== 'playing' || !this._engine) return
      const engine = this._engine
      this._pumping = true
      try {
        // 上限防止逻辑异常导致死循环。
        for (let guard = 0; guard < 5000; guard++) {
          if (this.status !== 'playing') return

          // 1. 待发动作(可能来自本机 UI, 也可能来自 P2P 远端真人)
          if (this._pending) {
            const { seat, action, payload } = this._pending
            this._pending = null
            const r = engine.act(seat, action, payload, this.seq)
            if (!r.ok) {
              this.errorMessage = r.error ?? '非法操作'
              continue
            }
            this.seq = r.seq
            this.pushEvents(r.events)
            await this.refreshView()
            if (r.finished) {
              this.finish()
              return
            }
            continue
          }

          const view = this.view ?? (await this.refreshView())
          if (!view) return
          if (view.phase === 'finished') {
            this.finish()
            return
          }

          // 2. 强制摸牌: 任一真人座位轮到摸牌时直接执行(摸牌没有可选项)
          let drewSeat = -1
          for (const seat of this.humanSeats()) {
            const hv = this.viewOf(seat)
            if (!hv) continue
            // 未启用响应倒计时时, 出牌者的下家可以用「摸牌」直接收敛响应窗口。
            if (hv.forceDraw) {
              const r = engine.act(seat, 'forcedraw', {}, this.seq)
              if (r.ok) {
                drewSeat = seat
                this.seq = r.seq
                this.pushEvents(r.events)
                await this.refreshView()
                if (r.finished) {
                  this.finish()
                  return
                }
              }
              break
            }
            if (!(hv.actions ?? []).includes('draw')) continue
            const r = engine.act(seat, 'draw', {}, this.seq)
            if (!r.ok) {
              this.fail(r.error ?? '摸牌失败')
              return
            }
            drewSeat = seat
            this.seq = r.seq
            this.pushEvents(r.events)
            await this.refreshView()
            if (r.finished) {
              this.finish()
              return
            }
            break
          }
          if (drewSeat >= 0) continue

          // 2.5 倒计时: 响应/出牌超时由引擎自动"过"或自动打牌。
          const tk = engine.tick(this.seq)
          if (tk.ok && tk.changed) {
            this.seq = tk.seq
            this.pushEvents(tk.events)
            await this.refreshView()
            if (tk.finished) {
              this.finish()
              return
            }
            continue
          }

          // 3. 任一真人需要决定 → 停在此处, 等该座位玩家动作(本机 UI 或 P2P 远端)
          for (const seat of this.humanSeats()) {
            const hv = this.viewOf(seat)
            if (hv && this.needsHumanDecision(hv)) {
              this.thinkingSeat = -1
              return
            }
          }

          // 4. 交给 AI 走一步
          this.thinkingSeat = -1
          if (this.aiDelayMs > 0) await delay(this.aiDelayMs)
          if (this.status !== 'playing') return
          const r = engine.aiStep(this.seq)
          if (!r.ok) {
            this.fail(r.error ?? 'AI 行动失败')
            return
          }
          if (!r.acted) {
            // 没有 AI 可行动: 若人类也没有动作则是异常状态。
            await this.refreshView()
            this.thinkingSeat = -1
            return
          }
          this.thinkingSeat = r.seat
          this.seq = r.seq
          this.pushEvents(r.events)
          await this.refreshView()
          this.thinkingSeat = -1
          if (r.finished) {
            this.finish()
            return
          }
        }
        this.fail('自动推进超出步数上限, 已停止(疑似死循环)')
      } finally {
        this._pumping = false
      }
    },

    /**
     * 是否需要人类亲自决定。
     * - 出牌阶段轮到自己: 必须人类选牌;
     * - 出现响应类动作(含胡): 必须人类确认。
     */
    needsHumanDecision(view: SeatView): boolean {
      if ((view.actions ?? []).includes('hu.self')) return true
      if ((view.actions ?? []).includes('discard')) return true
      for (const a of RESPOND_ACTIONS) {
        if ((view.actions ?? []).includes(a)) return true
      }
      return false
    },

    pushEvents(events: EngineEvent[]): void {
      if (!events || events.length === 0) return
      const merged = [...this.recentEvents, ...events]
      this.recentEvents = merged.slice(-12)
    },

    /** 启动倒计时轮询: 200ms 一次, 让引擎托管响应/出牌超时。 */
    _startTickTimer(): void {
      this._stopTickTimer()
      this._tickTimer = setInterval(() => {
        if (this.status !== 'playing' || !this._engine || this._pumping) return
        void this.pump()
      }, 200)
    },

    _stopTickTimer(): void {
      if (this._tickTimer !== null) {
        clearInterval(this._tickTimer)
        this._tickTimer = null
      }
    },

    finish(): void {
      this._stopTickTimer()
      this.status = 'finished'
      this.thinkingSeat = -1
      this._pending = null
      void this.buildPlayers().then((p) => {
        this.players = p
      })
    },
  },
})
