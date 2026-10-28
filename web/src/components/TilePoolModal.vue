<script setup lang="ts">
/**
 * 自定义麻将牌（二级弹窗）
 *
 * - 预设会把「该规则实际用到的牌」勾上, 并把张数设好;
 * - 用户可以任意改变种类与每种张数(1/2/3/4/8), 规则仍沿用所选预设;
 * - 花牌开启后每种固定 1 张; 百搭分「通用数量 0~8」与「限定百搭种类 + 张数」。
 */
import { computed, ref } from 'vue'
import {
  COUNT_OPTIONS,
  FLOWER_KINDS,
  FLOWER_LABELS,
  HONOR_KINDS,
  HONOR_LABELS,
  SUIT_KINDS,
  clonePool,
  poolSummary,
  type TilePoolSpec,
} from '../config/rules'

const props = defineProps<{
  presetName: string
  /** 当前牌池（打开弹窗时的编辑起点） */
  modelValue: TilePoolSpec
  /** 预设默认牌池（用于「恢复预设默认」） */
  presetPool: TilePoolSpec
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', v: TilePoolSpec): void
  (e: 'close'): void
}>()

const draft = ref<TilePoolSpec>(clonePool(props.modelValue))
const tab = ref<'suits' | 'honors' | 'flowers' | 'jokers'>('suits')

const suitGroups = [
  { id: 'wan' as const, label: '万子', color: 'text-red-600' },
  { id: 'tiao' as const, label: '条子', color: 'text-emerald-600' },
  { id: 'tong' as const, label: '饼子', color: 'text-sky-600' },
]

const summary = computed(() => poolSummary(draft.value))
const flowersOn = computed(() => FLOWER_KINDS.some((k) => (draft.value.kinds[k] ?? 0) > 0))

function countOf(kind: string): number {
  return draft.value.kinds[kind] ?? 0
}

function toggle(kind: string): void {
  if (countOf(kind) > 0) delete draft.value.kinds[kind]
  else draft.value.kinds[kind] = 4
}

function setCount(kind: string, n: number): void {
  if (n <= 0) delete draft.value.kinds[kind]
  else draft.value.kinds[kind] = n
}

function toggleFlowers(): void {
  if (flowersOn.value) {
    for (const k of FLOWER_KINDS) delete draft.value.kinds[k]
  } else {
    for (const k of FLOWER_KINDS) draft.value.kinds[k] = 1
  }
}

function bulkSuit(suit: 'wan' | 'tiao' | 'tong'): void {
  const codes = SUIT_KINDS[suit]
  const anyOn = codes.some((c) => countOf(c) > 0)
  for (const c of codes) {
    if (anyOn) delete draft.value.kinds[c]
    else draft.value.kinds[c] = 4
  }
}

function bulkHonors(): void {
  const anyOn = HONOR_KINDS.some((c) => countOf(c) > 0)
  for (const c of HONOR_KINDS) {
    if (anyOn) delete draft.value.kinds[c]
    else draft.value.kinds[c] = 4
  }
}

function setJokers(n: number): void {
  draft.value.jokers = Math.max(0, Math.min(8, n))
}

function isLimitedJoker(kind: string): boolean {
  return (draft.value.limitedJokers?.[kind] ?? 0) > 0
}

function toggleLimitedJoker(kind: string): void {
  const cur = draft.value.limitedJokers ?? {}
  if (cur[kind]) {
    delete cur[kind]
  } else {
    if (countOf(kind) === 0) draft.value.kinds[kind] = 4
    cur[kind] = 1
  }
  draft.value.limitedJokers = Object.keys(cur).length ? cur : undefined
}

function setLimitedJokerCount(kind: string, n: number): void {
  const cur = draft.value.limitedJokers ?? {}
  if (n <= 0) delete cur[kind]
  else cur[kind] = n
  draft.value.limitedJokers = Object.keys(cur).length ? cur : undefined
}

function restorePreset(): void {
  draft.value = clonePool(props.presetPool)
}

function confirm(): void {
  emit('update:modelValue', clonePool(draft.value))
  emit('close')
}

const honorTiles = ['dong', 'nan', 'xi', 'bei', 'zhong', 'fa', 'bai']
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-stone-900/40 p-4 backdrop-blur-sm">
    <div class="flex max-h-[88vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-stone-200 bg-white shadow-2xl">
      <!-- 标题 -->
      <header class="flex items-center justify-between border-b border-stone-100 px-6 py-4">
        <div>
          <h2 class="text-lg font-semibold text-stone-800">自定义麻将牌</h2>
          <p class="mt-0.5 text-xs text-stone-500">
            预设「{{ presetName }}」已勾选对应牌种；修改种类或张数后，规则仍按该预设执行。
          </p>
        </div>
        <button class="rounded-lg px-2 py-1 text-stone-400 hover:bg-stone-100 hover:text-stone-600" @click="emit('close')">✕</button>
      </header>

      <!-- 页签 -->
      <nav class="flex gap-1 border-b border-stone-100 px-4 pt-3">
        <button
          v-for="t in [
            { id: 'suits', label: '数牌' },
            { id: 'honors', label: '字牌' },
            { id: 'flowers', label: '花牌' },
            { id: 'jokers', label: '百搭' },
          ]"
          :key="t.id"
          class="rounded-t-lg px-4 py-2 text-sm font-medium transition"
          :class="tab === t.id ? 'bg-emerald-50 text-emerald-700' : 'text-stone-500 hover:bg-stone-50'"
          @click="tab = t.id as any"
        >
          {{ t.label }}
        </button>
        <div class="ml-auto self-center pb-2 text-xs text-stone-500">
          合计 <b class="text-stone-800">{{ summary.total }}</b> 张
          <span class="mx-1 text-stone-300">|</span>
          数牌 {{ summary.suits }} · 字牌 {{ summary.honors }} · 花牌 {{ summary.flowers }} · 百搭 {{ summary.jokers }}
        </div>
      </nav>

      <!-- 内容 -->
      <div class="min-h-[18rem] flex-1 overflow-y-auto px-6 py-4">
        <!-- 数牌 -->
        <div v-if="tab === 'suits'" class="space-y-5">
          <section v-for="g in suitGroups" :key="g.id">
            <div class="mb-2 flex items-center justify-between">
              <h3 class="text-sm font-semibold" :class="g.color">{{ g.label }}</h3>
              <button class="rounded-lg border border-stone-200 px-2 py-1 text-xs text-stone-600 hover:bg-stone-50" @click="bulkSuit(g.id)">
                全选/全不选
              </button>
            </div>
            <div class="grid grid-cols-3 gap-2 sm:grid-cols-5">
              <label
                v-for="(code, i) in SUIT_KINDS[g.id]"
                :key="code"
                class="flex items-center gap-2 rounded-xl border px-3 py-2 text-sm transition"
                :class="countOf(code) > 0 ? 'border-emerald-300 bg-emerald-50/60' : 'border-stone-200 bg-white'"
              >
                <input
                  type="checkbox"
                  class="h-4 w-4 accent-emerald-600"
                  :checked="countOf(code) > 0"
                  @change="toggle(code)"
                />
                <span class="min-w-8 text-stone-700">{{ i + 1 }}{{ g.label[0] }}</span>
                <select
                  class="ml-auto rounded-md border border-stone-200 bg-white px-1 py-0.5 text-xs text-stone-700 disabled:opacity-40"
                  :disabled="countOf(code) === 0"
                  :value="countOf(code) || 4"
                  @change="setCount(code, Number(($event.target as HTMLSelectElement).value))"
                >
                  <option v-for="n in COUNT_OPTIONS" :key="n" :value="n">{{ n }} 张</option>
                </select>
              </label>
            </div>
          </section>
        </div>

        <!-- 字牌 -->
        <div v-else-if="tab === 'honors'" class="space-y-4">
          <div class="flex items-center justify-between">
            <h3 class="text-sm font-semibold text-violet-700">风牌 / 箭牌</h3>
            <button class="rounded-lg border border-stone-200 px-2 py-1 text-xs text-stone-600 hover:bg-stone-50" @click="bulkHonors">
              全选/全不选
            </button>
          </div>
          <div class="grid grid-cols-3 gap-2 sm:grid-cols-4">
            <label
              v-for="code in honorTiles"
              :key="code"
              class="flex items-center gap-2 rounded-xl border px-3 py-2 text-sm transition"
              :class="countOf(code) > 0 ? 'border-emerald-300 bg-emerald-50/60' : 'border-stone-200 bg-white'"
            >
              <input type="checkbox" class="h-4 w-4 accent-emerald-600" :checked="countOf(code) > 0" @change="toggle(code)" />
              <span class="text-stone-700">{{ HONOR_LABELS[code] }}</span>
              <select
                class="ml-auto rounded-md border border-stone-200 bg-white px-1 py-0.5 text-xs text-stone-700 disabled:opacity-40"
                :disabled="countOf(code) === 0"
                :value="countOf(code) || 4"
                @change="setCount(code, Number(($event.target as HTMLSelectElement).value))"
              >
                <option v-for="n in COUNT_OPTIONS" :key="n" :value="n">{{ n }} 张</option>
              </select>
            </label>
          </div>
          <p class="text-xs text-stone-500">字牌共 7 种（东南西北中发白），每种可设 1 / 2 / 3 / 4 / 8 张。</p>
        </div>

        <!-- 花牌 -->
        <div v-else-if="tab === 'flowers'" class="space-y-4">
          <label class="flex items-center justify-between rounded-xl border border-stone-200 px-4 py-3">
            <span>
              <span class="text-sm font-medium text-stone-700">启用花牌（每种固定 1 张）</span>
              <span class="mt-0.5 block text-xs text-stone-500">春夏秋冬 · 梅兰竹菊，共 8 张</span>
            </span>
            <input type="checkbox" class="h-5 w-5 accent-emerald-600" :checked="flowersOn" @change="toggleFlowers" />
          </label>
          <div class="grid grid-cols-4 gap-2 sm:grid-cols-8">
            <div
              v-for="code in FLOWER_KINDS"
              :key="code"
              class="rounded-xl border py-3 text-center text-sm"
              :class="countOf(code) > 0 ? 'border-amber-300 bg-amber-50 text-amber-800' : 'border-stone-200 bg-white text-stone-300'"
            >
              {{ FLOWER_LABELS[code] }}
            </div>
          </div>
          <p class="text-xs text-stone-500">花牌不参与组合，摸到后补花计分（济宁「钱牌」可作万能牌）。</p>
        </div>

        <!-- 百搭 -->
        <div v-else class="space-y-5">
          <section class="rounded-xl border border-stone-200 p-4">
            <div class="flex items-center justify-between">
              <div>
                <h3 class="text-sm font-semibold text-stone-700">通用百搭</h3>
                <p class="text-xs text-stone-500">可代替任意牌的「万能牌」，数量 0~8 张</p>
              </div>
              <div class="flex items-center gap-2">
                <button class="h-8 w-8 rounded-lg border border-stone-200 text-stone-600 hover:bg-stone-50" @click="setJokers((draft.jokers || 0) - 1)">−</button>
                <span class="w-10 text-center text-lg font-semibold text-stone-800">{{ draft.jokers || 0 }}</span>
                <button class="h-8 w-8 rounded-lg border border-stone-200 text-stone-600 hover:bg-stone-50" @click="setJokers((draft.jokers || 0) + 1)">+</button>
              </div>
            </div>
          </section>

          <section class="rounded-xl border border-stone-200 p-4">
            <div class="mb-3">
              <h3 class="text-sm font-semibold text-stone-700">限定百搭</h3>
              <p class="text-xs text-stone-500">勾选后该牌种视为百搭（仍计入牌池），可单独设置张数</p>
            </div>
            <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <label
                v-for="code in honorTiles.concat(SUIT_KINDS.wan.slice(0, 2))"
                :key="code"
                class="flex items-center gap-2 rounded-lg border px-2 py-1.5 text-xs"
                :class="isLimitedJoker(code) ? 'border-fuchsia-300 bg-fuchsia-50' : 'border-stone-200'"
              >
                <input type="checkbox" class="h-4 w-4 accent-fuchsia-600" :checked="isLimitedJoker(code)" @change="toggleLimitedJoker(code)" />
                <span class="text-stone-700">{{ HONOR_LABELS[code] ?? code }}</span>
                <select
                  v-if="isLimitedJoker(code)"
                  class="ml-auto rounded border border-stone-200 px-1 py-0.5"
                  :value="draft.limitedJokers?.[code] ?? 1"
                  @change="setLimitedJokerCount(code, Number(($event.target as HTMLSelectElement).value))"
                >
                  <option v-for="n in COUNT_OPTIONS" :key="n" :value="n">{{ n }}</option>
                </select>
              </label>
            </div>
          </section>
        </div>
      </div>

      <!-- 底部 -->
      <footer class="flex flex-wrap items-center gap-2 border-t border-stone-100 px-6 py-4">
        <button class="rounded-xl border border-stone-200 px-3 py-2 text-sm text-stone-600 hover:bg-stone-50" @click="restorePreset">
          恢复预设默认
        </button>
        <span class="ml-auto text-xs text-stone-500">将使用 {{ summary.total }} 张牌</span>
        <button class="rounded-xl border border-stone-200 px-4 py-2 text-sm text-stone-600 hover:bg-stone-50" @click="emit('close')">
          取消
        </button>
        <button class="rounded-xl bg-emerald-600 px-5 py-2 text-sm font-medium text-white hover:bg-emerald-700" @click="confirm">
          确定
        </button>
      </footer>
    </div>
  </div>
</template>
