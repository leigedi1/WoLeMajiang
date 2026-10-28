<script setup lang="ts">
/**
 * 应用外壳：极简三态
 *   1) home    —— 只要求输入名字；输入后出现「创建房间 / 加入房间」
 *   2) create  —— 创建房间设置（核心界面：预设 + 人数 + 联机方式 + 自定义牌池）
 *   3) room    —— 等待房间（房间码 / 邀请链接 / 满员后房主开始）
 *   4) table   —— 对局与结算
 *
 * 不包含单机、机器人、大厅。联机方式支持 在线服务器 / P2P / 局域网。
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useIdentityStore } from './stores/identity'
import { useConnectionStore } from './stores/connection'
import { useMatchStore } from './stores/match'
import { useThemeStore } from './stores/theme'
import { useWebRTCStore } from './stores/webrtc'
import { useDIYStore } from './stores/diy'
import { diyAuth, diySetPassword, resolveRoom } from './api/client'
import {
  sfxDiscard,
  sfxDraw,
  sfxDrawWall,
  sfxKong,
  sfxMeld,
  sfxPass,
  sfxWin,
  sfxYourTurn,
  setAudioEnabled,
  unlockAudio,
} from './utils/sfx'
import { findPreset, TRANSPORT_OPTIONS, type RuleOptions, type TilePoolSpec, type TransportMode } from './config/rules'

import ThemeSwitcher from './components/ThemeSwitcher.vue'
import CreateRoomScreen from './components/CreateRoomScreen.vue'
import WaitingRoom from './components/WaitingRoom.vue'
import MahjongTable from './components/MahjongTable.vue'
import GameResultModal from './components/GameResultModal.vue'
import DIYThemeSettings from './components/DIYThemeSettings.vue'

const identity = useIdentityStore()
const conn = useConnectionStore()
const match = useMatchStore()
const themeStore = useThemeStore()
const wrtc = useWebRTCStore()
const diy = useDIYStore()

type Screen = 'home' | 'create' | 'join' | 'room' | 'table' | 'diy'
const screen = ref<Screen>('home')

const nicknameInput = ref('')
const nameConfirmed = ref(false)
const joinCodeInput = ref('')
const joinPassword = ref('')
const busy = ref(false)
const pageError = ref('')
let errorTimer: ReturnType<typeof setTimeout> | undefined
const showResultModal = ref(true)
/** 玩家主动「回房间」后, 不要再被 match.view 自动拉回牌桌(否则邀请界面一闪而过) */
const leftTable = ref(false)
/** 玩家主动按返回键回到首页: 之后即使收到房间广播也不要被拽回房间 */
const stayOnHome = ref(false)
/** 全屏状态: 全屏时隐藏顶栏(房间名/主题/动效/按钮), 把牌桌铺满整个屏幕 */
const isFullscreen = ref(false)

/** 房主创建房间时选的联机方式/玩法, 用于等待界面展示 */
const transport = ref<TransportMode>('server')
const presetId = ref('jinan')
const poolTotal = ref(0)

/* ---------------------------------------------------- 阶段7: 会话内房间记忆 */

const SESSION_ROOM_KEY = 'majiang.session.room'

interface RoomHint {
  code: string
  name?: string
  transport: TransportMode
  password?: string
}

/**
 * 记住"我上次在哪个房间"。放 sessionStorage 而不是 localStorage:
 * 同一次页面会话里刷新/返回都应该能回来, 但新开浏览器不该打扰用户。
 */
function loadRoomHint(): RoomHint | null {
  try {
    const raw = sessionStorage.getItem(SESSION_ROOM_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<RoomHint>
    if (!parsed || typeof parsed.code !== 'string' || !parsed.code) return null
    return {
      code: parsed.code,
      name: typeof parsed.name === 'string' ? parsed.name : undefined,
      transport: parsed.transport === 'p2p' || parsed.transport === 'lan' ? parsed.transport : 'server',
      password: typeof parsed.password === 'string' ? parsed.password : undefined,
    }
  } catch {
    return null
  }
}

function persistRoomHint(hint: RoomHint | null): void {
  try {
    if (hint) sessionStorage.setItem(SESSION_ROOM_KEY, JSON.stringify(hint))
    else sessionStorage.removeItem(SESSION_ROOM_KEY)
  } catch {
    /* storage unavailable */
  }
}

/** 上次所在的房间(用于首页「继续对局」和刷新后自动回房) */
const savedRoom = ref<RoomHint | null>(loadRoomHint())
/** 自动回房正在进行: 避免首页闪现「继续对局」又跳走 */
const resuming = ref(false)

/** 房主迁移提示(阶段7): 房主换人时给所有人一个明确反馈 */
const hostNotice = ref('')
let hostNoticeTimer: ReturnType<typeof setTimeout> | undefined

function notifyHost(message: string): void {
  hostNotice.value = message
  if (hostNoticeTimer) clearTimeout(hostNoticeTimer)
  hostNoticeTimer = setTimeout(() => {
    hostNotice.value = ''
  }, 6000)
}

const transportLabel = computed(
  () => TRANSPORT_OPTIONS.find((t) => t.id === transport.value)?.label ?? '在线服务器',
)
const presetName = computed(() => findPreset(presetId.value).name)
const myGuestId = computed(() => identity.guestId || '')
const isHost = computed(() => {
  const room = conn.room
  if (!room) return false
  const me = (room.players ?? []).find((p) => p.guestId === myGuestId.value)
  // 服务端会显式下发 isHost(房主中途离开时会转移房主), 老数据回退到 0 号座位。
  if (me && typeof me.isHost === 'boolean') return me.isHost
  return !!me && (me.seat ?? -1) === 0
})
/** 当前房间是不是我当房主的 P2P/局域网房: 这类房间必须保持页面在前台 */
const isP2PHost = computed(() => transport.value !== 'server' && isHost.value)
/** 房间里是否还有未结束的对局(用于显示「回到牌桌」) */
const hasLiveMatch = computed(() => conn.room?.status === 'playing' || !!match.view)

/* ---------------------------------------------------------------- 生命周期 */

let pingTimer: number | undefined

function syncFullscreenState(): void {
  const doc = document as Document & { webkitFullscreenElement?: Element | null }
  isFullscreen.value = !!(document.fullscreenElement ?? doc.webkitFullscreenElement)
}

/** 移动端切后台/切网回来: 把信令补上, 房间不至于「消失」 */
function onPageVisible(): void {
  if (document.visibilityState !== 'visible') return
  wrtc.ensureSignaling()
  if (isP2PHost.value && (screen.value === 'room' || screen.value === 'table')) {
    void wrtc.requestWakeLock()
  }
}

onMounted(async () => {
  // 有本地身份就先恢复(失败的令牌会被清掉, 回到未就绪)
  if (!identity.hasIdentity) {
    try {
      await identity.restore()
    } catch {
      /* 忽略: 走重新创建流程 */
    }
  }
  if (identity.nickname) {
    nicknameInput.value = identity.nickname
    nameConfirmed.value = true
  }
  // 邀请/房间链接: ?join=CODE
  const code = new URLSearchParams(location.search).get('join')
  if (code) {
    joinCodeInput.value = code.trim().toUpperCase()
    screen.value = 'join'
  }
  pingTimer = window.setInterval(() => {
    if (conn.status === 'connected') conn.ping()
  }, 20_000)
  // 启动就拉一次 DIY 牌面: 玩家可能直接选了 DIY 主题打牌,
  // 不预先加载的话牌桌只能退回经典图案(所有玩家看到同一套自定义图, 所以要拉服务端)。
  void diy.refresh()
  // 阶段7: 刷新/返回后自动尝试回到上次的房间(不记得就什么都不做)。
  void tryResumeSession()
  setAudioEnabled(themeStore.soundEnabled)
  document.addEventListener('fullscreenchange', syncFullscreenState)
  document.addEventListener('webkitfullscreenchange', syncFullscreenState)
  document.addEventListener('visibilitychange', onPageVisible)
  window.addEventListener('online', onPageVisible)
  window.addEventListener('popstate', onPopState)
  // 浏览器要求音频在用户手势后启动: 第一次点击任意位置时解锁。
  window.addEventListener('pointerdown', onFirstPointer, { once: true })
  syncFullscreenState()
})

onUnmounted(() => {
  if (pingTimer) window.clearInterval(pingTimer)
  if (hostNoticeTimer) clearTimeout(hostNoticeTimer)
  if (errorTimer) clearTimeout(errorTimer)
  document.removeEventListener('fullscreenchange', syncFullscreenState)
  document.removeEventListener('webkitfullscreenchange', syncFullscreenState)
  document.removeEventListener('visibilitychange', onPageVisible)
  window.removeEventListener('online', onPageVisible)
  window.removeEventListener('popstate', onPopState)
  window.removeEventListener('pointerdown', onFirstPointer)
  void wrtc.releaseWakeLock()
})

function onFirstPointer(): void {
  if (themeStore.soundEnabled) unlockAudio()
}

/** 退出全屏(离开牌桌时必须复位, 否则下一个界面还是横屏全屏) */
async function exitFullscreenIfAny(): Promise<void> {
  try {
    const doc = document as Document & {
      webkitFullscreenElement?: Element | null
      webkitExitFullscreen?: () => Promise<void>
    }
    if (document.fullscreenElement) await document.exitFullscreen()
    else if (doc.webkitFullscreenElement) await doc.webkitExitFullscreen?.()
    const orientation = window.screen.orientation as ScreenOrientation & { unlock?: () => void }
    orientation?.unlock?.()
  } catch {
    /* 用户手势缺失等情况忽略 */
  } finally {
    syncFullscreenState()
  }
}

/* ---------------------------------------------------------------- 状态联动 */

watch(
  () => conn.room,
  (room) => {
    if (room) {
      themeStore.setRoomThemes({ defaultTheme: room.theme })
      // 注意: 首页不能被"房间状态广播"拽回房间。
      // 玩家按返回键回首页时是主动离开牌桌的, 这时服务端每个 room.state
      // 都是新对象, 若照旧自动切回 room, 返回键就等于没反应。
      const explicit = screen.value === 'create' || screen.value === 'join'
      if (explicit || (screen.value === 'home' && !stayOnHome.value)) {
        screen.value = 'room'
      }
    } else {
      themeStore.clearRoomThemes()
      if (screen.value === 'room' || screen.value === 'table') screen.value = 'home'
    }
  },
)

watch(
  () => match.view,
  (view) => {
    // 玩家主动「回房间」后不要再被自动弹回牌桌(邀请界面需要停留);
    // 真正回到牌桌由「回到牌桌」按钮或新一局开始触发。
    if (view && !leftTable.value) screen.value = 'table'
  },
)

// 房主开新一局(room.status → playing)时, 所有人都回到牌桌。
watch(
  () => conn.room?.status,
  (status, prev) => {
    if (status === 'playing' && prev !== 'playing') {
      leftTable.value = false
      if (match.view) screen.value = 'table'
    }
  },
)

// P2P/局域网房主: 保持屏幕常亮, 否则手机锁屏 = 房间掉线。
watch(
  [() => screen.value, () => isP2PHost.value],
  ([current, hosting]) => {
    if (hosting && (current === 'room' || current === 'table')) void wrtc.requestWakeLock()
    else void wrtc.releaseWakeLock()
  },
  { immediate: true },
)

watch(
  () => match.isFinished,
  (finished) => {
    if (finished) {
      showResultModal.value = true
    } else if (match.view) {
      // 房主开始了下一局: 自动收起上一局的结算弹窗, 直接回到牌桌。
      showResultModal.value = false
    }
  },
)

watch(
  () => conn.lastError,
  (err) => {
    if (!err) return
    pageError.value = typeof err === 'string' ? err : (err as { message?: string }).message ?? ''
    // 提示 5 秒后自动消失, 避免旧错误一直挂在界面上。
    if (errorTimer) clearTimeout(errorTimer)
    errorTimer = setTimeout(() => {
      pageError.value = ''
    }, 5000)
  },
)

/* ---------------------------------------- 阶段7: 回房 / 返回键 / 房主迁移 / 音效 */

/**
 * 尝试回到上次的房间。
 *
 * 服务端在 WS 握手后会用 `FindByGuest` 找回会话并把 room.state 推回来
 * (见 server/internal/httpapi/ws.go 的 readLoop), 所以客户端只要重连就够了;
 * 连不上或者房间已经没了, 就安静地把记忆清掉, 不要弹错误吓人。
 */
async function tryResumeSession(): Promise<void> {
  const hint = savedRoom.value
  if (!hint) return
  if (!identity.hasIdentity) {
    // 没有身份就不可能还在房间里(服务端按 guestId 认人)。
    savedRoom.value = null
    persistRoomHint(null)
    return
  }
  if (hint.transport !== 'server') {
    // P2P 房间跑在房主浏览器里, 房主不在时无法自行恢复, 保留提示让玩家手动处理。
    return
  }
  resuming.value = true
  transport.value = 'server'
  try {
    await conn.connect(identity.guestId, identity.token, true, 'ws')
    // 给服务端一点时间推 room.state; 没推回来说明房间已经不存在了。
    await new Promise((resolve) => setTimeout(resolve, 800))
    if (!conn.room) {
      savedRoom.value = null
      persistRoomHint(null)
    }
  } catch {
    savedRoom.value = null
    persistRoomHint(null)
  } finally {
    resuming.value = false
  }
}

/** 首页「继续对局」: 有房间记忆就重连并回房 */
async function onResumeSession(): Promise<void> {
  if (resuming.value) return
  stayOnHome.value = false
  if (conn.room) {
    leftTable.value = false
    screen.value = match.view ? 'table' : 'room'
    return
  }
  leftTable.value = false
  await tryResumeSession()
}

/** 首页「离开上次的房间」: 真正退房, 让出座位给别人 */
async function onAbandonSession(): Promise<void> {
  const hint = savedRoom.value
  pageError.value = ''
  try {
    if (hint && hint.transport === 'server' && identity.hasIdentity) {
      if (conn.status !== 'connected') {
        await conn.connect(identity.guestId, identity.token, true, 'ws').catch(() => undefined)
      }
      conn.leaveRoom()
      await new Promise((resolve) => setTimeout(resolve, 200))
      conn.disconnect()
    }
  } finally {
    match.clear()
    savedRoom.value = null
    persistRoomHint(null)
    screen.value = 'home'
  }
}

/** P2P 房主消失后的确认: 这局已经不可能恢复, 清干净回首页。 */
function onHostLostAck(): void {
  conn.hostLost = false
  conn.lastError = null
  try {
    conn.disconnect()
  } catch {
    /* 已经断开 */
  }
  match.clear()
  savedRoom.value = null
  persistRoomHint(null)
  screen.value = 'home'
  pageError.value = ''
}

/**
 * 返回键守卫(阶段7 的核心修复)。
 *
 * 以前全仓库没有任何 popstate 处理, 手机上按一下返回键就直接离开 SPA,
 * 房间还在、座位还占着, 玩家却再也回不来了。
 * 现在的语义:
 *   - 牌桌上按返回 → 回等待房间(座位、对局、房主身份全部保留);
 *   - 等待房间按返回 → 回首页, 但**不退出房间**, 首页会出现「继续对局」;
 *   - 首页再按返回 → 交给浏览器(真要离开请用「离开房间」, 那才会让座)。
 */
let backGuardArmed = false

function armBackGuard(): void {
  if (backGuardArmed) return
  backGuardArmed = true
  try {
    history.pushState({ majiang: 'room' }, '')
  } catch {
    /* 某些环境禁止 pushState, 退化为无守卫 */
  }
}

function onPopState(): void {
  backGuardArmed = false
  if (screen.value === 'table') {
    onBackToWaiting()
    armBackGuard()
    return
  }
  if (screen.value === 'room' || screen.value === 'create' || screen.value === 'join' || screen.value === 'diy') {
    showResultModal.value = false
    // 主动回首页: 标记"离开牌桌", 否则对局中收到下一个 game.event 时
    // match.view 变了会把玩家又弹回牌桌, 返回键等于没反应。
    leftTable.value = true
    stayOnHome.value = true
    screen.value = 'home'
    if (conn.room) {
      notifyHost('已回到首页，房间仍为你保留 —— 点「继续对局」随时回来')
    }
  }
}

// 进入房间/牌桌时武装返回键守卫
watch(
  () => screen.value,
  (value) => {
    if (value === 'room' || value === 'table') armBackGuard()
  },
)

// 进房就记住房间, 离房就忘掉(写入 sessionStorage, 刷新后仍能恢复)
watch(
  () => conn.room,
  (room) => {
    if (room) {
      savedRoom.value = {
        code: room.code,
        name: room.name,
        transport: transport.value,
      }
      persistRoomHint(savedRoom.value)
    } else if (!resuming.value) {
      savedRoom.value = null
      persistRoomHint(null)
    }
  },
)

// 房主迁移提示: 只有 hostEpoch 真的变化时才提示, 避免每次 room.state 都弹
watch(
  () => conn.room?.hostEpoch ?? 0,
  (epoch, prev) => {
    if (!conn.room || epoch === prev || epoch <= 0) return
    const host = conn.room.players.find((p) => p.isHost)
    notifyHost(
      isHost.value
        ? '原房主已离开，你已成为新房主（可以开局 / 添加机器人）'
        : `房主已转交给 ${host?.nickname || '其他玩家'}`,
    )
  },
)

// 音效: 按引擎事件播放(声音全部现场合成, 不加载资源)
watch(
  () => match.lastEvent?.tick,
  () => {
    const ev = match.lastEvent
    if (!ev || !themeStore.soundEnabled) return
    switch (ev.kind) {
      case 'discard':
        sfxDiscard()
        break
      case 'draw':
        // 摸牌事件带牌内容时才是自己摸的(别人的摸牌不可见, 不放声音)
        if (ev.tiles && ev.tiles.length > 0 && ev.seat === match.view?.seat) sfxDraw()
        break
      case 'chow':
      case 'pong':
        sfxMeld()
        break
      case 'kong':
        sfxKong()
        break
      case 'win':
        sfxWin()
        break
      case 'draw_wall':
        sfxDrawWall()
        break
      case 'pass':
        sfxPass()
        break
      default:
        break
    }
  },
)

// 轮到自己: 从"没有可选动作"变成"有可选动作"时轻响一声
watch(
  () => match.myActions.length,
  (len, prev) => {
    if (!themeStore.soundEnabled) return
    if (len > 0 && prev === 0 && screen.value === 'table') sfxYourTurn()
  },
)

// 主题 store 的音效开关变化时同步给音效引擎
watch(
  () => themeStore.soundEnabled,
  (on) => setAudioEnabled(on),
)

/* ---------------------------------------------------------------- 动作 */

function confirmName(): void {
  const name = nicknameInput.value.trim()
  if (!name) {
    pageError.value = '请先输入名字'
    return
  }
  pageError.value = ''
  identity.nickname = name
  nameConfirmed.value = true
}

/** 确保有可用身份: 在线服务器走后端注册; P2P/局域网可完全离线(本地生成 guestId) */
async function ensureIdentity(name: string, offline: boolean): Promise<void> {
  if (identity.hasIdentity && identity.nickname === name) return
  if (offline) {
    identity.adoptLocal(name)
    return
  }
  await identity.register(name)
}

async function connectTransport(mode: TransportMode, roomCode?: string): Promise<void> {
  const guestId = identity.guestId
  const token = identity.token
  if (mode === 'server') {
    await conn.connect(guestId, token, true, 'ws')
    return
  }
  // P2P / 局域网: 房主先开信令房间, 加入者先加入信令房间并打通 DataChannel
  if (roomCode) {
    await wrtc.joinP2PRoom(roomCode, joinPassword.value || '')
    await wrtc.connectToHost()
  } else {
    await wrtc.createP2PRoom('', '', { lanOnly: mode === 'lan' })
  }
  await conn.connect(guestId, token, true, 'p2p')
}

async function onCreateRoom(opts: {
  name: string
  password: string
  players: number
  presetId: string
  transport: TransportMode
  pool: TilePoolSpec
  rules: RuleOptions
}): Promise<void> {
  if (busy.value) return
  busy.value = true
  pageError.value = ''
  try {
    const name = identity.nickname || nicknameInput.value.trim() || '我'
    await ensureIdentity(name, opts.transport !== 'server')
    transport.value = opts.transport
    presetId.value = opts.presetId
    poolTotal.value = Object.values(opts.pool.kinds).reduce((a, b) => a + b, 0) + (opts.pool.jokers || 0)
    await connectTransport(opts.transport)
    conn.createRoom({
      name: opts.name,
      password: opts.password,
      public: false,
      theme: themeStore.currentTheme,
      players: opts.players,
      presetId: opts.presetId,
      pool: opts.pool.kinds,
      // 阶段10 细则(计时/截牌/和牌条件)随房间下发, 服务端逐字段合并到默认规则。
      ruleSet: {
        players: opts.players,
        chow: opts.rules.chow,
        seven_pairs: opts.rules.seven_pairs,
        self_draw: opts.rules.self_draw,
        discard_win: opts.rules.discard_win,
        base_score: opts.rules.base_score,
        respond_meld_sec: opts.rules.respond_meld_sec,
        respond_win_sec: opts.rules.respond_win_sec,
        discard_sec: opts.rules.discard_sec,
        rob_win: opts.rules.rob_win,
        rob_meld: opts.rules.rob_meld,
        // 阶段11 时序番种(抢杠胡 / 杠上开花 / 海底捞月·河底捞鱼)
        rob_kong_win: opts.rules.rob_kong_win,
        kong_flower: opts.rules.kong_flower,
        last_tile: opts.rules.last_tile,
      },
    })
    stayOnHome.value = false
    screen.value = 'room'
  } catch (err) {
    pageError.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}

async function onJoinRoom(): Promise<void> {
  const code = joinCodeInput.value.trim().toUpperCase()
  const name = nicknameInput.value.trim()
  if (!code) {
    pageError.value = '请输入房间码'
    return
  }
  if (!name) {
    pageError.value = '请先输入名字'
    return
  }
  if (busy.value) return
  busy.value = true
  pageError.value = ''
  try {
    // 先按房间码判定联机方式: 在线托管房间走 server; P2P/局域网房间走 WebRTC
    // (局域网房主在加入端同样走 WebRTC, 同网段会自动用主机候选直连)。
    const info = await resolveRoom(code)
    const mode: TransportMode = info.transport === 'server' ? 'server' : 'p2p'
    // P2P 房间的信令已经没了房主(手机切后台/锁屏), 直接给出可操作提示,
    // 不要让玩家干等 40 秒 DataChannel 超时。
    if (mode === 'p2p' && info.peers === 0) {
      throw new Error('房主暂时不在线（P2P 房间跑在房主浏览器里）。请让房主回到游戏页面后再加入，或让房主改用「在线服务器」模式。')
    }
    // 注意顺序: 必须先 ensureIdentity(用旧昵称判断是否需要新建身份), 再改昵称。
    // 否则 ensureIdentity 内部的 `identity.nickname === name` 恒真, 会沿用旧 guestId,
    // 服务端会当成同一游客并回「请先离开当前房间」。
    // P2P 用离线身份: 不依赖线上服务器的可达性。
    await ensureIdentity(name, mode !== 'server')
    // 头部联机方式文案跟随本次加入实际使用的传输方式。
    transport.value = mode
    await connectTransport(mode, code)
    conn.joinRoom(code, joinPassword.value)
    if (conn.lastError) throw new Error(conn.lastError.message)
    stayOnHome.value = false
    screen.value = 'room'
  } catch (err) {
    pageError.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}

function onStartGame(): void {
  conn.startMatch()
}

/** 一局结束后「再来一局」: 只有房主会真正发起, 其他人等待房主开局。 */
function onRestartGame(): void {
  showResultModal.value = false
  leftTable.value = false
  stayOnHome.value = false
  match.clear()
  screen.value = 'room'
  if (isHost.value) conn.startMatch()
}

/** 牌桌「回房间」: 只离开牌桌视图, 座位与房间都保留(可以再回来继续打)。 */
function onBackToWaiting(): void {
  showResultModal.value = false
  leftTable.value = true
  screen.value = 'room'
  void exitFullscreenIfAny()
}

/** 从房间界面重新进入未结束的对局 */
function onResumeMatch(): void {
  leftTable.value = false
  if (!match.view && transport.value === 'server') conn.resync()
  screen.value = 'table'
}

/** 真正离开房间: 断连接(在线)/解散信令(P2P), 回到首页。 */
async function onLeaveRoom(): Promise<void> {
  showResultModal.value = false
  leftTable.value = false
  await exitFullscreenIfAny()
  try {
    if (transport.value === 'server') {
      conn.leaveRoom()
      await new Promise((resolve) => window.setTimeout(resolve, 150))
      conn.disconnect()
    } else {
      conn.disconnect()
      await wrtc.leaveP2P()
    }
  } finally {
    match.clear()
    pageError.value = ''
    // 主动退房: 忘掉房间记忆, 并把座位立刻还给别人(服务端 leave 会交棒房主)。
    savedRoom.value = null
    persistRoomHint(null)
    screen.value = 'home'
  }
}

function onGameAction(action: string, payload?: Record<string, unknown>): void {
  conn.gameAction(action, payload)
}

/** 房主补/删机器人, 用于有人离开后仍能开下一局 */
function onSeatBot(seat: number, add: boolean): void {
  conn.setBot(seat, add)
}

/* ---------------------------------------------------------------- DIY 主题 */

/** DIY 密码门状态 */
const showDIYGate = ref(false)
const diyPasswordInput = ref('')
const diyGateBusy = ref(false)
const diyGateError = ref('')
/** 通过校验后的密码(仅内存), 用于设置页的写操作 */
const diyPassword = ref('')
/** 是否已进入设置页 */
const diyUnlocked = ref(false)
/** 服务端是否已设置过密码(用于首次进入时的文案) */
const diyPasswordSet = ref(true)

async function openDIYGate(): Promise<void> {
  showDIYGate.value = true
  diyPasswordInput.value = ''
  diyGateError.value = ''
  // 顺便刷新一次, 拿到 passwordSet 与已自定义数量
  await diy.refresh(true)
  diyPasswordSet.value = diy.passwordSet
}

function closeDIYGate(): void {
  showDIYGate.value = false
  diyPasswordInput.value = ''
  diyGateError.value = ''
}

/** 首次设置密码: 没有密码时直接输入一次即完成设置并进入 */
async function submitDIYGate(): Promise<void> {
  const pw = diyPasswordInput.value
  if (diyGateBusy.value) return
  if (pw.length < 4) {
    diyGateError.value = '密码至少 4 个字符'
    return
  }
  diyGateBusy.value = true
  diyGateError.value = ''
  try {
    if (!diyPasswordSet.value) {
      // 尚未设置密码: 首次输入即作为管理密码
      await diySetPassword('', pw)
      diyPasswordSet.value = true
      diy.passwordSet = true
      diyPassword.value = pw
      diyUnlocked.value = true
      closeDIYGate()
      screen.value = 'diy'
      return
    }
    await diyAuth(pw)
    diyPassword.value = pw
    diyUnlocked.value = true
    closeDIYGate()
    screen.value = 'diy'
  } catch (err) {
    diyGateError.value = err instanceof Error ? err.message : '密码不正确'
  } finally {
    diyGateBusy.value = false
  }
}

/** 设置页改了密码: 同步内存里的密码, 后续保存继续可用 */
function onDIYPasswordChanged(next: string): void {
  diyPassword.value = next
}

function closeDIYSettings(): void {
  diyUnlocked.value = false
  screen.value = 'home'
}
</script>

<template>
  <div class="min-h-screen bg-stone-50 text-stone-800">
    <!-- 房主迁移提示(阶段7): 任意界面都可见, 6 秒后自动消失 -->
    <Teleport to="body">
      <div v-if="hostNotice" class="pointer-events-none fixed inset-x-0 top-3 z-[60] flex justify-center px-4">
        <div
          class="pointer-events-auto max-w-md rounded-full border border-amber-500/40 bg-stone-900/90 px-4 py-2 text-center text-xs text-amber-100 shadow-lg backdrop-blur"
        >
          {{ hostNotice }}
        </div>
      </div>
    </Teleport>
    <!-- P2P 房主消失: 明确终止(阶段7)。P2P 的牌墙与隐藏手牌只存在于房主浏览器,
         没有可信备份可以移交, 所以只能告知并让玩家重开, 而不是伪装成还能继续。 -->
    <Teleport to="body">
      <div
        v-if="conn.hostLost"
        class="fixed inset-0 z-[70] flex items-center justify-center bg-black/50 px-4"
      >
        <div class="w-full max-w-sm rounded-2xl border border-stone-200 bg-white p-6 shadow-xl">
          <h2 class="text-center text-lg font-semibold text-stone-800">对局已中断</h2>
          <p class="mt-3 text-center text-sm leading-relaxed text-stone-600">
            房主已离开，P2P 对局的牌墙与手牌只在房主浏览器里，无法安全恢复。
          </p>
          <p class="mt-2 text-center text-xs text-stone-400">这一局只能作废，请重新创建房间。</p>
          <button
            type="button"
            class="mt-5 w-full rounded-xl bg-emerald-600 py-3 text-base font-medium text-white transition hover:bg-emerald-700"
            @click="onHostLostAck"
          >
            知道了，返回首页
          </button>
        </div>
      </div>
    </Teleport>
    <!-- 首页: 只有一个名字输入 -->
    <div v-if="screen === 'home'" class="relative flex min-h-screen items-center justify-center px-4">
      <!-- 左边缘的 DIY 设置入口(小齿轮) -->
      <button
        type="button"
        class="fixed left-0 top-1/2 z-30 -translate-y-1/2 rounded-r-lg border border-l-0 border-stone-300 bg-white/90 px-1.5 py-3 text-stone-400 shadow-sm backdrop-blur transition hover:bg-white hover:text-stone-700"
        title="DIY 牌面设置"
        aria-label="DIY 牌面设置"
        @click="openDIYGate"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" class="h-4 w-4">
          <circle cx="12" cy="12" r="3.2" />
          <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.7 1.7 0 0 0-1.87-.34 1.7 1.7 0 0 0-1.03 1.56V21a2 2 0 1 1-4 0v-.09A1.7 1.7 0 0 0 8.9 19.3a1.7 1.7 0 0 0-1.87.34l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.7 1.7 0 0 0 4.7 15a1.7 1.7 0 0 0-1.56-1.03H3a2 2 0 1 1 0-4h.09A1.7 1.7 0 0 0 4.7 8.9a1.7 1.7 0 0 0-.34-1.87l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.7 1.7 0 0 0 9 4.7a1.7 1.7 0 0 0 1.03-1.56V3a2 2 0 1 1 4 0v.09A1.7 1.7 0 0 0 15.1 4.7a1.7 1.7 0 0 0 1.87-.34l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.7 1.7 0 0 0 19.4 9v.09A1.7 1.7 0 0 0 20.96 10H21a2 2 0 1 1 0 4h-.09A1.7 1.7 0 0 0 19.4 15Z" />
        </svg>
      </button>

      <div class="w-full max-w-sm rounded-2xl border border-stone-200 bg-white p-7 shadow-sm">
        <h1 class="text-center text-2xl font-semibold tracking-tight text-stone-800">我嘞麻将</h1>
        <p class="mt-1 text-center text-sm text-stone-500">输入名字即可开始</p>

        <input
          v-model="nicknameInput"
          class="mt-6 w-full rounded-xl border border-stone-300 px-3 py-3 text-center text-base focus:border-emerald-500 focus:outline-none"
          placeholder="你的名字"
          maxlength="12"
          @keyup.enter="confirmName"
        />

        <button
          v-if="!nameConfirmed"
          class="mt-3 w-full rounded-xl bg-emerald-600 px-4 py-3 text-base font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
          :disabled="!nicknameInput.trim()"
          @click="confirmName"
        >
          进入
        </button>

        <template v-else>
          <!-- 阶段7: 还在某个房间里(刷新/按返回键之后) —— 给一条回到牌桌的路 -->
          <div
            v-if="savedRoom || conn.room"
            class="mt-4 rounded-xl border border-emerald-200 bg-emerald-50/80 px-3 py-2.5 text-left"
          >
            <p class="text-xs font-medium text-emerald-800">
              {{ resuming ? '正在回到房间…' : `你还在房间 ${conn.room?.code || savedRoom?.code || ''}` }}
            </p>
            <p class="mt-0.5 text-[11px] leading-relaxed text-emerald-700">
              {{ conn.room?.status === 'playing'
                ? '这局还没结束，可以随时回到牌桌继续。'
                : '座位还为你保留，回去即可继续（也可以留下让别人接手）。' }}
            </p>
            <div class="mt-2 flex gap-2">
              <button
                type="button"
                class="flex-1 rounded-lg bg-emerald-600 px-3 py-2 text-xs font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
                :disabled="resuming"
                @click="onResumeSession"
              >
                继续对局
              </button>
              <button
                type="button"
                class="rounded-lg border border-emerald-300 px-3 py-2 text-xs text-emerald-800 hover:bg-emerald-100"
                @click="onAbandonSession"
              >
                离开房间
              </button>
            </div>
          </div>

          <button
            class="mt-3 w-full rounded-xl bg-emerald-600 px-4 py-3 text-base font-medium text-white hover:bg-emerald-700"
            @click="screen = 'create'"
          >
            创建房间
          </button>
          <button
            class="mt-2 w-full rounded-xl border border-stone-300 px-4 py-3 text-base text-stone-700 hover:bg-stone-50"
            @click="screen = 'join'"
          >
            加入房间
          </button>
          <button class="mt-3 w-full text-xs text-stone-400 hover:text-stone-600" @click="nameConfirmed = false">
            换个名字
          </button>
        </template>

        <p v-if="pageError" class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-center text-xs text-red-600">{{ pageError }}</p>
      </div>
    </div>

    <!-- 创建房间设置 -->
    <CreateRoomScreen
      v-else-if="screen === 'create'"
      :busy="busy"
      :error-text="pageError"
      @create="onCreateRoom"
      @back="screen = 'home'"
    />

    <!-- DIY 牌面设置 -->
    <DIYThemeSettings
      v-else-if="screen === 'diy' && diyUnlocked"
      :password="diyPassword"
      @close="closeDIYSettings"
      @password-changed="onDIYPasswordChanged"
    />

    <!-- 加入房间 -->
    <div v-else-if="screen === 'join'" class="flex min-h-screen items-center justify-center px-4">
      <div class="w-full max-w-sm rounded-2xl border border-stone-200 bg-white p-7 shadow-sm">
        <h1 class="text-center text-xl font-semibold text-stone-800">加入房间</h1>
        <p class="mt-1 text-center text-sm text-stone-500">输入名字和房间码</p>

        <input
          v-model="nicknameInput"
          class="mt-6 w-full rounded-xl border border-stone-300 px-3 py-3 text-center text-base focus:border-emerald-500 focus:outline-none"
          placeholder="你的名字"
          maxlength="12"
        />
        <input
          v-model="joinCodeInput"
          class="mt-2 w-full rounded-xl border border-stone-300 px-3 py-3 text-center font-mono text-2xl uppercase tracking-[0.3em] focus:border-emerald-500 focus:outline-none"
          placeholder="房间码"
          maxlength="6"
        />
        <input
          v-model="joinPassword"
          class="mt-2 w-full rounded-xl border border-stone-300 px-3 py-2.5 text-center text-sm focus:border-emerald-500 focus:outline-none"
          placeholder="房间密码（没有就留空）"
          maxlength="32"
        />

        <button
          class="mt-4 w-full rounded-xl bg-emerald-600 px-4 py-3 text-base font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
          :disabled="busy || !joinCodeInput.trim()"
          @click="onJoinRoom"
        >
          {{ busy ? '正在加入…' : '加入' }}
        </button>
        <button class="mt-2 w-full text-xs text-stone-400 hover:text-stone-600" @click="screen = 'home'">返回</button>
        <p v-if="pageError" class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-center text-xs text-red-600">{{ pageError }}</p>
      </div>
    </div>

    <!-- 等待房间 -->
    <WaitingRoom
      v-else-if="screen === 'room' && conn.room"
      :room="conn.room"
      :my-guest-id="myGuestId"
      :is-host="isHost"
      :transport-label="transportLabel"
      :preset-name="presetName"
      :pool-total="poolTotal"
      :error-text="pageError"
      :can-resume="hasLiveMatch"
      :p2p-host="isP2PHost"
      :peer-count="wrtc.p2pPeers.filter((p) => p.id !== wrtc.myPeerId).length + 1"
      @start="onStartGame"
      @leave="onLeaveRoom"
      @resume="onResumeMatch"
      @bot="onSeatBot"
    />

    <!-- 对局 -->
    <div
      v-else-if="screen === 'table' && match.view"
      class="flex h-screen flex-col overflow-hidden bg-stone-900"
      style="height: 100dvh"
    >
      <div
        v-if="!isFullscreen"
        class="mx-auto flex w-full max-w-6xl items-center justify-between gap-1.5 px-2 py-1.5 sm:gap-2 sm:px-3"
      >
        <div class="min-w-0 flex-1 truncate text-xs text-stone-300 sm:text-sm">{{ conn.room?.name || '对局' }}</div>
        <div class="flex shrink-0 items-center gap-1">
          <ThemeSwitcher />
          <button
            type="button"
            class="rounded-md border border-stone-600 px-2 py-1 text-[11px] text-stone-300 hover:border-stone-400 hover:text-white"
            title="回到房间(保留座位, 可随时回到牌桌)"
            @click="onBackToWaiting"
          >
            回房间
          </button>
        </div>
      </div>
      <div class="min-h-0 flex-1 px-1 pb-1">
        <MahjongTable
          class="mx-auto h-full"
          :view="match.view"
          :players="conn.room?.players ?? []"
          :my-guest-id="myGuestId"
          @action="onGameAction"
        />
      </div>
      <GameResultModal
        v-if="showResultModal && match.view.result"
        :result="match.view.result"
        :my-seat="match.view.seat"
        :players="conn.room?.players ?? []"
        :is-host="isHost"
        @close="showResultModal = false"
        @restart="onRestartGame"
        @back="onBackToWaiting"
        @leave="onLeaveRoom"
      />
    </div>

    <!-- DIY 密码门(无账户, 只有一个密码) -->
    <Teleport to="body">
      <div
        v-if="showDIYGate"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
        @click.self="closeDIYGate"
      >
        <div class="w-full max-w-xs overflow-hidden rounded-2xl bg-white shadow-2xl">
          <div class="border-b border-stone-200 px-4 py-3">
            <div class="text-sm font-semibold text-stone-800">
              {{ diyPasswordSet ? '输入 DIY 设置密码' : '设置 DIY 管理密码' }}
            </div>
            <p class="mt-0.5 text-[11px] text-stone-500">
              {{ diyPasswordSet
                ? '该密码用于进入牌面自定义界面'
                : '首次使用请先设置一个管理密码（至少 4 位）' }}
            </p>
          </div>
          <div class="p-4">
            <input
              v-model="diyPasswordInput"
              type="password"
              class="w-full rounded-xl border border-stone-300 px-3 py-2.5 text-center text-base tracking-widest focus:border-emerald-500 focus:outline-none"
              :placeholder="diyPasswordSet ? '密码' : '设置密码'"
              autocomplete="current-password"
              @keyup.enter="submitDIYGate"
            />
            <p v-if="diyGateError" class="mt-2 rounded-lg bg-red-50 px-2.5 py-1.5 text-[11px] text-red-600">{{ diyGateError }}</p>
            <button
              type="button"
              class="mt-3 w-full rounded-xl bg-emerald-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
              :disabled="diyGateBusy || !diyPasswordInput"
              @click="submitDIYGate"
            >
              {{ diyGateBusy ? '校验中…' : (diyPasswordSet ? '进入设置' : '设置并进入') }}
            </button>
            <button
              type="button"
              class="mt-2 w-full text-xs text-stone-400 hover:text-stone-600"
              @click="closeDIYGate"
            >
              取消
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
