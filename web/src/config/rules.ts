/**
 * 规则预设与牌池模型（前端权威定义, 后端 server/internal/game/rules.go 保持同名同义）
 *
 * 设计要点:
 * 1. 预设决定「规则」+「默认牌池」; 用户可以在预设基础上改牌池(种类与每种张数),
 *    规则仍然跟随预设 —— 这是本项目「可完全自定义」的核心诉求。
 * 2. 牌池以「种类 → 张数」表达: 数牌/字牌可用 1/2/3/4/8 张, 花牌每种固定 1 张,
 *    另有通用百搭(0~8)与限定百搭(指定若干种类 + 各自张数)。
 */

/** 联机方式 */
export type TransportMode = 'server' | 'p2p' | 'lan'

/** 一副牌的组成规格 */
export interface TilePoolSpec {
  /** 牌种代码 → 张数 (wan1..wan9 / tiao1..tiao9 / tong1..tong9 / dong..bai / hua1..hua8) */
  kinds: Record<string, number>
  /** 通用百搭张数 0~8 */
  jokers: number
  /** 限定百搭: 牌种代码 → 张数 (这些牌视为百搭, 且仍占牌池) */
  limitedJokers?: Record<string, number>
}

/** 规则开关（预设决定, 创建房间时随房间下发） */
export interface RuleSpec {
  /** 必须 258 做将（万/条/筒的 2、5、8） */
  pair258Only: boolean
  /** 吃牌只能吃上家 */
  chowLeftOnly: boolean
  /** 花牌可作万能牌（济宁「钱牌」用于十三不靠） */
  flowersAsWild: boolean
  /** 牌墙末尾留 N 张不打（淄博「臭牌」N=14） */
  deadWall: number
  /** 报听可撤回 */
  allowWithdrawTing: boolean
  /** 明搂/明楼：亮牌后只能自摸且输赢加倍 */
  mingLou: boolean
}

/** 一套预设 */
export interface RulePreset {
  id: 'jinan' | 'jining' | 'zibo' | 'sd258'
  name: string
  /** 一句话说明（界面展示） */
  summary: string
  /** 默认牌池（不含用户改动） */
  pool: TilePoolSpec
  rules: RuleSpec
  /** 说明条目 */
  notes: string[]
  /** 「更多规则设置」里该预设的默认值（省略则用全局默认） */
  options?: Partial<RuleOptions>
}

/* -------------------------------------------------------- 更多规则设置(阶段10) */

/**
 * 可配置的对局细则, 键名与后端 game.RuleSet 的 json tag 一致。
 *
 * 分两类:
 *   1. 计时: 响应(碰/杠/吃 与 胡分开)、出牌;
 *   2. 优先级与截牌: 胡 > 碰/杠 > 吃 的仲裁是否允许"截胡/截碰"。
 */
export interface RuleOptions {
  /** 碰/杠/吃的响应倒计时(秒), 0 = 不设倒计时(改由下家点「摸牌」推进) */
  respond_meld_sec: number
  /** 荣和(胡)的响应倒计时(秒), 0 = 不设倒计时 */
  respond_win_sec: number
  /** 出牌倒计时(秒), 0 = 不限时 */
  discard_sec: number
  /** 截胡: 多人可胡时, 按出牌顺序由最近的一家先胡(关闭则先按者得) */
  rob_win: boolean
  /** 截碰/杠: 多人可碰杠时, 按出牌顺序优先(关闭则先按者得) */
  rob_meld: boolean
  /** 允许吃牌 */
  chow: boolean
  /** 七对 */
  seven_pairs: boolean
  /** 允许自摸 */
  self_draw: boolean
  /** 允许点炮胡 */
  discard_win: boolean
  /** 抢杠胡(阶段11): 别人补杠时若正好是你要和的牌, 可以抢 */
  rob_kong_win: boolean
  /** 杠上开花(阶段11): 杠后补牌自摸, 加番 */
  kong_flower: boolean
  /** 海底捞月 / 河底捞鱼(阶段11): 牌墙摸完前最后一张自摸 / 最后一张打出的牌被和 */
  last_tile: boolean
  /** 底分 */
  base_score: number
}

/** 与后端 DefaultRuleSet() 对齐的默认细则。 */
export const DEFAULT_RULE_OPTIONS: RuleOptions = {
  respond_meld_sec: 3,
  respond_win_sec: 5,
  discard_sec: 0,
  rob_win: true,
  rob_meld: true,
  chow: true,
  seven_pairs: true,
  self_draw: true,
  discard_win: true,
  rob_kong_win: true,
  kong_flower: true,
  last_tile: true,
  base_score: 1,
}

/** 预设对应的细则默认值(预设未覆盖的字段用全局默认)。 */
export function presetRuleOptions(preset: RulePreset): RuleOptions {
  return { ...DEFAULT_RULE_OPTIONS, ...(preset.options ?? {}) }
}

/* ---------------------------------------------------------------- 牌种目录 */

export const SUIT_KINDS: Record<'wan' | 'tiao' | 'tong', string[]> = {
  wan: ['wan1', 'wan2', 'wan3', 'wan4', 'wan5', 'wan6', 'wan7', 'wan8', 'wan9'],
  tiao: ['tiao1', 'tiao2', 'tiao3', 'tiao4', 'tiao5', 'tiao6', 'tiao7', 'tiao8', 'tiao9'],
  tong: ['tong1', 'tong2', 'tong3', 'tong4', 'tong5', 'tong6', 'tong7', 'tong8', 'tong9'],
}

export const HONOR_KINDS = ['dong', 'nan', 'xi', 'bei', 'zhong', 'fa', 'bai'] as const

export const FLOWER_KINDS = [
  'hua1', 'hua2', 'hua3', 'hua4', 'hua5', 'hua6', 'hua7', 'hua8',
] as const

export const FLOWER_LABELS: Record<string, string> = {
  hua1: '春', hua2: '夏', hua3: '秋', hua4: '冬',
  hua5: '梅', hua6: '兰', hua7: '竹', hua8: '菊',
}

export const HONOR_LABELS: Record<string, string> = {
  dong: '东', nan: '南', xi: '西', bei: '北', zhong: '中', fa: '发', bai: '白',
}

/** 数牌/字牌允许的张数档位（引擎每种最多 4 张） */
export const COUNT_OPTIONS = [1, 2, 3, 4] as const

/** 全部 34 种常规牌（数牌 27 + 字牌 7） */
export const STANDARD_KINDS: string[] = [
  ...SUIT_KINDS.wan, ...SUIT_KINDS.tiao, ...SUIT_KINDS.tong, ...HONOR_KINDS,
]

/* ---------------------------------------------------------------- 牌池工具 */

/** 标准 34 种 + 可选花牌, 各 count 张 */
export function buildPool(opts: {
  suits?: Array<'wan' | 'tiao' | 'tong'>
  honors?: boolean
  count?: number
  flowers?: boolean
  jokers?: number
}): TilePoolSpec {
  const { suits = ['wan', 'tiao', 'tong'], honors = true, count = 4, flowers = false, jokers = 0 } = opts
  const kinds: Record<string, number> = {}
  for (const s of suits) for (const k of SUIT_KINDS[s]) kinds[k] = count
  if (honors) for (const k of HONOR_KINDS) kinds[k] = count
  if (flowers) for (const k of FLOWER_KINDS) kinds[k] = 1
  return { kinds, jokers }
}

/** 牌池总张数 */
export function poolTotal(pool: TilePoolSpec): number {
  let n = 0
  for (const v of Object.values(pool.kinds)) n += v
  n += pool.jokers || 0
  return n
}

/** 牌池摘要: 数牌/字牌/花牌/百搭 各自张数 */
export function poolSummary(pool: TilePoolSpec): {
  suits: number
  honors: number
  flowers: number
  jokers: number
  total: number
} {
  let suits = 0
  let honors = 0
  let flowers = 0
  for (const [k, v] of Object.entries(pool.kinds)) {
    if (k.startsWith('hua')) flowers += v
    else if ((HONOR_KINDS as readonly string[]).includes(k)) honors += v
    else suits += v
  }
  const jokers = pool.jokers || 0
  return { suits, honors, flowers, jokers, total: suits + honors + flowers + jokers }
}

/** 深拷贝牌池 */
export function clonePool(pool: TilePoolSpec): TilePoolSpec {
  return {
    kinds: { ...pool.kinds },
    jokers: pool.jokers || 0,
    limitedJokers: pool.limitedJokers ? { ...pool.limitedJokers } : undefined,
  }
}

/* ---------------------------------------------------------------- 预设定义 */

/** 济南麻将: 136 张(数牌+字牌各 4), 必须 258 做将, 可吃碰杠, 有「明搂」 */
const JINAN_POOL = buildPool({ flowers: false, count: 4 })

/** 济宁麻将: 136 张, 吃仅上家, 花牌万能(待阶段8 花牌上线后启用) */
const JINING_POOL = buildPool({ flowers: false, count: 4 })

/** 淄博麻将: 136 张(无花), 牌墙末尾 14 张作「臭牌」不打, 明楼 + 报听可撤 */
const ZIBO_POOL = buildPool({ flowers: false, count: 4 })

/** 山东二五八: 136 张, 258 做将, 只能吃上家, 碰杠不限 */
const SD258_POOL = buildPool({ flowers: false, count: 4 })

const BASE_RULES: RuleSpec = {
  pair258Only: false,
  chowLeftOnly: false,
  flowersAsWild: false,
  deadWall: 0,
  allowWithdrawTing: false,
  mingLou: false,
}

export const RULE_PRESETS: RulePreset[] = [
  {
    id: 'jinan',
    name: '济南麻将',
    summary: '136 张（数牌+字牌各 4）· 必须 258 做将 · 可吃碰杠 · 明搂',
    pool: JINAN_POOL,
    rules: { ...BASE_RULES, pair258Only: true, mingLou: true },
    notes: [
      '用牌：136 张（万/条/筒 + 东南西北中发白，各 4 张）；花牌待后续版本上线',
      '核心规则：必须用 258（万/条/筒的 2、5、8）做将；可吃、碰、杠',
      '明搂：亮牌后只能自摸，输赢双倍',
    ],
  },
  {
    id: 'jining',
    name: '济宁麻将',
    summary: '136 张 · 吃仅上家 · 优先级 胡 > 杠 > 碰 > 吃',
    pool: JINING_POOL,
    rules: { ...BASE_RULES, flowersAsWild: true, chowLeftOnly: true },
    notes: [
      '用牌：136 张（数牌 + 字牌各 4 张）',
      '核心规则：吃牌仅限上家；优先级 胡 > 杠 > 碰 > 吃',
      '济宁「钱牌」花牌玩法待阶段8 花牌上线后启用',
    ],
  },
  {
    id: 'zibo',
    name: '淄博麻将',
    summary: '136 张（无花牌）· 末尾留 14 张臭牌 · 报听可撤',
    pool: ZIBO_POOL,
    rules: { ...BASE_RULES, deadWall: 14, allowWithdrawTing: true, mingLou: true },
    notes: [
      '用牌：136 张（去掉花牌）',
      '牌局会剩下最后 14 张作为「臭牌」不使用',
      '特色：明楼；报听后允许撤回（灵活性更高）',
    ],
  },
  {
    id: 'sd258',
    name: '山东二五八',
    summary: '136 张 · 258 做将 · 只能吃上家 · 碰杠不限',
    pool: SD258_POOL,
    rules: { ...BASE_RULES, pair258Only: true, chowLeftOnly: true },
    notes: [
      '用牌：136 张（数牌 + 字牌各 4 张，无花牌）',
      '核心：258 做将，其余由顺子或刻子构成',
      '只能吃上家的牌，碰、杠不限',
    ],
  },
]

export function findPreset(id: string): RulePreset {
  return RULE_PRESETS.find((p) => p.id === id) ?? RULE_PRESETS[0]!
}

/** 可选人数 */
export const PLAYER_COUNTS = [2, 3, 4] as const

/** 联机方式选项 */
export const TRANSPORT_OPTIONS: Array<{ id: TransportMode; label: string; hint: string }> = [
  { id: 'server', label: '在线服务器', hint: '经服务器中转，最稳，适合异地朋友' },
  { id: 'p2p', label: 'P2P', hint: 'WebRTC 直连（STUN 穿透），房主本地权威' },
  { id: 'lan', label: '局域网', hint: '同网段直连，不走公网、不依赖 STUN' },
]
