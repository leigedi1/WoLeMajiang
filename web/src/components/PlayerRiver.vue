<script setup lang="ts">
import { computed } from 'vue'
import MahjongTile from './MahjongTile.vue'

interface Props {
  tiles: string[]
  orientation?: 'bottom' | 'right' | 'top' | 'left'
  isCurrentDiscard?: boolean
  size?: 'xs' | 'sm' | 'md'
  /** 每行张数: 桌面 6 张, 手机上收窄为 3 张以免牌河挤到两侧玩家 */
  perRow?: number
}

const props = withDefaults(defineProps<Props>(), {
  orientation: 'bottom',
  isCurrentDiscard: false,
  size: 'xs',
  perRow: 6,
})

// 每 perRow 张一行，标准牌河
const rows = computed(() => {
  const step = Math.max(1, props.perRow)
  const result: string[][] = []
  for (let i = 0; i < props.tiles.length; i += step) {
    result.push(props.tiles.slice(i, i + step))
  }
  return result
})
</script>

<template>
  <div
    class="player-river flex flex-col gap-1 p-1 rounded-md bg-stone-900/15 backdrop-blur-[2px]"
    :class="[
      orientation === 'bottom' || orientation === 'top' ? 'items-center' : 'items-center'
    ]"
  >
    <div
      v-for="(row, rIdx) in rows"
      :key="'river-row-' + rIdx"
      class="flex flex-wrap gap-1 justify-center"
    >
      <MahjongTile
        v-for="(t, cIdx) in row"
        :key="'river-tile-' + rIdx + '-' + cIdx"
        :code="t"
        :size="size"
        :is-last-discard="isCurrentDiscard && (rIdx * perRow + cIdx === tiles.length - 1)"
      />
    </div>
    <div v-if="tiles.length === 0" class="h-6 flex items-center justify-center text-[10px] text-emerald-100/40 font-mono">
      牌河空
    </div>
  </div>
</template>
