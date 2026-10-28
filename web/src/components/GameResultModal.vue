<script setup lang="ts">
import { computed } from 'vue'
import type { ResultView, PlayerView } from '../protocol/types'
import MahjongTile from './MahjongTile.vue'

interface Props {
  result: ResultView
  mySeat: number
  players?: PlayerView[]
  /** 是否房主: 房主可以直接开始下一局 */
  isHost?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  players: () => [],
  isHost: false,
})

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'restart'): void
  (e: 'back'): void
  (e: 'leave'): void
}>()

const seatWinds = ['东', '南', '西', '北']

const isDrawWall = computed(() => props.result.winner < 0)
const isMyWin = computed(() => props.result.winner === props.mySeat)
const isMyLose = computed(() => !props.result.byDraw && props.result.loser === props.mySeat)

function nameOfSeat(seat: number): string {
  const p = props.players.find((pl) => pl.seat === seat)
  return p ? p.nickname : `${seatWinds[seat]}家`
}
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-sm animate-fade-in">
    <div
      class="relative flex max-h-[92dvh] w-full max-w-md flex-col items-center overflow-y-auto rounded-2xl border border-stone-700 bg-gradient-to-b from-stone-900 via-stone-900 to-stone-950 p-4 text-white shadow-2xl sm:p-6"
    >
      <!-- 结果标题 -->
      <div class="mb-4 text-center">
        <template v-if="isDrawWall">
          <span class="text-4xl">🀄</span>
          <h2 class="mt-2 text-2xl font-black text-stone-300">荒牌流局</h2>
          <p class="text-xs text-stone-500 mt-1">牌墙已摸尽，本局平局结算</p>
        </template>
        <template v-else-if="isMyWin">
          <span class="text-5xl animate-bounce inline-block">🏆</span>
          <h2 class="mt-2 text-2xl font-black text-amber-400">
            {{ result.byDraw ? '自摸大捷！' : '荣和大胜！' }}
          </h2>
          <p class="text-xs text-amber-200/80 mt-1">
            恭喜和牌 · {{ result.winKind === 'seven_pairs' ? '七对子' : '标准形' }}
          </p>
        </template>
        <template v-else>
          <span class="text-4xl">🀄</span>
          <h2 class="mt-2 text-xl font-bold text-stone-200">
            {{ nameOfSeat(result.winner) }} 和牌 ({{ result.byDraw ? '自摸' : '点炮' }})
          </h2>
          <p v-if="isMyLose" class="text-xs text-rose-400 mt-1">
            放炮支付：-{{ result.pay }} 分
          </p>
        </template>
      </div>

      <!-- 番数与分值卡片 -->
      <div
        v-if="!isDrawWall"
        class="mb-4 flex w-full items-center justify-around rounded-xl border border-amber-600/30 bg-amber-950/20 py-3 text-center"
      >
        <div>
          <span class="text-[10px] text-amber-200/60 uppercase">总番数</span>
          <div class="font-mono text-2xl font-black text-amber-300">{{ result.fan }} 番</div>
        </div>
        <div class="h-8 w-px bg-amber-800/40"></div>
        <div>
          <span class="text-[10px] text-amber-200/60 uppercase">单家基准</span>
          <div class="font-mono text-2xl font-black text-amber-300">{{ result.pay }} 分</div>
        </div>
      </div>

      <!-- 番种明细(阶段11): 和牌/七对子/自摸/门清/抢杠胡/杠上开花/海底捞月/河底捞鱼 -->
      <div
        v-if="!isDrawWall && (result.flags?.length ?? 0) > 0"
        class="mb-4 flex w-full flex-wrap items-center justify-center gap-1.5"
      >
        <span
          v-for="(f, fIdx) in result.flags"
          :key="'res-flag-' + fIdx"
          class="rounded-full border border-amber-600/40 bg-amber-950/30 px-2.5 py-0.5 text-[11px] font-medium text-amber-200"
        >{{ f }}</span>
      </div>

      <!-- 和牌分解 (标准形雀头或七对子) -->
      <div v-if="result.shape && result.shape.kind === 'standard'" class="mb-4 flex flex-col items-center gap-1.5 w-full">
        <span class="text-[11px] text-stone-400">和牌面子结构</span>
        <div class="flex flex-wrap gap-2 justify-center">
          <div
            v-for="(m, mIdx) in result.shape.melds || []"
            :key="'res-meld-' + mIdx"
            class="flex items-center gap-0.5 rounded border border-stone-700 bg-stone-800/60 p-1"
          >
            <MahjongTile v-for="(t, tIdx) in m" :key="'res-t-' + mIdx + '-' + tIdx" :code="t" size="xs" />
          </div>
          <!-- 雀头 -->
          <div v-if="result.shape.pair" class="flex items-center gap-0.5 rounded border border-amber-600/50 bg-amber-950/40 p-1">
            <MahjongTile :code="result.shape.pair" size="xs" />
            <MahjongTile :code="result.shape.pair" size="xs" />
          </div>
        </div>
      </div>

      <!-- 各家得分明细 (2/3/4 人通用, 家数取实际座位数) -->
      <div class="mb-5 w-full rounded-xl border border-stone-800 bg-stone-950/60 p-3">
        <div class="mb-2 text-[11px] text-stone-400 font-medium">本局得失分（{{ result.scores.length }} 家）：</div>
        <div class="grid grid-cols-2 gap-2 text-xs">
          <div
            v-for="(sc, sIdx) in result.scores"
            :key="'score-' + sIdx"
            class="flex items-center justify-between rounded-lg border px-2.5 py-1.5"
            :class="[
              sIdx === mySeat ? 'border-amber-500/40 bg-stone-800/60 font-bold' : 'border-stone-800/80 bg-stone-900/40',
              sc > 0 ? 'text-emerald-400' : sc < 0 ? 'text-rose-400' : 'text-stone-400'
            ]"
          >
            <span class="truncate max-w-[80px]">{{ seatWinds[sIdx] }} · {{ nameOfSeat(sIdx) }}</span>
            <span class="font-mono font-bold">{{ sc > 0 ? '+' + sc : sc }}</span>
          </div>
        </div>
      </div>

      <!-- 一局结束后: 直接再来一局 / 回房间 / 离开 -->
      <div class="flex w-full flex-col gap-2">
        <button
          v-if="isHost"
          type="button"
          class="w-full rounded-xl bg-gradient-to-r from-emerald-600 to-teal-700 py-3 font-bold text-white shadow-lg transition hover:scale-[1.02] active:scale-[0.98]"
          @click="emit('restart')"
        >
          🀄 再来一局
        </button>
        <button
          v-if="isHost"
          type="button"
          class="w-full rounded-xl border border-stone-600 py-2.5 text-sm font-medium text-stone-200 transition hover:border-stone-400 hover:text-white"
          @click="emit('back')"
        >
          返回房间（调整后再开始）
        </button>
        <button
          v-else
          type="button"
          class="w-full rounded-xl bg-gradient-to-r from-emerald-600 to-teal-700 py-3 font-bold text-white shadow-lg transition hover:scale-[1.02] active:scale-[0.98]"
          @click="emit('back')"
        >
          返回房间，等房主开下一局
        </button>

        <div class="flex w-full gap-2">
          <button
            type="button"
            class="flex-1 rounded-xl border border-stone-700 py-2 text-xs text-stone-300 transition hover:border-stone-500 hover:text-white"
            @click="emit('close')"
          >
            看看牌桌
          </button>
          <button
            type="button"
            class="flex-1 rounded-xl border border-stone-700 py-2 text-xs text-stone-400 transition hover:border-rose-500/60 hover:text-rose-300"
            @click="emit('leave')"
          >
            离开房间
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
