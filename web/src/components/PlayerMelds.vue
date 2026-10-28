<script setup lang="ts">
import type { MeldView } from '../protocol/types'
import MahjongTile from './MahjongTile.vue'

interface Props {
  melds: MeldView[]
  size?: 'xs' | 'sm' | 'md'
}

withDefaults(defineProps<Props>(), {
  size: 'sm',
})
</script>

<template>
  <div v-if="melds && melds.length > 0" class="player-melds flex flex-wrap gap-2 items-center">
    <div
      v-for="(meld, mIdx) in melds"
      :key="'meld-group-' + mIdx"
      class="inline-flex items-center gap-0.5 rounded border border-amber-900/30 bg-stone-900/30 p-1 shadow-inner backdrop-blur-xs"
    >
      <!-- 暗杠特殊展示: 两头是牌背，中间露出或四张全背 -->
      <template v-if="meld.kind === 'kong.concealed'">
        <MahjongTile code="back" :size="size" />
        <MahjongTile :code="meld.tiles[1] || meld.tiles[0]" :size="size" />
        <MahjongTile :code="meld.tiles[2] || meld.tiles[0]" :size="size" />
        <MahjongTile code="back" :size="size" />
      </template>

      <!-- 普通吃、碰、明杠、补杠 -->
      <template v-else>
        <MahjongTile
          v-for="(t, tIdx) in meld.tiles"
          :key="'meld-tile-' + mIdx + '-' + tIdx"
          :code="t"
          :size="size"
          :horizontal="tIdx === 0 && (meld.kind === 'pong' || meld.kind === 'chow')"
        />
      </template>
    </div>
  </div>
</template>
