/**
 * 单机引擎桥接: 加载 Go 编译的 WebAssembly 权威引擎, 提供类型化 API。
 *
 * 职责边界:
 *   - 这里只做"调用与 JSON 解析", 不含任何麻将规则;
 *   - 规则判定、合法动作、牌型与计分全部在 Go 侧(与联机后端同一套代码);
 *   - 视图(View)由引擎按座位投影, 前端不做裁剪、不做补全。
 *
 * 资源: /engine/engine.wasm + /engine/wasm_exec.js(由 scripts/build-wasm.mjs 生成)。
 */

/** 引擎侧公共事件(与 WS 协议的 game.event 负载同构)。 */
export interface EngineEvent {
  seq: number
  kind: string
  seat: number
  tiles?: string[]
}

/** 统一回复包装(aiact/aiStep)。 */
interface ReplyBase {
  ok: boolean
  error?: string
  seq: number
  finished: boolean
  events: EngineEvent[]
}

export interface ActReply extends ReplyBase {
  seat?: number
  action?: string
}

export interface AiStepReply extends ReplyBase {
  acted: boolean
  seat: number
}

export interface TickReply extends ReplyBase {
  changed: boolean
}

export interface NewMatchReply {
  ok: boolean
  error?: string
  seatCount?: number
  seq?: number
  dealer?: number
}

export interface SeatSpec {
  name: string
  ai: boolean
  difficulty?: 'easy' | 'normal' | 'hard'
}

/** 规则配置(键名与 Go game.RuleSet 的 json tag 一致)。 */
export interface RulesConfig {
  players: number
  chow: boolean
  seven_pairs: boolean
  self_draw: boolean
  discard_win: boolean
  concealed_hand_only: boolean
  base_score: number
  /** 碰/杠/吃的响应倒计时(秒), 0 表示不设倒计时。 */
  respond_meld_sec: number
  /** 荣和(胡)的响应倒计时(秒), 0 表示不设倒计时。 */
  respond_win_sec: number
  /** 出牌倒计时(秒), 0 表示不限时。 */
  discard_sec: number
  /** 截胡: 多人可胡时按座次顺序由最近的一家先胡。 */
  rob_win: boolean
  /** 截碰/杠: 多人可碰杠时按座次顺序优先。 */
  rob_meld: boolean
  /** 抢杠胡: 别人补杠的牌正好是你要和的牌时可以抢。 */
  rob_kong_win?: boolean
  /** 杠上开花: 杠后补牌自摸加番。 */
  kong_flower?: boolean
  /** 海底捞月 / 河底捞鱼: 牌墙最后一张自摸 / 最后一张打出的牌被和加番。 */
  last_tile?: boolean
  /** 自定义牌集: 牌种代码 → 每种张数(省略为标准 136 张)。 */
  pool?: Record<string, number>
}

/** 与 Go 侧 DefaultRuleSet() 完全一致的首版预设。 */
export const DEFAULT_RULES: RulesConfig = {
  players: 4,
  chow: true,
  seven_pairs: true,
  self_draw: true,
  discard_win: true,
  concealed_hand_only: false,
  base_score: 1,
  respond_meld_sec: 3,
  respond_win_sec: 5,
  discard_sec: 0,
  rob_win: true,
  rob_meld: true,
  rob_kong_win: true,
  kong_flower: true,
  last_tile: true,
}

export interface MatchConfig {
  rules?: Partial<RulesConfig>
  dealer?: number
  seed?: number
  seats: SeatSpec[]
}

/** 原始 js 侧接口(全部返回 JSON 字符串)。 */
interface RawEngine {
  version(): string
  newMatch(cfgJSON: string): string
  act(seat: number, action: string, payloadJSON: string, since: number): string
  aiStep(since: number): string
  view(seat: number): string
  state(): string
  rules(): string
  tick?(nowMs: number, since: number): string
}

export interface Engine {
  version: string
  newMatch(cfg: MatchConfig): NewMatchReply
  act(seat: number, action: string, payload: unknown, since: number): ActReply
  aiStep(since: number): AiStepReply
  /** 驱动响应/出牌倒计时(旧引擎无 tick 时返回 changed:false)。 */
  tick(since: number, nowMs?: number): TickReply
  view<T>(seat: number): T
  state(): { ok: boolean; seq: number; finished: boolean; seatCount: number }
}

/** 引擎不可用(未构建 wasm、浏览器不支持等)时抛出。 */
export class EngineUnavailableError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'EngineUnavailableError'
  }
}

const ENGINE_BASE = `${import.meta.env.BASE_URL}engine/`
const LOAD_TIMEOUT_MS = 20000

let loading: Promise<Engine> | null = null

/** 加载并初始化单机引擎(进程内单例, 重复调用返回同一 Promise)。 */
export function loadEngine(): Promise<Engine> {
  if (!loading) {
    loading = boot().catch((err: unknown) => {
      // 失败不缓存, 允许用户重试。
      loading = null
      throw err
    })
  }
  return loading
}

/** 仅供测试: 重置单例。 */
export function resetEngineForTest(): void {
  loading = null
}

async function boot(): Promise<Engine> {
  if (typeof WebAssembly !== 'object') {
    throw new EngineUnavailableError('当前浏览器不支持 WebAssembly, 无法启动单机模式')
  }
  // 1. 取 Go 运行时胶水代码并执行, 它会定义 globalThis.Go。
  const glueUrl = `${ENGINE_BASE}wasm_exec.js`
  let glueText: string
  try {
    const res = await fetch(glueUrl)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    glueText = await res.text()
  } catch (err) {
    throw new EngineUnavailableError(
      `单机引擎未就绪(${glueUrl} 不可用): ${String(err)}。请先运行 pnpm run build:wasm`,
    )
  }
  // 胶水代码是普通脚本(非 ESM), 以函数作用域执行。
  new Function(glueText)()

  const GoCtor = (globalThis as unknown as { Go?: new () => GoRuntime }).Go
  if (!GoCtor) {
    throw new EngineUnavailableError('Go 运行时胶水代码执行失败(未定义 Go 构造器)')
  }

  // 2. 实例化 wasm 并启动 Go 运行时(main 永久阻塞, 不 await)。
  const go = new GoCtor()
  const wasmURL = `${ENGINE_BASE}engine.wasm`
  const inst = await instantiateWasm(wasmURL, go.importObject)
  void go.run(inst)

  // 3. 等待 Go 侧把 WoleEngine 挂到全局。
  const raw = await waitForEngine()
  return wrap(raw)
}

interface GoRuntime {
  importObject: WebAssembly.Imports
  run(instance: WebAssembly.Instance): Promise<void>
}

async function instantiateWasm(
  url: string,
  imports: WebAssembly.Imports,
): Promise<WebAssembly.Instance> {
  const res = await fetch(url)
  if (!res.ok) {
    throw new EngineUnavailableError(
      `引擎二进制加载失败(${url} HTTP ${res.status})。请先运行 pnpm run build:wasm`,
    )
  }
  // 优先流式实例化; 服务器 MIME 不正确时退回 ArrayBuffer。
  if (typeof WebAssembly.instantiateStreaming === 'function') {
    try {
      const out = await WebAssembly.instantiateStreaming(res.clone(), imports)
      return out.instance
    } catch {
      /* 落到下面的分支 */
    }
  }
  const buf = await res.arrayBuffer()
  const out = await WebAssembly.instantiate(buf, imports)
  return out.instance
}

function waitForEngine(): Promise<RawEngine> {
  return new Promise((resolve, reject) => {
    const started = Date.now()
    const tick = () => {
      const cand = (globalThis as unknown as { WoleEngine?: RawEngine }).WoleEngine
      if (cand && typeof cand.newMatch === 'function') {
        resolve(cand)
        return
      }
      if (Date.now() - started > LOAD_TIMEOUT_MS) {
        reject(new EngineUnavailableError('引擎启动超时(未挂载 WoleEngine)'))
        return
      }
      setTimeout(tick, 30)
    }
    tick()
  })
}

/** 把"返回 JSON 字符串"的原始接口包装成类型化 API。 */
function wrap(raw: RawEngine): Engine {
  const parse = <T>(s: string, what: string): T => {
    try {
      return JSON.parse(s) as T
    } catch {
      throw new EngineUnavailableError(`引擎返回了不可解析的结果(${what})`)
    }
  }
  return {
    version: raw.version(),
    newMatch(cfg: MatchConfig): NewMatchReply {
      const rules: RulesConfig = { ...DEFAULT_RULES, ...(cfg.rules ?? {}) }
      const payload = {
        rules,
        dealer: cfg.dealer ?? 0,
        seed: cfg.seed ?? 0,
        seats: cfg.seats,
      }
      return parse<NewMatchReply>(raw.newMatch(JSON.stringify(payload)), 'newMatch')
    },
    act(seat, action, payload, since): ActReply {
      const body = JSON.stringify(payload ?? {})
      return parse<ActReply>(raw.act(seat, action, body, since), 'act')
    },
    aiStep(since): AiStepReply {
      return parse<AiStepReply>(raw.aiStep(since), 'aiStep')
    },
    tick(since, nowMs) {
      if (typeof raw.tick !== 'function') {
        return { ok: true, changed: false, seq: since, finished: false, events: [] }
      }
      return parse<TickReply>(raw.tick(nowMs ?? Date.now(), since), 'tick')
    },
    view<T>(seat: number): T {
      const text = raw.view(seat)
      const maybeErr = parse<{ ok?: boolean; error?: string }>(text, 'view')
      if (maybeErr && maybeErr.ok === false) {
        throw new EngineUnavailableError(maybeErr.error ?? '视图不可用')
      }
      return maybeErr as T
    },
    state() {
      return parse<{ ok: boolean; seq: number; finished: boolean; seatCount: number }>(
        raw.state(),
        'state',
      )
    },
  }
}
