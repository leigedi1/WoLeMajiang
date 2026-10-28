<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { SeatView, PlayerView } from '../protocol/types'
import MahjongTile from './MahjongTile.vue'
import TableCenter from './TableCenter.vue'
import PlayerRiver from './PlayerRiver.vue'
import PlayerMelds from './PlayerMelds.vue'
import ActionControls from './ActionControls.vue'

interface Props {
  view: SeatView
  players?: PlayerView[]
  myGuestId?: string
  disabled?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  players: () => [],
  disabled: false,
})

const emit = defineEmits<{
  (e: 'action', action: string, payload?: Record<string, unknown>): void
}>()

const seatWinds = ['东', '南', '西', '北']
const totalSeats = computed(() => (props.players && props.players.length > 0 ? props.players.length : 4))
const mySeat = computed(() => props.view.seat ?? 0)

// 相对座位映射:
// 0 = 自己 (下)
// 1 = 下家 (右)
// 2 = 对家 (上)
// 3 = 上家 (左)
function seatOfRelative(rel: number): number {
  return (mySeat.value + rel) % totalSeats.value
}

function playerOfSeat(seat: number): PlayerView | undefined {
  return props.players.find((p) => p.seat === seat)
}

/** 某个座位还剩几张手牌(他人的暗牌只能给数量, 自家直接看 hand) */
function handCountOf(seat: number): number {
  if (seat === mySeat.value) return props.view.hand?.length ?? 0
  return props.view.othersHandCounts?.[seat] ?? 0
}

function windOfSeat(seat: number): string {
  return seatWinds[seat % 4] || ''
}

function nameOfSeat(seat: number): string {
  if (seat === mySeat.value) return playerOfSeat(seat)?.nickname || '我'
  return playerOfSeat(seat)?.nickname || ['对家', '下家', '上家'][0] || `座位${seat + 1}`
}

// 玩家名称与风位
const bottomPlayer = computed(() => playerOfSeat(mySeat.value))

// 各方位实际对应的座位(-1 = 该方位没有玩家):
// 2 人局只有对家, 3 人局是下家/上家, 4 人局才是右/上/左三面。
// 若直接用 (mySeat+rel)%总数, 2 人局会把「对家」算成自己、把「上家」算成对手。
const topSeat = computed(() => {
  if (totalSeats.value === 2) return seatOfRelative(1)
  if (totalSeats.value === 4) return seatOfRelative(2)
  return -1
})
const rightSeat = computed(() => (totalSeats.value >= 3 ? seatOfRelative(1) : -1))
const leftSeat = computed(() => {
  if (totalSeats.value === 3) return seatOfRelative(2)
  if (totalSeats.value === 4) return seatOfRelative(3)
  return -1
})
const topPlayer = computed(() => (topSeat.value >= 0 ? playerOfSeat(topSeat.value) : undefined))
const rightPlayer = computed(() => (rightSeat.value >= 0 ? playerOfSeat(rightSeat.value) : undefined))
const leftPlayer = computed(() => (leftSeat.value >= 0 ? playerOfSeat(leftSeat.value) : undefined))

// 是否轮到自家出牌
const isMyDiscardTurn = computed(() => {
  return props.view.turnStage === 'discard' && props.view.turnSeat === mySeat.value
})

// 是否为最新打出的弃牌
const lastDiscardSeat = computed(() => props.view.pendingBy ?? -1)

function onTileClick(code: string) {
  if (isMyDiscardTurn.value) {
    emit('action', 'discard', { tile: code })
  }
}

// ---- 出牌记录: 桌面只留最近 6 张, 点开二级面板看全部 ----
/** 全局出牌流水(按时间顺序); 老引擎没有 discardLog 时退回空数组 */
const discardLog = computed<{ seat: number; tile: string }[]>(() => props.view.discardLog ?? [])
/** 桌面横条只放最近几张: 窄屏 3 张, 宽屏 6 张(其余进二级面板) */
const recentCount = computed(() => (narrow.value ? 3 : 6))
const recentDiscards = computed(() => discardLog.value.slice(-recentCount.value))
const showHistory = ref(false)

/** 历史面板: 按相对座位顺序列出四家完整牌河 */
const historySeats = computed(() =>
  Array.from({ length: totalSeats.value }, (_, rel) => {
    const seat = seatOfRelative(rel)
    return {
      seat,
      wind: windOfSeat(seat),
      name: nameOfSeat(seat),
      isMe: seat === mySeat.value,
      tiles: props.view.discards?.[seat] ?? [],
    }
  }),
)

// ---- 阶段10: 响应/出牌倒计时 + 判定状态 ----
// 服务端与 WASM 引擎都只下发截止时间(Unix 毫秒)与剩余秒数, 这里用本地时钟
// 平滑递减, 不依赖服务端逐秒推送。
const nowMs = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | null = null

/** 本家当前倒计时剩余秒数(响应优先, 其次出牌); 未启用倒计时时 undefined */
const mySecondsLeft = computed<number | undefined>(() => {
  const v = props.view
  const deadline = v.respondDeadline ?? v.discardDeadline
  if (!deadline) {
    // 老引擎/未启用倒计时: 退回服务端给的静态剩余秒数
    const fallback = v.respondSecLeft ?? v.discardSecLeft
    return fallback && fallback > 0 ? fallback : undefined
  }
  return Math.max(0, Math.ceil((deadline - nowMs.value) / 1000))
})

/** 某个座位是否处于"判定中" */
function claimStateOf(seat: number): string {
  return props.view.claimStates?.[seat] ?? ''
}

function claimLabel(seat: number): string {
  switch (claimStateOf(seat)) {
    case 'pending':
      return '判定中'
    case 'success':
      return '判定成功'
    case 'failed':
      return '判定失败'
    default:
      return ''
  }
}

function claimClass(seat: number): string {
  switch (claimStateOf(seat)) {
    case 'pending':
      return 'bg-amber-400/90 text-stone-900 animate-pulse'
    case 'success':
      return 'bg-emerald-400/90 text-stone-900'
    case 'failed':
      return 'bg-stone-600/90 text-stone-300'
    default:
      return ''
  }
}

/** 出牌倒计时: 别人待出牌时显示「等待 X 出牌 Ns」 */
const waitingDiscard = computed<{ name: string; sec: number } | null>(() => {
  const v = props.view
  if (v.turnStage !== 'discard' || v.turnSeat === mySeat.value) return null
  const deadline = v.discardDeadline
  if (!deadline) return null
  return {
    name: nameOfSeat(v.turnSeat),
    sec: Math.max(0, Math.ceil((deadline - nowMs.value) / 1000)),
  }
})


// 窄屏(竖屏手机)收窄两侧玩家; 矮屏(横屏手机)压缩纵向占用。
const narrow = ref(false)
const short = ref(false)
const compact = computed(() => narrow.value || short.value)
const handTileSize = computed<'sm' | 'md'>(() => (compact.value ? 'sm' : 'md'))
const riverPerRow = computed(() => (narrow.value ? 4 : 8))

function updateViewport() {
  narrow.value = window.innerWidth < 640
  short.value = window.innerHeight < 560
}

// ---- 全屏(手机横屏被浏览器上下栏遮挡时使用) ----
const isFullscreen = ref(false)
const fullscreenSupported = ref(false)

function syncFullscreen() {
  const doc = document as Document & { webkitFullscreenElement?: Element | null }
  isFullscreen.value = Boolean(doc.fullscreenElement || doc.webkitFullscreenElement)
}

async function toggleFullscreen() {
  const doc = document as Document & {
    webkitFullscreenElement?: Element | null
    webkitExitFullscreen?: () => Promise<void>
    exitFullscreen?: () => Promise<void>
  }
  try {
    if (doc.fullscreenElement || doc.webkitFullscreenElement) {
      await (doc.exitFullscreen ? doc.exitFullscreen() : doc.webkitExitFullscreen?.())
      return
    }
    const el = document.documentElement as HTMLElement & {
      webkitRequestFullscreen?: () => Promise<void>
      requestFullscreen?: (opts?: FullscreenOptions) => Promise<void>
    }
    if (el.requestFullscreen) {
      await el.requestFullscreen({ navigationUI: 'hide' })
    } else {
      await el.webkitRequestFullscreen?.()
    }
    // 安卓 Chrome 允许在全屏后锁定横屏; iOS 不支持, 失败时静默忽略。
    const so = (screen as unknown as { orientation?: { lock?: (o: string) => Promise<void> } }).orientation
    if (so && typeof so.lock === 'function') {
      so.lock('landscape').catch(() => {})
    }
  } catch {
    // 浏览器拒绝全屏(如 iOS Safari): 保持普通模式, 由 dvh 布局兜底。
  }
}

// 手牌横向滚动容器: 出牌/摸牌后自动滚到最右侧(新摸的牌), 且所有牌都能滑到。
const handScroll = ref<HTMLElement | null>(null)

function scrollHandToEnd() {
  const el = handScroll.value
  if (!el) return
  el.scrollLeft = el.scrollWidth
}

watch(
  () => (props.view.hand ? props.view.hand.length : 0),
  () => nextTick(scrollHandToEnd),
)

// 打开历史面板时禁掉背景滚动(Escape 关闭)
function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && showHistory.value) showHistory.value = false
}

let liveQuery: MediaQueryList | null = null
function onLiveChange() {
  updateViewport()
}

onMounted(() => {
  updateViewport()
  window.addEventListener('resize', updateViewport, { passive: true })
  window.addEventListener('orientationchange', updateViewport, { passive: true })
  window.addEventListener('keydown', onKeydown)
  // 倒计时本地时钟: 200ms 刷新一次, 只在有截止时间时才有意义
  clockTimer = setInterval(() => {
    nowMs.value = Date.now()
  }, 200)
  if (typeof window.matchMedia === 'function') {
    liveQuery = window.matchMedia('(max-width: 640px)')
    liveQuery.addEventListener?.('change', onLiveChange)
  }
  fullscreenSupported.value = Boolean(
    document.fullscreenEnabled ||
      (document as Document & { webkitFullscreenEnabled?: boolean }).webkitFullscreenEnabled,
  )
  document.addEventListener('fullscreenchange', syncFullscreen)
  document.addEventListener('webkitfullscreenchange', syncFullscreen as EventListener)
  syncFullscreen()
  nextTick(scrollHandToEnd)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', updateViewport)
  window.removeEventListener('orientationchange', updateViewport)
  window.removeEventListener('keydown', onKeydown)
  liveQuery?.removeEventListener?.('change', onLiveChange)
  document.removeEventListener('fullscreenchange', syncFullscreen)
  document.removeEventListener('webkitfullscreenchange', syncFullscreen as EventListener)
  if (clockTimer !== null) {
    clearInterval(clockTimer)
    clockTimer = null
  }
})
</script>

<template>
  <div
    class="mahjong-table relative flex h-full min-h-0 w-full max-w-5xl flex-col items-center justify-between overflow-hidden rounded-xl border-4 border-stone-800 bg-gradient-to-br from-emerald-800 via-emerald-900 to-teal-950 p-1.5 text-white shadow-2xl sm:rounded-2xl sm:border-8 sm:p-4"
  >
    <!-- 桌布微绒微光底纹 (纯 CSS) -->
    <div class="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_center,rgba(255,255,255,0.06)_0%,transparent_75%)]"></div>

    <!-- 全屏按钮: 手机横屏被浏览器地址栏/底栏压缩时点它即可占满屏幕 -->
    <button
      v-if="fullscreenSupported"
      type="button"
      class="absolute right-1 top-1 z-40 rounded-md border border-emerald-500/40 bg-black/45 px-1.5 py-0.5 text-[10px] font-medium text-emerald-100 backdrop-blur-xs active:scale-95 sm:text-xs"
      :aria-label="isFullscreen ? '退出全屏' : '全屏显示'"
      @click.stop="toggleFullscreen"
    >
      {{ isFullscreen ? '退出全屏' : '⛶ 全屏' }}
    </button>

    <!-- 顶部方位 (对家; 2 人局时对手就坐在这里) -->
    <div v-if="topSeat >= 0" class="z-10 flex w-full shrink-0 flex-col items-center gap-0.5 pt-6 sm:pt-0.5">
      <div class="flex max-w-full items-center gap-1.5 rounded-full bg-black/40 px-2 py-0.5 text-[10px] backdrop-blur-xs sm:gap-2 sm:px-3 sm:py-1 sm:text-xs">
        <span class="font-bold text-amber-300">{{ seatWinds[topSeat % 4] }}</span>
        <span class="max-w-[5.5rem] truncate font-medium text-stone-200 sm:max-w-[120px]">
          {{ topPlayer?.nickname || '对家' }}
        </span>
        <span class="shrink-0 rounded bg-black/30 px-1 py-0.5 font-mono text-[9px] text-emerald-200/80">
          {{ handCountOf(topSeat) }}张
        </span>
        <span
          class="h-1.5 w-1.5 rounded-full sm:h-2 sm:w-2"
          :class="(topPlayer?.connected ?? true) ? 'bg-emerald-400' : 'bg-stone-500'"
        />
        <span
          v-if="view.turnSeat === topSeat"
          class="rounded bg-amber-400/90 px-1 py-0.5 text-[9px] font-bold text-stone-900 sm:text-[10px]"
        >
          行动
        </span>
        <span
          v-if="claimLabel(topSeat)"
          class="rounded px-1 py-0.5 text-[9px] font-bold sm:text-[10px]"
          :class="claimClass(topSeat)"
        >
          {{ claimLabel(topSeat) }}
        </span>
      </div>

      <!-- 对家副露(暗牌数量已用「N 张」表示, 矮屏不再画牌背) -->
      <div class="flex max-w-full items-center gap-2 overflow-hidden">
        <PlayerMelds :melds="view.othersMelds[topSeat] || []" size="xs" />
        <div v-if="!short" class="flex items-center gap-0.5">
          <MahjongTile
            v-for="i in Math.min(13, handCountOf(topSeat))"
            :key="'top-hand-' + i"
            code="back"
            size="xs"
          />
        </div>
      </div>
    </div>

    <!-- 中间区域: 左右玩家 + 中央(牌墙 + 最近出牌) -->
    <div class="relative z-10 flex w-full flex-1 items-center justify-between gap-1 overflow-hidden px-0.5 py-0.5 sm:gap-3 sm:px-3 sm:py-1">
      <!-- 左侧方位 (上家; 3 人局的对家也在这里) -->
      <div v-if="leftSeat >= 0" class="flex w-12 shrink-0 flex-col items-center gap-0.5 sm:w-28 sm:gap-1.5">
        <div class="flex max-w-full flex-col items-center rounded-lg bg-black/40 px-1 py-0.5 text-[10px] backdrop-blur-xs sm:flex-row sm:gap-1.5 sm:rounded-full sm:px-2.5 sm:py-1 sm:text-xs">
          <span class="font-bold text-amber-300">{{ seatWinds[leftSeat % 4] }}</span>
          <span class="w-full truncate text-center text-[9px] text-stone-200 sm:w-auto sm:max-w-[64px] sm:text-xs">
            {{ leftPlayer?.nickname || '上家' }}
          </span>
          <span class="rounded bg-black/30 px-1 py-0.5 font-mono text-[9px] leading-none text-emerald-200/80">
            {{ handCountOf(leftSeat) }}张
          </span>
          <span
            v-if="view.turnSeat === leftSeat"
            class="rounded bg-amber-400/90 px-1 py-0.5 text-[8px] font-bold text-stone-900 sm:text-[9px]"
          >
            行动
          </span>
          <span
            v-if="claimLabel(leftSeat)"
            class="rounded px-1 py-0.5 text-[8px] font-bold sm:text-[9px]"
            :class="claimClass(leftSeat)"
          >
            {{ claimLabel(leftSeat) }}
          </span>
        </div>
        <PlayerMelds :melds="view.othersMelds[leftSeat] || []" size="xs" />
        <div v-if="!short" class="flex flex-col items-center gap-0.5">
          <MahjongTile
            v-for="i in Math.min(narrow ? 6 : 13, handCountOf(leftSeat))"
            :key="'left-hand-' + i"
            code="back"
            size="xs"
            horizontal
          />
        </div>
      </div>

    <!-- 桌心: 剩余牌墙 + 最近出牌横条(点开看全部) -->
      <div class="flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 sm:gap-1">
        <TableCenter
          :wall-left="view.wallLeft"
          :turn-seat="view.turnSeat"
          :my-seat="mySeat"
          :turn-stage="view.turnStage"
          :seat-count="totalSeats"
          :pending-tile="view.pendingTile"
        />

        <!-- 出牌倒计时: 别人待出牌时显示「等待 X 出牌」 -->
        <div
          v-if="waitingDiscard"
          class="max-w-full truncate rounded-full border border-amber-500/40 bg-black/40 px-1.5 py-0.5 text-[9px] font-medium text-amber-200 tabular-nums sm:px-2 sm:text-[10px]"
        >
          等待 {{ waitingDiscard.name }} 出牌 · {{ waitingDiscard.sec }}s
        </div>

        <!-- 本家判定中提示(碰/杠/吃已提交, 等其他家决定是否截胡) -->
        <div
          v-if="claimStateOf(mySeat) === 'pending'"
          class="rounded-full border border-amber-400/60 bg-amber-950/70 px-2 py-0.5 text-[10px] font-bold text-amber-200"
        >
          判定中… 等待其他家确认
        </div>
        <div
          v-else-if="claimStateOf(mySeat) === 'failed'"
          class="rounded-full border border-stone-600 bg-stone-900/80 px-2 py-0.5 text-[10px] font-medium text-stone-300"
        >
          判定失败 · 被更高优先级截走
        </div>

        <button
          type="button"
          class="flex max-w-full items-center gap-0.5 overflow-hidden rounded-md border border-emerald-700/50 bg-black/30 px-1 py-0.5 text-[9px] text-emerald-100/80 active:scale-95 sm:gap-1 sm:px-2 sm:text-[10px]"
          :aria-label="`最近出牌, 共 ${discardLog.length} 张, 点开查看全部`"
          @click.stop="showHistory = true"
        >
          <span class="shrink-0 text-emerald-200/70">最近</span>
          <template v-if="recentDiscards.length">
            <MahjongTile
              v-for="(d, i) in recentDiscards"
              :key="'recent-' + i + '-' + d.tile"
              :code="d.tile"
              size="xs"
              :is-last-discard="i === recentDiscards.length - 1"
            />
          </template>
          <span v-else class="px-1 text-emerald-100/40">—</span>
          <span class="ml-0.5 shrink-0 underline decoration-emerald-400/50">全部</span>
        </button>
      </div>

      <!-- 右侧方位 (下家) -->
      <div v-if="rightSeat >= 0" class="flex w-12 shrink-0 flex-col items-center gap-0.5 sm:w-28 sm:gap-1.5">
        <div class="flex max-w-full flex-col items-center rounded-lg bg-black/40 px-1 py-0.5 text-[10px] backdrop-blur-xs sm:flex-row sm:gap-1.5 sm:rounded-full sm:px-2.5 sm:py-1 sm:text-xs">
          <span class="font-bold text-amber-300">{{ seatWinds[rightSeat % 4] }}</span>
          <span class="w-full truncate text-center text-[9px] text-stone-200 sm:w-auto sm:max-w-[64px] sm:text-xs">
            {{ rightPlayer?.nickname || '下家' }}
          </span>
          <span class="rounded bg-black/30 px-1 py-0.5 font-mono text-[9px] leading-none text-emerald-200/80">
            {{ handCountOf(rightSeat) }}张
          </span>
          <span
            v-if="view.turnSeat === rightSeat"
            class="rounded bg-amber-400/90 px-1 py-0.5 text-[8px] font-bold text-stone-900 sm:text-[9px]"
          >
            行动
          </span>
          <span
            v-if="claimLabel(rightSeat)"
            class="rounded px-1 py-0.5 text-[8px] font-bold sm:text-[9px]"
            :class="claimClass(rightSeat)"
          >
            {{ claimLabel(rightSeat) }}
          </span>
        </div>
        <PlayerMelds :melds="view.othersMelds[rightSeat] || []" size="xs" />
        <div v-if="!short" class="flex flex-col items-center gap-0.5">
          <MahjongTile
            v-for="i in Math.min(narrow ? 6 : 13, handCountOf(rightSeat))"
            :key="'right-hand-' + i"
            code="back"
            size="xs"
            horizontal
          />
        </div>
      </div>
    </div>

    <!-- 底部方位 (自家手牌与操作按钮) -->
    <div class="z-20 flex w-full shrink-0 flex-col items-center gap-1 pt-0.5 pb-0.5">
      <!-- 操作按钮浮条 (吃/碰/杠/自摸/和/过) -->
      <div v-if="(view.actions && view.actions.length > 0) || view.forceDraw" class="max-w-full">
        <ActionControls
          :actions="view.actions ?? []"
          :pending-tile="view.pendingTile"
          :hand="view.hand"
          :melds="view.melds"
          :disabled="disabled"
          :seconds-left="mySecondsLeft"
          :force-draw="view.forceDraw === true"
          @action="(act, payload) => emit('action', act, payload)"
        />
      </div>

      <!-- 本家信息栏 (横屏矮屏只在轮到自己时显示提示, 节省高度) -->
      <div
        v-if="!short || isMyDiscardTurn"
        class="flex max-w-full items-center gap-2 px-1 text-[10px] text-stone-300 sm:gap-3 sm:text-xs"
      >
        <span v-if="!short" class="shrink-0 rounded bg-black/40 px-1.5 py-0.5 font-bold text-amber-300">
          {{ seatWinds[mySeat] }} (自家)
        </span>
        <span v-if="!short" class="max-w-[7rem] truncate font-medium text-white">
          {{ bottomPlayer?.nickname || '我' }}
        </span>
        <span v-if="isMyDiscardTurn" class="animate-pulse font-bold text-amber-400">
          👉 轮到你出牌，请点击手牌
        </span>
      </div>

      <!-- 本家已亮明副露 (吃碰杠) -->
      <div v-if="view.melds && view.melds.length > 0" class="max-w-full">
        <PlayerMelds :melds="view.melds" :size="compact ? 'sm' : 'md'" />
      </div>

      <!-- 本家手牌: w-max + min-w-full 保证牌多时能从最左滑到最右, 不会卡住看不到边张 -->
      <div ref="handScroll" class="hand-scroll w-full overflow-x-auto overflow-y-hidden">
        <div class="flex w-max min-w-full items-end justify-center gap-0.5 px-1 pb-0.5 sm:gap-1.5 sm:px-2 sm:py-1">
          <MahjongTile
            v-for="(t, idx) in view.hand"
            :key="'my-hand-' + idx + '-' + t"
            :code="t"
            :size="handTileSize"
            :selectable="isMyDiscardTurn && !disabled"
            :is-drawn="idx === view.hand.length - 1 && view.hand.length % 3 === 2"
            @click="onTileClick"
          />
        </div>
      </div>
    </div>

    <!-- 全部已出牌(二级面板): 桌面只放最近 6 张, 想看历史点这里 -->
    <div
      v-if="showHistory"
      class="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 p-3"
      @click.self="showHistory = false"
    >
      <div class="flex max-h-[86dvh] w-full max-w-md flex-col overflow-hidden rounded-2xl border border-stone-700 bg-stone-900 shadow-2xl">
        <div class="flex shrink-0 items-center justify-between border-b border-stone-700 px-3 py-2">
          <h3 class="text-sm font-semibold text-white">全部已出牌 · {{ discardLog.length }} 张</h3>
          <button
            type="button"
            class="rounded-md border border-stone-600 px-2 py-0.5 text-xs text-stone-300 active:scale-95"
            @click="showHistory = false"
          >
            关闭
          </button>
        </div>
        <div class="min-h-0 flex-1 space-y-2 overflow-y-auto p-2">
          <div v-for="h in historySeats" :key="'hist-' + h.seat" class="rounded-lg bg-black/30 p-1.5">
            <div class="flex items-center gap-1.5 text-[11px]">
              <span class="font-bold text-amber-300">{{ h.wind }}</span>
              <span class="truncate text-stone-200">{{ h.name }}</span>
              <span v-if="h.isMe" class="rounded bg-sky-900/70 px-1 text-[10px] text-sky-200">我</span>
              <span class="ml-auto font-mono text-stone-400">{{ h.tiles.length }} 张</span>
            </div>
            <PlayerRiver
              :tiles="h.tiles"
              :is-current-discard="lastDiscardSeat === h.seat"
              :per-row="riverPerRow"
              size="xs"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mahjong-table {
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.6), inset 0 0 100px rgba(0, 0, 0, 0.4);
  touch-action: manipulation;
  /* iPhone 刘海/底部横条安全区 */
  padding-bottom: max(0.25rem, env(safe-area-inset-bottom));
}

/* 手牌横向滚动: 隐藏滚动条, 松手不粘连, 拖动时不误触出牌 */
.hand-scroll {
  scrollbar-width: none;
  -ms-overflow-style: none;
  -webkit-overflow-scrolling: touch;
  overscroll-behavior-x: contain;
  touch-action: pan-x;
  scroll-padding-inline: 0.5rem;
}

.hand-scroll::-webkit-scrollbar {
  display: none;
}

/* 横屏手机(高度很小): 压缩中央区, 保证手牌与操作按钮始终完整可见 */
@media (max-height: 560px) {
  .mahjong-table {
    border-width: 3px;
    padding: 0.125rem;
    padding-bottom: max(0.125rem, env(safe-area-inset-bottom));
  }

  .mahjong-table :deep(.table-center) {
    padding: 0.125rem 0.375rem;
  }

  .mahjong-table :deep(.table-center .font-mono) {
    font-size: 1rem;
    line-height: 1.1;
  }

  .mahjong-table :deep(.player-river) {
    gap: 0.125rem;
    padding: 0.125rem;
  }
}

/* 手机上的操作按钮: 缩小内边距, 避免一排按钮换行过多遮住手牌 */
@media (max-width: 640px) {
  .mahjong-table :deep(.action-controls) {
    gap: 0.375rem;
  }

  .mahjong-table :deep(.action-controls > button) {
    padding: 0.375rem 0.625rem;
    font-size: 0.8125rem;
    line-height: 1.1;
  }
}

/* 触控设备: 去掉双击缩放/点击延迟, 保证出牌手感 */
@media (pointer: coarse) {
  .mahjong-table :deep(button) {
    touch-action: manipulation;
  }
}
</style>
