<script setup lang="ts">
import { useMatchStore } from '../stores/match'
import { useThemeStore } from '../stores/theme'
import type { SeatView } from '../protocol/types'

defineProps<{
  open: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const matchStore = useMatchStore()
const themeStore = useThemeStore()

// 场景预设定义
const scenarios: Array<{
  id: string
  title: string
  desc: string
  view: SeatView
}> = [
  {
    id: 'opening',
    title: '场景 1 · 经典开局行牌',
    desc: '轮到自家摸牌（第14张万4），自家东家，展示起手手牌与摸牌位',
    view: {
      seat: 0,
      phase: 'turn',
      turnSeat: 0,
      turnStage: 'discard',
      wallLeft: 83,
      hand: ['wan1', 'wan2', 'wan3', 'wan5', 'wan6', 'wan7', 'tiao2', 'tiao3', 'tiao4', 'tong7', 'tong8', 'tong9', 'zhong', 'wan4'],
      drawn: 'wan4',
      melds: [],
      discards: [
        ['dong'],
        ['xi', 'nan'],
        ['bei'],
        ['zhong'],
      ],
      othersHandCounts: [14, 13, 13, 13],
      othersMelds: [[], [], [], []],
      actions: ['discard'],
    },
  },
  {
    id: 'respond_window',
    title: '场景 2 · 吃碰杠响应窗口',
    desc: '上家打出万5，自家手持万3、万4、万5、万5，可碰、可吃、可过',
    view: {
      seat: 0,
      phase: 'turn',
      turnSeat: 3,
      turnStage: 'respond',
      wallLeft: 64,
      pendingTile: 'wan5',
      pendingBy: 3,
      hand: ['wan3', 'wan4', 'wan5', 'wan5', 'tiao7', 'tiao8', 'tiao9', 'tong1', 'tong2', 'tong3', 'nan', 'nan', 'nan'],
      melds: [],
      discards: [
        ['wan1', 'tiao1', 'tong9', 'dong', 'dong'],
        ['tong4', 'tong5', 'tiao3', 'nan'],
        ['wan9', 'wan8', 'tong2', 'xi'],
        ['tiao9', 'bei', 'zhong', 'fa', 'wan5'],
      ],
      othersHandCounts: [13, 13, 13, 12],
      othersMelds: [
        [],
        [{ kind: 'pong', tiles: ['tong8', 'tong8', 'tong8'] }],
        [],
        [],
      ],
      actions: ['pong', 'chow', 'pass'],
    },
  },
  {
    id: 'self_win',
    title: '场景 3 · 听牌自摸和',
    desc: '万123、万456、条789、东刻子、摸得红中成对雀头，出现自摸按钮',
    view: {
      seat: 0,
      phase: 'turn',
      turnSeat: 0,
      turnStage: 'discard',
      wallLeft: 42,
      hand: ['wan1', 'wan2', 'wan3', 'wan4', 'wan5', 'wan6', 'tiao7', 'tiao8', 'tiao9', 'dong', 'dong', 'dong', 'zhong', 'zhong'],
      drawn: 'zhong',
      melds: [],
      discards: [
        ['wan8', 'wan9', 'tong1', 'tong2', 'tong3', 'tong4'],
        ['tiao1', 'tiao2', 'tiao3', 'tiao4', 'nan', 'nan'],
        ['tong5', 'tong6', 'tong7', 'xi', 'bei'],
        ['fa', 'bai', 'wan7', 'tong8', 'tong9'],
      ],
      othersHandCounts: [14, 13, 13, 13],
      othersMelds: [[], [], [], []],
      actions: ['hu.self', 'discard'],
    },
  },
  {
    id: 'melds_rich',
    title: '场景 4 · 多副露与暗杠极值',
    desc: '自家有一副暗杠、一副碰牌，他人各有多组副露，检验各方位牌面排版',
    view: {
      seat: 1,
      phase: 'turn',
      turnSeat: 1,
      turnStage: 'discard',
      wallLeft: 18,
      hand: ['tiao1', 'tiao2', 'tiao3', 'wan9', 'wan9', 'wan9', 'fa'],
      drawn: 'fa',
      melds: [
        { kind: 'kong.concealed', tiles: ['wan1', 'wan1', 'wan1', 'wan1'] },
        { kind: 'pong', tiles: ['dong', 'dong', 'dong'] },
      ],
      discards: [
        ['wan2', 'wan3', 'wan4', 'wan5', 'wan6', 'wan7', 'tiao4', 'tiao5', 'tiao6', 'tiao7'],
        ['tong1', 'tong2', 'tong3', 'tong4', 'tong5', 'tong6', 'tong7', 'tong8'],
        ['nan', 'nan', 'xi', 'xi', 'bei', 'bei', 'zhong', 'zhong'],
        ['fa', 'bai', 'bai', 'bai', 'tiao8', 'tiao9'],
      ],
      othersHandCounts: [10, 7, 10, 10],
      othersMelds: [
        [{ kind: 'chow', tiles: ['tong7', 'tong8', 'tong9'] }],
        [
          { kind: 'kong.concealed', tiles: ['wan1', 'wan1', 'wan1', 'wan1'] },
          { kind: 'pong', tiles: ['dong', 'dong', 'dong'] },
        ],
        [{ kind: 'pong', tiles: ['zhong', 'zhong', 'zhong'] }],
        [{ kind: 'kong.exposed', tiles: ['bei', 'bei', 'bei', 'bei'] }],
      ],
      actions: ['discard'],
    },
  },
  {
    id: 'finished_settlement',
    title: '场景 5 · 七对子自摸结算弹窗',
    desc: '已触发自摸七对子（3番），展示完整的得分结算卡片与输赢分布',
    view: {
      seat: 0,
      phase: 'finished',
      turnSeat: 0,
      turnStage: 'discard',
      wallLeft: 36,
      hand: ['wan1', 'wan1', 'wan3', 'wan3', 'wan5', 'wan5', 'tiao7', 'tiao7', 'tong2', 'tong2', 'dong', 'dong', 'zhong', 'zhong'],
      melds: [],
      discards: [
        ['wan2', 'wan4', 'wan6', 'wan8', 'tong1', 'tong3'],
        ['tiao1', 'tiao3', 'tiao5', 'nan', 'nan'],
        ['tong7', 'tong8', 'tong9', 'xi', 'xi'],
        ['bei', 'bei', 'fa', 'fa', 'bai'],
      ],
      othersHandCounts: [14, 13, 13, 13],
      othersMelds: [[], [], [], []],
      actions: [],
      result: {
        winner: 0,
        byDraw: true,
        fan: 3,
        pay: 8,
        winKind: 'seven_pairs',
        scores: [24, -8, -8, -8],
      },
    },
  },
]

function applyScenario(s: typeof scenarios[0]) {
  matchStore.applyGameEvent({
    kind: 'mock.preset',
    view: s.view,
  })
  emit('close')
}

function clearMock() {
  matchStore.clear()
  emit('close')
}
</script>

<template>
  <div v-if="open" class="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-sm">
    <div
      class="relative flex w-full max-w-xl flex-col rounded-2xl border-2 border-amber-500/60 bg-stone-900 p-5 text-white shadow-2xl max-h-[90vh] overflow-y-auto"
    >
      <!-- 警示条 (严格符合要求: 明确标记为本地测试驱动) -->
      <div class="mb-4 rounded-xl border border-amber-500/40 bg-amber-950/40 p-3 text-amber-200">
        <div class="flex items-center gap-2 font-bold text-sm text-amber-300">
          <span>🛠️ 【本地测试驱动模式 · 规则状态演示】</span>
        </div>
        <p class="mt-1 text-xs text-amber-200/80 leading-relaxed">
          本模式专供阶段 3 验证 2D/伪 3D 牌桌各方位渲染、弃牌河、副露、双主题无缝切换与自摸/胡牌弹窗，不代表实际联机或跳过后端。
        </p>
      </div>

      <!-- 当前主题快捷切换 -->
      <div class="mb-4 flex items-center justify-between rounded-xl border border-stone-800 bg-stone-950/60 p-3 text-xs">
        <span class="text-stone-300">快速切换当前视觉主题：</span>
        <div class="inline-flex rounded-lg border border-stone-700 bg-stone-900 p-0.5">
          <button
            type="button"
            class="rounded-md px-2.5 py-1 transition"
            :class="themeStore.currentTheme === 'classic' ? 'bg-amber-500 text-stone-950 font-bold' : 'text-stone-400 hover:text-white'"
            @click="themeStore.setPlayerPreference('classic')"
          >
            经典传统 (riichi SVG)
          </button>
          <button
            type="button"
            class="rounded-md px-2.5 py-1 transition"
            :class="themeStore.currentTheme === 'ink' ? 'bg-amber-500 text-stone-950 font-bold' : 'text-stone-400 hover:text-white'"
            @click="themeStore.setPlayerPreference('ink')"
          >
            明文水墨 (中文大字)
          </button>
        </div>
      </div>

      <!-- 预设列表 -->
      <div class="mb-4 flex flex-col gap-2.5">
        <div
          v-for="s in scenarios"
          :key="s.id"
          class="flex items-center justify-between rounded-xl border border-stone-800 bg-stone-950/40 p-3 transition hover:border-amber-500/40 hover:bg-stone-800/40"
        >
          <div class="flex-1 pr-3">
            <h4 class="font-bold text-sm text-stone-200">{{ s.title }}</h4>
            <p class="text-xs text-stone-400 mt-0.5">{{ s.desc }}</p>
          </div>
          <button
            type="button"
            class="rounded-lg bg-emerald-700 px-3 py-1.5 text-xs font-bold text-white hover:bg-emerald-600 active:scale-95 transition"
            @click="applyScenario(s)"
          >
            加载演示
          </button>
        </div>
      </div>

      <!-- 底部操作按钮 -->
      <div class="flex items-center justify-between border-t border-stone-800 pt-3">
        <button
          type="button"
          class="rounded-lg border border-rose-500/40 px-3 py-1.5 text-xs text-rose-300 hover:bg-rose-950/40"
          @click="clearMock"
        >
          清空演示状态
        </button>
        <button
          type="button"
          class="rounded-lg bg-stone-800 px-4 py-1.5 text-xs font-medium text-white hover:bg-stone-700"
          @click="emit('close')"
        >
          关闭窗口
        </button>
      </div>
    </div>
  </div>
</template>
