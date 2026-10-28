<script setup lang="ts">
/**
 * 桌心指示盘: 只放「剩余牌墙 / 当前阶段 / 谁在行动」。
 * 四方风位已经在各自玩家名牌上, 这里不再画一圈方位字符 ——
 * 手机横屏时那圈字符会和数字挤在一起(用户反馈重叠)。
 */
import { computed } from 'vue'

interface Props {
  wallLeft: number
  turnSeat: number
  mySeat: number
  turnStage: string // 'draw' | 'discard' | 'respond'
  seatCount?: number
  pendingTile?: string
}

const props = withDefaults(defineProps<Props>(), {
  seatCount: 4,
})

const seatWinds = ['东', '南', '西', '北']

const stageText = computed(() => {
  switch (props.turnStage) {
    case 'draw':
      return '摸牌'
    case 'discard':
      return '出牌'
    case 'respond':
      return '响应'
    default:
      return '对局中'
  }
})

/** 谁在行动: 直接说「你」或「东风家」, 比在四角高亮更清楚也更省地方 */
const turnLabel = computed(() => {
  if (props.turnSeat < 0) return ''
  if (props.turnSeat === props.mySeat) return '你'
  return `${seatWinds[props.turnSeat % props.seatCount] || '?'}家`
})
</script>

<template>
  <div
    class="table-center flex shrink-0 flex-col items-center justify-center gap-0.5 rounded-lg border border-emerald-700/60 bg-gradient-to-br from-stone-900/90 to-stone-950/95 px-2 py-1 text-white shadow-xl backdrop-blur-md"
  >
    <div class="flex items-baseline gap-1 leading-none">
      <span class="text-[9px] text-stone-400">剩余</span>
      <span class="font-mono text-lg font-black text-amber-300 drop-shadow sm:text-2xl">{{ wallLeft }}</span>
    </div>
    <div class="flex items-center gap-1">
      <span
        class="rounded border border-emerald-800/40 bg-emerald-950/80 px-1.5 py-0.5 text-[9px] text-emerald-300"
      >
        {{ stageText }}
      </span>
      <span v-if="turnLabel" class="text-[9px] text-amber-300/90">{{ turnLabel }}</span>
    </div>
  </div>
</template>

<style scoped>
.table-center {
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.45), inset 0 1px 1px rgba(255, 255, 255, 0.1);
}
</style>
