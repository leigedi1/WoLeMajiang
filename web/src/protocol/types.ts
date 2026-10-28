/**
 * 协议常量与类型: 与服务端 internal/protocol 一一对应(线缆稳定)。
 * 所有 WS 消息均为统一信封: {v, seq?, ref?, type, payload?}
 */

export const PROTOCOL_VERSION = 1

export interface Envelope {
  v: number
  /** 服务端事件序列号(房间内单调递增), 用于断线重连增量同步 */
  seq?: number
  /** 客户端请求引用, 服务端在错误事件中回显 */
  ref?: string
  type: string
  payload?: unknown
}

/** 客户端上行操作类型 */
export const Op = {
  Hello: 'guest.hello',
  Ping: 'ping',
  Resync: 'sync.resync',
  RoomCreate: 'room.create',
  RoomJoin: 'room.join',
  RoomLeave: 'room.leave',
  GameStart: 'game.start',
  GameAction: 'game.action',
  // 阶段5: 大厅、座位、邀请
  RoomList: 'room.list',
  RoomUpdate: 'room.update',
  RoomDissolve: 'room.dissolve',
  SeatReady: 'seat.ready',
  SeatBot: 'seat.bot',
  InviteCreate: 'invite.create',
  InviteList: 'invite.list',
  InviteRevoke: 'invite.revoke',
  InviteJoin: 'invite.join',
} as const

/** 服务端下行事件类型 */
export const Ev = {
  Hello: 'hello',
  Pong: 'pong',
  RoomState: 'room.state',
  GameEvent: 'game.event',
  Error: 'error',
  // 阶段5
  RoomList: 'room.list',
  InviteCreated: 'invite.created',
  InviteList: 'invite.list',
} as const

export interface HelloIn {
  guestId: string
  token: string
}

export interface HelloOut {
  guestId: string
  nickname: string
}

export interface ErrorOut {
  code: string
  message: string
}

export interface ResyncIn {
  lastSeq: number
}

/** 其他玩家的可见投影: 永远不含手牌内容 */
export interface PlayerView {
  guestId: string
  nickname: string
  seat: number
  connected: boolean
  tileCount: number
  /** 准备状态(仅大厅阶段有意义) */
  ready?: boolean
  /** 服务端 AI 座位 */
  isBot?: boolean
  /** 房主 */
  isHost?: boolean
}

/** 自己的私有视图: 仅下发到本人客户端 */
export interface YouView {
  guestId: string
  seat: number
  hand?: string[]
}

/** 房间状态投影 */
export interface RoomStateView {
  roomId: string
  code: string
  mode: string
  status: string
  theme: string
  /** 房间名 */
  name?: string
  /** 是否公开(出现在大厅) */
  isPublic?: boolean
  /** 是否设置房间密码(永不回传哈希) */
  hasPassword?: boolean
  /** 开局后规则锁定 */
  rulesLocked?: boolean
  /** 规则人数容量(2~4, 阶段5 实现为 4) */
  capacity?: number
  seq: number
  /** 阶段7: 主机任期, 每次房主变更 +1(客户端据此识别房主转移) */
  hostEpoch?: number
  players: PlayerView[]
  you?: YouView
  /** 阶段10: 本局规则细则(计时/截牌/和牌条件), 服务端房间才有 */
  rules?: RulesView
}

/** 规则细则投影(阶段10) */
export interface RulesView {
  players: number
  chow: boolean
  seven_pairs: boolean
  self_draw: boolean
  discard_win: boolean
  base_score: number
  respond_meld_sec: number
  respond_win_sec: number
  discard_sec: number
  rob_win: boolean
  rob_meld: boolean
  /** 抢杠胡(阶段11) */
  rob_kong_win: boolean
  /** 杠上开花(阶段11) */
  kong_flower: boolean
  /** 海底捞月 / 河底捞鱼(阶段11) */
  last_tile: boolean
}

/** 公开大厅条目 */
export interface LobbyRoomView {
  code: string
  name: string
  status: string
  theme: string
  players: number
  capacity: number
  hasPassword: boolean
  hostNickname: string
  updatedAt: number
}

/** 大厅快照负载 */
export interface LobbyListView {
  rooms: LobbyRoomView[]
  onlineRooms: number
}

/** 邀请可见信息(不含令牌明文) */
export interface InviteView {
  id: string
  hasPassword: boolean
  bypassRoomPassword: boolean
  maxUses: number
  uses: number
  revoked: boolean
  expiresAt: number
  createdAt: number
}

/** 邀请创建结果: token 明文仅此一次下发 */
export interface InviteCreatedView {
  token: string
  invite: InviteView
}

/** 副露投影 */
export interface MeldView {
  kind: string
  tiles: string[]
}

/** 和牌分解投影 */
export interface WinShapeView {
  kind: string
  melds?: string[][]
  pair?: string
}

/** 结算投影 */
export interface ResultView {
  winner: number
  byDraw: boolean
  loser?: number
  fan: number
  pay: number
  winKind?: string
  shape?: WinShapeView
  scores: number[]
  /** 番种明细(阶段11): 和牌/七对子/自摸/门清/抢杠胡/杠上开花/海底捞月/河底捞鱼 */
  flags?: string[]
}

/** 对局座位视角投影(引擎 SeatView 的线缆形状) */
export interface SeatView {
  seat: number
  phase: string
  turnSeat: number
  turnStage: string // draw | discard | respond
  wallLeft: number
  hand: string[] // 仅本人
  drawn?: string // 仅本人: 本回合摸的牌
  melds: MeldView[] // 本人副露
  discards: string[][] // 全员河牌
  /** 全局出牌流水(按时间顺序), 手机端显示「最近 N 张」与历史面板 */
  discardLog?: { seat: number; tile: string }[]
  othersHandCounts: number[]
  othersMelds: MeldView[][]
  actions: string[] | null
  pendingTile?: string
  pendingBy?: number
  /** 响应倒计时剩余秒数(仅本人处于响应窗口且启用了倒计时) */
  respondSecLeft?: number
  /** 响应截止时间(Unix 毫秒), 供前端做本地平滑倒计时 */
  respondDeadline?: number
  /** 出牌倒计时剩余秒数(仅本人待出牌且启用了倒计时) */
  discardSecLeft?: number
  /** 出牌截止时间(Unix 毫秒) */
  discardDeadline?: number
  /** 四家判定状态: '' | 'pending'(判定中) | 'success'(成功) | 'failed'(失败) */
  claimStates?: string[]
  /** 未启用响应倒计时时, 出牌者下家可用「摸牌」收敛窗口 */
  forceDraw?: boolean
  result?: ResultView
}

/** 对局事件负载 */
export interface GameEventOut {
  kind: string
  seat?: number
  tiles?: string[]
  view?: SeatView | null
}

/** 编码一条上行信封为 JSON 字符串 */
export function encode(e: Envelope): string {
  const { v, ...rest } = e
  return JSON.stringify({ ...rest, v: v ?? PROTOCOL_VERSION })
}

/** 解析下行信封, 校验协议版本与类型 */
export function decode(data: string): Envelope {
  const parsed = JSON.parse(data) as Partial<Envelope>
  if (parsed.v !== PROTOCOL_VERSION) {
    throw new Error(`协议版本不支持: ${parsed.v}(当前 ${PROTOCOL_VERSION})`)
  }
  if (!parsed.type) {
    throw new Error('消息 type 不能为空')
  }
  return parsed as Envelope
}

/** 构造一条带自增引用编号的信封 */
export function makeEnvelope(type: string, payload?: unknown, ref?: string): Envelope {
  return { v: PROTOCOL_VERSION, type, payload, ref }
}
