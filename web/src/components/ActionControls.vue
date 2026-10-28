<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import MahjongTile from './MahjongTile.vue'
import { getTileMeta } from '../utils/tiles'

interface Props {
  actions: string[] // 可执行的动作代码数组，如 ['hu.self', 'kong.concealed', 'discard']
  pendingTile?: string // 等待响应的牌代码，如 'wan5'
  hand: string[] // 自家暗牌，用于计算吃/暗杠选项
  melds?: Array<{ kind: string; tiles: string[] }> // 自家副露，用于补杠计算
  disabled?: boolean
  /** 响应/出牌倒计时剩余秒数(阶段10); 未启用倒计时时为 undefined */
  secondsLeft?: number
  /** 未启用响应倒计时时, 出牌者下家可用「摸牌」强制收敛窗口 */
  forceDraw?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  actions: () => [],
  hand: () => [],
  melds: () => [],
  disabled: false,
  secondsLeft: undefined,
  forceDraw: false,
})

const emit = defineEmits<{
  (e: 'action', action: string, payload?: Record<string, unknown>): void
}>()

/* ------------------------------------------------------------------ 工具 */

/** 牌代码拆成 (花色, 数字); 非数牌返回 null。 */
function splitCode(code: string): { suit: string; rank: number } | null {
  const m = /^([a-z]+)(\d+)$/.exec(code)
  if (!m) return null
  const rank = Number(m[2])
  if (!Number.isFinite(rank) || rank < 1 || rank > 9) return null
  return { suit: m[1], rank }
}

/** 手牌多重集计数, 用于"确实有这两张牌"的判断(而不是只判断存在)。 */
const handCounts = computed<Record<string, number>>(() => {
  const out: Record<string, number> = {}
  for (const t of props.hand) out[t] = (out[t] ?? 0) + 1
  return out
})

/* -------------------------------------------------------------- 候选计算 */

/** 吃牌候选: 每种组合需要两张具体的手牌, 因此用多重集判断数量。 */
const chowCandidates = computed<Array<[string, string]>>(() => {
  if (!props.pendingTile) return []
  const pend = splitCode(props.pendingTile)
  if (!pend) return []
  const counts = handCounts.value
  const pairs: Array<[string, string]> = []
  const offsets: Array<[number, number]> = [
    [-2, -1],
    [-1, 1],
    [1, 2],
  ]
  for (const [o1, o2] of offsets) {
    const r1 = pend.rank + o1
    const r2 = pend.rank + o2
    if (r1 < 1 || r1 > 9 || r2 < 1 || r2 > 9) continue
    const t1 = `${pend.suit}${r1}`
    const t2 = `${pend.suit}${r2}`
    // 每种组合独立判断(不消耗), 与服务端 chowCandidates 的口径保持一致:
    // 两张不同时各自至少 1 张, 相同时需要至少 2 张。
    const ok =
      t1 === t2 ? (counts[t1] ?? 0) >= 2 : (counts[t1] ?? 0) >= 1 && (counts[t2] ?? 0) >= 1
    if (ok) pairs.push([t1, t2])
  }
  return pairs
})

/** 暗杠候选: 手牌里数量恰好 >= 4 的种类。 */
const concealedKongCandidates = computed<string[]>(() => {
  const counts: Record<string, number> = {}
  for (const t of props.hand) counts[t] = (counts[t] || 0) + 1
  return Object.keys(counts).filter((t) => counts[t] >= 4)
})

/** 补杠候选: 已碰(且不是杠) + 手牌仍有第 4 张。 */
const addedKongCandidates = computed<string[]>(() => {
  const out: string[] = []
  for (const m of props.melds) {
    if (m.kind !== 'pong') continue
    const tile = m.tiles[0]
    if (!tile) continue
    if ((handCounts.value[tile] ?? 0) >= 1 && !out.includes(tile)) out.push(tile)
  }
  return out
})

/* ------------------------------------------------------- 统一选择面板状态 */

type OptionKind = 'chow' | 'kong.concealed' | 'kong.added'

interface Option {
  id: string
  kind: OptionKind
  label: string
  tiles: string[]
  /** 面板里额外的说明(例如"用 3万 4万 吃 5万") */
  hint?: string
  payload?: Record<string, unknown>
}

/** 当前展开的选择面板; null = 未展开。 */
const picker = ref<OptionKind | null>(null)

/**
 * 面板内的提交锁: 选中一项后立刻锁住, 避免手指连点导致同一动作发两次
 * (服务端会按 already_responded 拒绝第二次, 但玩家会看到一次"无效"的报错)。
 * 只作用于多选面板, 不影响主按钮区的正常点击。
 */
const submitting = ref(false)
let submitTimer: ReturnType<typeof setTimeout> | undefined

function lockSubmit() {
  submitting.value = true
  if (submitTimer) clearTimeout(submitTimer)
  submitTimer = setTimeout(() => {
    submitting.value = false
  }, 900)
}

/** 主按钮出口: 关闭面板 + 发事件。 */
function fire(action: string, payload?: Record<string, unknown>) {
  if (props.disabled) return
  picker.value = null
  if (payload) emit('action', action, payload)
  else emit('action', action)
}

/** 面板内选项出口: 加锁后发事件。 */
function fireFromPicker(action: string, payload?: Record<string, unknown>) {
  if (props.disabled || submitting.value) return
  lockSubmit()
  picker.value = null
  if (payload) emit('action', action, payload)
  else emit('action', action)
}

/** 面板关闭(点背景 / 按 ✕ / 倒计时结束)。 */
function closePicker() {
  picker.value = null
}

/** 生成某个候选类型的选项列表。 */
const options = computed<Option[]>(() => {
  if (!picker.value) return []
  const out: Option[] = []
  const pendName = props.pendingTile ? getTileMeta(props.pendingTile).name : ''
  if (picker.value === 'chow') {
    chowCandidates.value.forEach((pair, i) => {
      out.push({
        id: `chow-${i}`,
        kind: 'chow',
        label: `吃 ${pendName}`,
        tiles: [...pair, props.pendingTile!],
        hint: `${getTileMeta(pair[0]).name} + ${getTileMeta(pair[1]).name} + ${pendName}`,
        payload: { with: pair },
      })
    })
  } else if (picker.value === 'kong.concealed') {
    concealedKongCandidates.value.forEach((tile, i) => {
      out.push({
        id: `kc-${i}`,
        kind: 'kong.concealed',
        label: `暗杠 ${getTileMeta(tile).name}`,
        tiles: [tile, tile, tile, tile],
        payload: { tile },
      })
    })
  } else if (picker.value === 'kong.added') {
    addedKongCandidates.value.forEach((tile, i) => {
      out.push({
        id: `ka-${i}`,
        kind: 'kong.added',
        label: `补杠 ${getTileMeta(tile).name}`,
        tiles: [tile, tile, tile, tile],
        hint: '已有碰牌, 补第 4 张',
        payload: { tile },
      })
    })
  }
  return out
})

/** 面板标题。 */
const pickerTitle = computed(() => {
  switch (picker.value) {
    case 'chow':
      return '选择吃牌组合'
    case 'kong.concealed':
      return '选择要暗杠的牌'
    case 'kong.added':
      return '选择要补杠的牌'
    default:
      return ''
  }
})

function choose(opt: Option) {
  fireFromPicker(opt.kind, opt.payload)
}

/** 点「吃」: 单组直接出, 多组展开面板。 */
function onChowClick() {
  const n = chowCandidates.value.length
  if (n === 0) return
  if (n === 1) {
    fireFromPicker('chow', { with: chowCandidates.value[0] })
    return
  }
  picker.value = 'chow'
}

function onConcealedKongClick() {
  const n = concealedKongCandidates.value.length
  if (n === 0) return
  if (n === 1) {
    fireFromPicker('kong.concealed', { tile: concealedKongCandidates.value[0] })
    return
  }
  picker.value = 'kong.concealed'
}

function onAddedKongClick() {
  const n = addedKongCandidates.value.length
  if (n === 0) return
  if (n === 1) {
    fireFromPicker('kong.added', { tile: addedKongCandidates.value[0] })
    return
  }
  picker.value = 'kong.added'
}

/* ---------------------------------------------------------------- 失效保护 */

/**
 * 牌桌视图会随对局推进不断刷新。如果面板还开着但对应的动作已经不在
 * actions 里(超时自动过、被更高优先级截走、轮次已过), 必须立刻关闭,
 * 否则玩家点下去发出的是一次非法动作 —— 表现就是"点了没反应"。
 */
watch(
  () => props.actions.join(','),
  () => {
    const a = props.actions
    if (picker.value === 'chow' && !a.includes('chow')) picker.value = null
    if (picker.value === 'kong.concealed' && !a.includes('kong.concealed')) picker.value = null
    if (picker.value === 'kong.added' && !a.includes('kong.added')) picker.value = null
    // 候选本身变了(例如刚摸到第 4 张)也要收起, 避免展示过期组合。
    if (picker.value === 'chow' && chowCandidates.value.length <= 1) picker.value = null
    if (picker.value === 'kong.concealed' && concealedKongCandidates.value.length <= 1) picker.value = null
    if (picker.value === 'kong.added' && addedKongCandidates.value.length <= 1) picker.value = null
    // 面板开着时倒计时归零 → 动作已由引擎接管, 收起面板。
    if (picker.value && typeof props.secondsLeft === 'number' && props.secondsLeft <= 0) {
      picker.value = null
    }
  },
)

/** 响应窗口彻底结束(没有可执行动作)时也收起。 */
watch(
  () => props.actions.length,
  (n) => {
    if (n === 0) picker.value = null
  },
)

const isBusy = computed(() => props.disabled || submitting.value)
</script>

<template>
  <div class="action-controls relative inline-flex flex-wrap items-center justify-center gap-2">
    <!-- 倒计时提示: 超时后引擎会自动"过"或自动打牌 -->
    <div
      v-if="typeof secondsLeft === 'number' && secondsLeft >= 0 && actions.length > 0"
      class="flex items-center gap-1 rounded-full border px-3 py-1 text-sm font-bold tabular-nums shadow-sm"
      :class="secondsLeft <= 2
        ? 'border-rose-400/70 bg-rose-950/80 text-rose-200 animate-pulse'
        : 'border-amber-400/50 bg-stone-900/80 text-amber-200'"
      :title="`剩余 ${secondsLeft} 秒, 超时将自动${actions.includes('discard') ? '打出' : '过牌'}`"
    >
      <span aria-hidden="true">⏱</span>
      <span>{{ secondsLeft }}s</span>
    </div>

    <!-- 摸牌 -->
    <button
      v-if="actions.includes('draw')"
      type="button"
      :disabled="isBusy"
      class="flex items-center gap-1 rounded-lg bg-gradient-to-r from-emerald-600 to-teal-700 px-4 py-2 font-bold text-white shadow-lg transition hover:scale-105 active:scale-95 disabled:opacity-50"
      @click="fire('draw')"
    >
      <span>摸牌</span>
    </button>

    <!-- 未启用响应倒计时: 下家用「摸牌」直接收敛响应窗口 -->
    <button
      v-if="forceDraw && !actions.includes('draw')"
      type="button"
      :disabled="isBusy"
      class="flex items-center gap-1 rounded-lg bg-gradient-to-r from-emerald-600 to-teal-700 px-4 py-2 font-bold text-white shadow-lg transition hover:scale-105 active:scale-95 disabled:opacity-50"
      title="其他人都不碰不吃时, 直接摸牌继续"
      @click="fire('forcedraw')"
    >
      <span>摸牌</span>
    </button>

    <!-- 自摸和牌 -->
    <button
      v-if="actions.includes('hu.self')"
      type="button"
      :disabled="isBusy"
      class="flex items-center gap-1 rounded-lg bg-gradient-to-r from-rose-600 to-red-700 px-5 py-2 font-black text-white shadow-lg ring-2 ring-amber-300 transition hover:scale-105 active:scale-95 animate-bounce disabled:opacity-50"
      @click="fire('hu.self')"
    >
      <span>🀄 自摸！</span>
    </button>

    <!-- 荣和 (点炮胡) -->
    <button
      v-if="actions.includes('hu.robbing')"
      type="button"
      :disabled="isBusy"
      class="flex items-center gap-1 rounded-lg bg-gradient-to-r from-rose-600 to-red-700 px-5 py-2 font-black text-white shadow-lg ring-2 ring-amber-300 transition hover:scale-105 active:scale-95 animate-bounce disabled:opacity-50"
      @click="fire('hu.robbing')"
    >
      <span>🀄 和牌！</span>
    </button>

    <!-- 碰牌 -->
    <button
      v-if="actions.includes('pong')"
      type="button"
      :disabled="isBusy"
      class="rounded-lg bg-gradient-to-r from-amber-500 to-orange-600 px-4 py-2 font-bold text-white shadow-md transition hover:scale-105 active:scale-95 disabled:opacity-50"
      @click="fire('pong')"
    >
      碰
    </button>

    <!-- 吃牌 (多组候选时展开选择面板) -->
    <button
      v-if="actions.includes('chow')"
      type="button"
      :disabled="isBusy"
      class="flex items-center gap-1 rounded-lg bg-gradient-to-r from-amber-600 to-yellow-600 px-4 py-2 font-bold text-white shadow-md transition hover:scale-105 active:scale-95 disabled:opacity-50"
      @click="onChowClick"
    >
      吃
      <span v-if="chowCandidates.length > 1" class="text-xs opacity-80">({{ chowCandidates.length }})</span>
    </button>

    <!-- 明杠 -->
    <button
      v-if="actions.includes('kong.exposed')"
      type="button"
      :disabled="isBusy"
      class="rounded-lg bg-gradient-to-r from-purple-600 to-indigo-700 px-4 py-2 font-bold text-white shadow-md transition hover:scale-105 active:scale-95 disabled:opacity-50"
      @click="fire('kong.exposed')"
    >
      杠
    </button>

    <!-- 暗杠 -->
    <button
      v-if="actions.includes('kong.concealed')"
      type="button"
      :disabled="isBusy"
      class="flex items-center gap-1 rounded-lg bg-gradient-to-r from-purple-600 to-indigo-700 px-4 py-2 font-bold text-white shadow-md transition hover:scale-105 active:scale-95 disabled:opacity-50"
      @click="onConcealedKongClick"
    >
      暗杠
      <span v-if="concealedKongCandidates.length > 1" class="text-xs opacity-80">
        ({{ concealedKongCandidates.length }})
      </span>
    </button>

    <!-- 补杠 -->
    <button
      v-if="actions.includes('kong.added')"
      type="button"
      :disabled="isBusy"
      class="flex items-center gap-1 rounded-lg bg-gradient-to-r from-purple-600 to-indigo-700 px-4 py-2 font-bold text-white shadow-md transition hover:scale-105 active:scale-95 disabled:opacity-50"
      @click="onAddedKongClick"
    >
      补杠
      <span v-if="addedKongCandidates.length > 1" class="text-xs opacity-80">
        ({{ addedKongCandidates.length }})
      </span>
    </button>

    <!-- 过 (响应窗口中拒绝动作) -->
    <button
      v-if="actions.includes('pass')"
      type="button"
      :disabled="isBusy"
      class="rounded-lg border border-stone-500 bg-stone-800/80 px-4 py-2 font-semibold text-stone-200 shadow-sm transition hover:bg-stone-700 active:scale-95 disabled:opacity-50"
      @click="fire('pass')"
    >
      过
    </button>

    <!-- 多选副露面板: Teleport 到 body, 避免被牌桌的 overflow-hidden 裁掉 -->
    <Teleport to="body">
      <div
        v-if="picker"
        class="fixed inset-0 z-50 flex items-end justify-center bg-black/60 p-4 backdrop-blur-sm sm:items-center"
        @click.self="closePicker"
      >
        <div
          class="w-full max-w-md rounded-2xl border border-amber-500/50 bg-stone-900/98 p-4 text-white shadow-2xl"
          role="dialog"
          aria-modal="true"
        >
          <div class="mb-3 flex items-center justify-between border-b border-stone-700 pb-2">
            <div>
              <div class="text-sm font-bold text-amber-300">{{ pickerTitle }}</div>
              <div class="mt-0.5 text-[11px] text-stone-400">
                点击一项立即执行 · 共 {{ options.length }} 种选择
              </div>
            </div>
            <div class="flex items-center gap-2">
              <!-- 面板开着时倒计时仍在走: 明确提示, 免得选到一半被自动过牌 -->
              <span
                v-if="typeof secondsLeft === 'number' && secondsLeft >= 0"
                class="rounded-full border px-2 py-0.5 text-xs font-bold tabular-nums"
                :class="secondsLeft <= 2
                  ? 'border-rose-400/70 bg-rose-950/80 text-rose-200 animate-pulse'
                  : 'border-amber-400/50 bg-stone-800 text-amber-200'"
              >
                ⏱ {{ secondsLeft }}s
              </span>
              <button
                type="button"
                class="rounded-lg px-2 py-1 text-stone-400 transition hover:bg-stone-800 hover:text-white"
                aria-label="关闭"
                @click="closePicker"
              >
                ✕
              </button>
            </div>
          </div>

          <div class="flex max-h-[60vh] flex-col gap-2 overflow-y-auto">
            <button
              v-for="opt in options"
              :key="opt.id"
              type="button"
              :disabled="isBusy"
              class="flex w-full items-center gap-3 rounded-xl border border-stone-700 bg-stone-800/80 p-2.5 text-left transition hover:border-amber-400 hover:bg-stone-700 active:scale-[0.98] disabled:opacity-50"
              @click="choose(opt)"
            >
              <span class="flex shrink-0 items-end gap-0.5">
                <MahjongTile
                  v-for="(t, ti) in opt.tiles"
                  :key="opt.id + '-t' + ti"
                  :code="t"
                  size="sm"
                />
              </span>
              <span class="min-w-0">
                <span class="block text-sm font-bold text-amber-100">{{ opt.label }}</span>
                <span v-if="opt.hint" class="mt-0.5 block truncate text-[11px] text-stone-400">
                  {{ opt.hint }}
                </span>
              </span>
            </button>
          </div>

          <button
            type="button"
            class="mt-3 w-full rounded-xl border border-stone-600 bg-stone-800/60 py-2 text-sm font-semibold text-stone-300 transition hover:bg-stone-700 active:scale-95"
            @click="closePicker"
          >
            取消
          </button>
        </div>
      </div>
    </Teleport>
  </div>
</template>
