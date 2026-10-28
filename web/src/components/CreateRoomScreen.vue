<script setup lang="ts">
/**
 * 创建房间设置界面（项目核心）
 *
 * 布局: 预设下拉 → 人数 / 联机方式 / 房间名 / 密码 → 牌池概览 + 自定义麻将牌 → 创建并进入等待。
 */
import { computed, ref, watch } from 'vue'
import {
  COUNT_OPTIONS,
  PLAYER_COUNTS,
  RULE_PRESETS,
  TRANSPORT_OPTIONS,
  buildPool,
  clonePool,
  findPreset,
  poolSummary,
  presetRuleOptions,
  type RuleOptions,
  type RulePreset,
  type TilePoolSpec,
  type TransportMode,
} from '../config/rules'
import TilePoolModal from './TilePoolModal.vue'

const props = defineProps<{ busy?: boolean; errorText?: string }>()

const emit = defineEmits<{
  (e: 'create', opts: {
    name: string
    password: string
    players: number
    presetId: string
    transport: TransportMode
    pool: TilePoolSpec
    rules: RuleOptions
  }): void
  (e: 'back'): void
}>()

const presetId = ref<RulePreset['id']>('jinan')
const players = ref<number>(4)
const transport = ref<TransportMode>('server')
const roomName = ref('')
const password = ref('')
const showTileModal = ref(false)
/** 用户改过的牌池; null = 用预设默认 */
const customPool = ref<TilePoolSpec | null>(null)

/** 基本牌集: 花色 / 字牌 / 每种张数（用户在预设之上直接选） */
const base = ref<{ suits: Record<'wan' | 'tiao' | 'tong', boolean>; honors: boolean; count: number }>({
  suits: { wan: true, tiao: true, tong: true },
  honors: true,
  count: 4,
})

const preset = computed(() => findPreset(presetId.value))
const pool = computed<TilePoolSpec>(() => customPool.value ?? preset.value.pool)
const summary = computed(() => poolSummary(pool.value))
const isCustomized = computed(() => customPool.value !== null)
/** 开局最少需要的张数: 每人 13 张 + 庄家第 14 张 */
const minTiles = computed(() => 13 * players.value + 1)
const poolTooSmall = computed(() => summary.value.total < minTiles.value)
const baseIsPreset = computed(() => {
  const p = preset.value.pool
  const n = summary.value
  return n.total === poolSummary(p).total
})
const countsUsed = computed(() => {
  const found = new Set<number>()
  for (const v of Object.values(pool.value.kinds)) found.add(v)
  return [...found]
})

const showPassword = ref(false)

// ---- 更多规则设置(二级面板) ----
const showAdvanced = ref(false)
/** 用户改过的细则; null = 跟随预设默认 */
const customRules = ref<RuleOptions | null>(null)
const rules = computed<RuleOptions>(() => customRules.value ?? presetRuleOptions(preset.value))
const rulesCustomized = computed(() => customRules.value !== null)

/** 二级面板里的分类: 计时 / 优先级与截牌 / 和牌条件 */
const advancedTabs = [
  { id: 'timer', label: '计时', hint: '响应与出牌倒计时' },
  { id: 'priority', label: '优先级', hint: '截胡 / 截碰' },
  { id: 'win', label: '和牌', hint: '吃 / 七对 / 自摸 / 点炮 / 时序番' },
] as const
const activeTab = ref<'timer' | 'priority' | 'win'>('timer')

function patchRules(patch: Partial<RuleOptions>): void {
  customRules.value = { ...rules.value, ...patch }
}

function resetRules(): void {
  customRules.value = null
}

/** 计时档位: 0 表示关闭(不设倒计时) */
const MELD_SEC_OPTIONS = [0, 3, 5, 8, 10, 15, 20, 30]
const WIN_SEC_OPTIONS = [0, 3, 5, 8, 10, 15, 20, 30]
const DISCARD_SEC_OPTIONS = [0, 5, 8, 10, 15, 20, 30, 45, 60, 90, 120]

function secLabel(v: number): string {
  return v === 0 ? '关闭' : `${v}s`
}

function onPresetChange(): void {
  // 切换预设 = 回到该预设的默认牌池与默认细则（用户可再次自定义）
  customPool.value = null
  customRules.value = null
  base.value = { suits: { wan: true, tiao: true, tong: true }, honors: true, count: 4 }
}

/** 基本牌集变化时, 直接按「花色 + 字牌 + 每种张数」重建牌池。 */
function applyBase(): void {
  const suits = (Object.keys(base.value.suits) as Array<'wan' | 'tiao' | 'tong'>).filter(
    (s) => base.value.suits[s],
  )
  if (suits.length === 0 && !base.value.honors) {
    // 至少留一种牌, 否则开局必然失败
    base.value.suits.wan = true
    return
  }
  customPool.value = buildPool({
    suits,
    honors: base.value.honors,
    count: base.value.count,
  })
}

function toggleSuit(s: 'wan' | 'tiao' | 'tong'): void {
  base.value.suits[s] = !base.value.suits[s]
  applyBase()
}

function toggleHonors(): void {
  base.value.honors = !base.value.honors
  applyBase()
}

function setCount(n: number): void {
  base.value.count = n
  applyBase()
}

/** 从当前牌池反推「基本牌集」的选中态(用于显示, 不改变数据)。 */
function suitPresent(s: 'wan' | 'tiao' | 'tong'): boolean {
  return Object.keys(pool.value.kinds).some((k) => k.startsWith(s))
}

watch(presetId, () => {
  // 预设变化时同步基本牌集的选中态显示
  base.value.suits = {
    wan: suitPresent('wan'),
    tiao: suitPresent('tiao'),
    tong: suitPresent('tong'),
  }
  base.value.honors = Object.keys(pool.value.kinds).some((k) => HONOR_SET.has(k))
})

const HONOR_SET = new Set(['dong', 'nan', 'xi', 'bei', 'zhong', 'fa', 'bai'])

function onPoolUpdated(next: TilePoolSpec): void {
  customPool.value = next
}

function resetPool(): void {
  customPool.value = null
}

function submit(): void {
  if (props.busy) return
  emit('create', {
    name: roomName.value.trim() || `${preset.value.name} 房间`,
    password: password.value,
    players: players.value,
    presetId: presetId.value,
    transport: transport.value,
    pool: clonePool(pool.value),
    rules: { ...rules.value },
  })
}
</script>

<template>
  <div class="mx-auto w-full max-w-2xl px-4 py-8">
    <button class="mb-4 text-sm text-stone-500 hover:text-stone-700" @click="emit('back')">← 返回</button>

    <div class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
      <h1 class="text-xl font-semibold text-stone-800">创建房间</h1>
      <p class="mt-1 text-sm text-stone-500">先选玩法预设，再按需改牌池；规则始终跟随预设。</p>

      <!-- 预设 -->
      <div class="mt-5">
        <label class="text-sm font-medium text-stone-700">规则预设</label>
        <select
          v-model="presetId"
          class="mt-1.5 w-full rounded-xl border border-stone-300 bg-white px-3 py-2.5 text-sm text-stone-800 focus:border-emerald-500 focus:outline-none"
          @change="onPresetChange"
        >
          <option v-for="p in RULE_PRESETS" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
        <p class="mt-1.5 text-xs text-stone-500">{{ preset.summary }}</p>
        <ul class="mt-2 space-y-1 rounded-xl bg-stone-50 p-3 text-xs text-stone-600">
          <li v-for="(n, i) in preset.notes" :key="i">· {{ n }}</li>
        </ul>
      </div>

      <!-- 人数 + 联机方式 -->
      <div class="mt-5 grid gap-4 sm:grid-cols-2">
        <div>
          <label class="text-sm font-medium text-stone-700">房间人数</label>
          <div class="mt-1.5 flex gap-2">
            <button
              v-for="n in PLAYER_COUNTS"
              :key="n"
              class="flex-1 rounded-xl border px-3 py-2 text-sm transition"
              :class="players === n ? 'border-emerald-500 bg-emerald-50 font-medium text-emerald-700' : 'border-stone-300 text-stone-600 hover:bg-stone-50'"
              @click="players = n"
            >
              {{ n }} 人
            </button>
          </div>
          <p class="mt-1 text-[11px] leading-tight text-stone-400">2/3/4 人均可开局（每人 13 张）</p>
        </div>
        <div>
          <label class="text-sm font-medium text-stone-700">联机方式</label>
          <div class="mt-1.5 grid gap-1.5">
            <button
              v-for="t in TRANSPORT_OPTIONS"
              :key="t.id"
              class="rounded-xl border px-3 py-2 text-left text-sm transition"
              :class="transport === t.id ? 'border-emerald-500 bg-emerald-50 text-emerald-800' : 'border-stone-300 text-stone-600 hover:bg-stone-50'"
              @click="transport = t.id"
            >
              <span class="font-medium">{{ t.label }}</span>
              <span class="mt-0.5 block text-[11px] leading-tight text-stone-500">{{ t.hint }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- 房间名 / 密码 -->
      <div class="mt-5 grid gap-4 sm:grid-cols-2">
        <div>
          <label class="text-sm font-medium text-stone-700">房间名</label>
          <input
            v-model="roomName"
            class="mt-1.5 w-full rounded-xl border border-stone-300 px-3 py-2.5 text-sm focus:border-emerald-500 focus:outline-none"
            :placeholder="`${preset.name} 房间`"
            maxlength="24"
          />
        </div>
        <div>
          <label class="flex items-center justify-between text-sm font-medium text-stone-700">
            <span>房间密码</span>
            <span class="cursor-pointer text-xs font-normal text-emerald-600" @click="showPassword = !showPassword">
              {{ showPassword ? '收起' : '设置密码' }}
            </span>
          </label>
          <input
            v-if="showPassword"
            v-model="password"
            type="text"
            class="mt-1.5 w-full rounded-xl border border-stone-300 px-3 py-2.5 text-sm focus:border-emerald-500 focus:outline-none"
            placeholder="可选，留空表示无密码"
            maxlength="32"
          />
          <p v-else class="mt-1.5 text-xs text-stone-400">未设置密码，凭房间码即可加入</p>
        </div>
      </div>

      <!-- 更多规则设置(二级面板) -->
      <div class="mt-5 rounded-2xl border border-stone-200 bg-stone-50/60">
        <button
          type="button"
          class="flex w-full items-center justify-between gap-2 px-4 py-3 text-left"
          :aria-expanded="showAdvanced"
          @click="showAdvanced = !showAdvanced"
        >
          <span>
            <span class="text-sm font-semibold text-stone-700">更多规则设置</span>
            <span class="ml-2 rounded bg-stone-200 px-1.5 py-0.5 text-[11px] text-stone-600">
              碰 {{ secLabel(rules.respond_meld_sec) }} · 胡 {{ secLabel(rules.respond_win_sec) }} ·
              出牌 {{ secLabel(rules.discard_sec) }}
            </span>
            <span
              v-if="rulesCustomized"
              class="ml-1 rounded bg-amber-100 px-1.5 py-0.5 text-[11px] text-amber-700"
            >已调整</span>
            <span class="mt-1 block text-xs text-stone-500">
              计时、优先级仲裁与和牌条件；不改动时跟随预设默认。
            </span>
          </span>
          <span class="shrink-0 text-xs text-stone-500">{{ showAdvanced ? '收起 ▲' : '展开 ▼' }}</span>
        </button>

        <div v-if="showAdvanced" class="border-t border-stone-200 px-4 py-3">
          <!-- 二级分类 -->
          <div class="flex flex-wrap gap-1.5">
            <button
              v-for="t in advancedTabs"
              :key="t.id"
              type="button"
              class="rounded-lg border px-2.5 py-1.5 text-xs transition"
              :class="activeTab === t.id
                ? 'border-emerald-500 bg-emerald-50 font-medium text-emerald-700'
                : 'border-stone-300 text-stone-600 hover:bg-stone-100'"
              :title="t.hint"
              @click="activeTab = t.id"
            >
              {{ t.label }}
            </button>
            <button
              v-if="rulesCustomized"
              type="button"
              class="ml-auto rounded-lg border border-stone-300 px-2.5 py-1.5 text-xs text-stone-500 hover:bg-stone-100"
              @click="resetRules"
            >
              恢复预设默认
            </button>
          </div>

          <!-- 计时 -->
          <div v-if="activeTab === 'timer'" class="mt-3 space-y-3">
            <div>
              <div class="flex items-baseline justify-between">
                <span class="text-xs font-medium text-stone-700">碰 / 杠 / 吃 响应倒计时</span>
                <span class="text-[11px] text-stone-400">{{ secLabel(rules.respond_meld_sec) }}</span>
              </div>
              <div class="mt-1.5 flex flex-wrap gap-1.5">
                <button
                  v-for="v in MELD_SEC_OPTIONS"
                  :key="'meld-' + v"
                  type="button"
                  class="rounded-lg border px-2 py-1 text-xs transition"
                  :class="rules.respond_meld_sec === v
                    ? 'border-emerald-500 bg-emerald-50 text-emerald-700'
                    : 'border-stone-300 text-stone-600 hover:bg-stone-100'"
                  @click="patchRules({ respond_meld_sec: v })"
                >
                  {{ secLabel(v) }}
                </button>
              </div>
              <p class="mt-1 text-[11px] leading-tight text-stone-500">
                超时自动「过」；关闭后改为由出牌者的下家点「摸牌」推进。
              </p>
            </div>
            <div>
              <div class="flex items-baseline justify-between">
                <span class="text-xs font-medium text-stone-700">胡（荣和）响应倒计时</span>
                <span class="text-[11px] text-stone-400">{{ secLabel(rules.respond_win_sec) }}</span>
              </div>
              <div class="mt-1.5 flex flex-wrap gap-1.5">
                <button
                  v-for="v in WIN_SEC_OPTIONS"
                  :key="'win-' + v"
                  type="button"
                  class="rounded-lg border px-2 py-1 text-xs transition"
                  :class="rules.respond_win_sec === v
                    ? 'border-emerald-500 bg-emerald-50 text-emerald-700'
                    : 'border-stone-300 text-stone-600 hover:bg-stone-100'"
                  @click="patchRules({ respond_win_sec: v })"
                >
                  {{ secLabel(v) }}
                </button>
              </div>
              <p class="mt-1 text-[11px] leading-tight text-stone-500">
                同一家既能胡又能碰时，按其中较长的倒计时计时。
              </p>
            </div>
            <div>
              <div class="flex items-baseline justify-between">
                <span class="text-xs font-medium text-stone-700">出牌倒计时</span>
                <span class="text-[11px] text-stone-400">{{ secLabel(rules.discard_sec) }}</span>
              </div>
              <div class="mt-1.5 flex flex-wrap gap-1.5">
                <button
                  v-for="v in DISCARD_SEC_OPTIONS"
                  :key="'discard-' + v"
                  type="button"
                  class="rounded-lg border px-2 py-1 text-xs transition"
                  :class="rules.discard_sec === v
                    ? 'border-emerald-500 bg-emerald-50 text-emerald-700'
                    : 'border-stone-300 text-stone-600 hover:bg-stone-100'"
                  @click="patchRules({ discard_sec: v })"
                >
                  {{ secLabel(v) }}
                </button>
              </div>
              <p class="mt-1 text-[11px] leading-tight text-stone-500">
                超时自动打出刚摸的牌（没有则打最右一张）；其他人会看到「等待 X 出牌」。
              </p>
            </div>
          </div>

          <!-- 优先级与截牌 -->
          <div v-else-if="activeTab === 'priority'" class="mt-3 space-y-2.5">
            <p class="rounded-lg bg-stone-100 px-2.5 py-1.5 text-[11px] leading-tight text-stone-600">
              基础优先级固定为 <b>胡 &gt; 碰/杠 &gt; 吃</b>。下面的开关决定同一优先级有多家同时要时怎么判。
            </p>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.rob_win"
                @change="patchRules({ rob_win: ($event.target as HTMLInputElement).checked })"
              />
              <span>
                <span class="text-xs font-medium text-stone-700">截胡（按出牌顺序优先）</span>
                <span class="mt-0.5 block text-[11px] leading-tight text-stone-500">
                  开：多人可胡时由离出牌者最近的一家先胡；关：谁先点「胡」谁得。
                </span>
              </span>
            </label>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.rob_meld"
                @change="patchRules({ rob_meld: ($event.target as HTMLInputElement).checked })"
              />
              <span>
                <span class="text-xs font-medium text-stone-700">截碰 / 截杠（按出牌顺序优先）</span>
                <span class="mt-0.5 block text-[11px] leading-tight text-stone-500">
                  开：多人可碰杠时按座次优先；关：先按者得。
                </span>
              </span>
            </label>
            <p class="text-[11px] leading-tight text-stone-500">
              判定期间牌桌会显示「判定中」，出结果后显示「判定成功 / 判定失败」。
            </p>
          </div>

          <!-- 和牌条件 -->
          <div v-else class="mt-3 space-y-2.5">
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.chow"
                @change="patchRules({ chow: ($event.target as HTMLInputElement).checked })"
              />
              <span class="text-xs font-medium text-stone-700">允许吃牌</span>
            </label>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.seven_pairs"
                @change="patchRules({ seven_pairs: ($event.target as HTMLInputElement).checked })"
              />
              <span class="text-xs font-medium text-stone-700">七对可和</span>
            </label>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.self_draw"
                @change="patchRules({ self_draw: ($event.target as HTMLInputElement).checked })"
              />
              <span class="text-xs font-medium text-stone-700">允许自摸</span>
            </label>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.discard_win"
                @change="patchRules({ discard_win: ($event.target as HTMLInputElement).checked })"
              />
              <span class="text-xs font-medium text-stone-700">允许点炮胡</span>
            </label>

            <p class="rounded-lg bg-stone-100 px-2.5 py-1.5 text-[11px] leading-tight text-stone-600">
              时序番型：在特定时机和牌额外 +2 番，同时只计最高的一个。
            </p>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.rob_kong_win"
                @change="patchRules({ rob_kong_win: ($event.target as HTMLInputElement).checked })"
              />
              <span>
                <span class="text-xs font-medium text-stone-700">抢杠胡</span>
                <span class="mt-0.5 block text-[11px] leading-tight text-stone-500">
                  开：别人补杠的那张牌正好是你要和的牌时可以抢；关：补杠不可被抢。
                </span>
              </span>
            </label>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.kong_flower"
                @change="patchRules({ kong_flower: ($event.target as HTMLInputElement).checked })"
              />
              <span>
                <span class="text-xs font-medium text-stone-700">杠上开花</span>
                <span class="mt-0.5 block text-[11px] leading-tight text-stone-500">
                  开：杠后补的那张牌正好自摸，额外 +2 番。
                </span>
              </span>
            </label>
            <label class="flex cursor-pointer items-start gap-2 rounded-lg bg-white p-2.5">
              <input
                type="checkbox"
                class="mt-0.5"
                :checked="rules.last_tile"
                @change="patchRules({ last_tile: ($event.target as HTMLInputElement).checked })"
              />
              <span>
                <span class="text-xs font-medium text-stone-700">海底捞月 / 河底捞鱼</span>
                <span class="mt-0.5 block text-[11px] leading-tight text-stone-500">
                  开：牌墙最后一张自摸（海底捞月）或最后一张打出的牌被和（河底捞鱼）额外 +2 番。
                </span>
              </span>
            </label>

            <div class="rounded-lg bg-white p-2.5">
              <div class="flex items-baseline justify-between">
                <span class="text-xs font-medium text-stone-700">底分</span>
                <span class="text-[11px] text-stone-400">{{ rules.base_score }}</span>
              </div>
              <input
                :value="rules.base_score"
                type="range"
                min="1"
                max="20"
                step="1"
                class="mt-1.5 w-full"
                @input="patchRules({ base_score: Number(($event.target as HTMLInputElement).value) })"
              />
            </div>
          </div>
        </div>
      </div>

      <!-- 牌池 -->
      <div class="mt-6 rounded-2xl border border-stone-200 bg-stone-50/60 p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 class="text-sm font-semibold text-stone-700">麻将牌</h2>
            <p class="mt-0.5 text-xs text-stone-500">
              共 <b class="text-stone-800">{{ summary.total }}</b> 张 ·
              数牌 {{ summary.suits }} · 字牌 {{ summary.honors }} · 花牌 {{ summary.flowers }} · 百搭 {{ summary.jokers }}
              <span v-if="isCustomized" class="ml-1 rounded bg-amber-100 px-1.5 py-0.5 text-[11px] text-amber-700">已自定义</span>
            </p>
          </div>
          <button
            class="rounded-xl border border-stone-300 bg-white px-3.5 py-2 text-sm text-stone-700 hover:bg-stone-100"
            @click="showTileModal = true"
          >
            自定义麻将牌
          </button>
        </div>
        <div v-if="isCustomized" class="mt-2">
          <button class="text-xs text-stone-500 underline hover:text-stone-700" @click="resetPool">
            恢复「{{ preset.name }}」预设牌池
          </button>
        </div>

        <!-- 基本牌集: 花色 / 字牌 / 每种张数 -->
        <div class="mt-4 border-t border-stone-200 pt-3">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-xs font-medium text-stone-600">基本牌集</span>
            <button
              v-for="s in (['wan', 'tiao', 'tong'] as const)"
              :key="s"
              class="rounded-lg border px-2.5 py-1 text-xs transition"
              :class="suitPresent(s) ? 'border-emerald-500 bg-emerald-50 text-emerald-700' : 'border-stone-300 text-stone-500 hover:bg-stone-100'"
              @click="toggleSuit(s)"
            >
              {{ s === 'wan' ? '万' : s === 'tiao' ? '条' : '筒' }}
            </button>
            <button
              class="rounded-lg border px-2.5 py-1 text-xs transition"
              :class="Object.keys(pool.kinds).some((k) => HONOR_SET.has(k)) ? 'border-emerald-500 bg-emerald-50 text-emerald-700' : 'border-stone-300 text-stone-500 hover:bg-stone-100'"
              @click="toggleHonors"
            >
              字牌
            </button>
            <span class="ml-1 text-xs text-stone-500">每种</span>
            <button
              v-for="c in COUNT_OPTIONS"
              :key="c"
              class="rounded-lg border px-2 py-1 text-xs transition"
              :class="countsUsed.length === 1 && countsUsed[0] === c ? 'border-emerald-500 bg-emerald-50 text-emerald-700' : 'border-stone-300 text-stone-500 hover:bg-stone-100'"
              @click="setCount(c)"
            >
              {{ c }} 张
            </button>
          </div>
          <p class="mt-2 text-[11px] leading-tight" :class="poolTooSmall ? 'text-red-600' : 'text-stone-500'">
            共 {{ summary.total }} 张 · {{ players }} 人开局至少需要 {{ minTiles }} 张
            <span v-if="poolTooSmall"> · 牌太少，请加花色或加张数</span>
            <span v-else-if="baseIsPreset" class="text-stone-400"> · 与预设一致</span>
          </p>
        </div>
      </div>

      <p v-if="props.errorText" class="mt-4 rounded-xl bg-red-50 px-3 py-2 text-sm text-red-600">{{ props.errorText }}</p>

      <button
        class="mt-6 w-full rounded-xl bg-emerald-600 px-4 py-3 text-base font-medium text-white transition hover:bg-emerald-700 disabled:opacity-60"
        :disabled="props.busy || poolTooSmall"
        @click="submit"
      >
        {{ props.busy ? '正在创建…' : poolTooSmall ? `牌太少（需 ${minTiles} 张）` : '创建房间' }}
      </button>
    </div>

    <TilePoolModal
      v-if="showTileModal"
      :preset-name="preset.name"
      :preset-pool="preset.pool"
      :model-value="pool"
      @update:model-value="onPoolUpdated"
      @close="showTileModal = false"
    />
  </div>
</template>
