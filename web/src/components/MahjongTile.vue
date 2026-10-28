<script setup lang="ts">
import { computed, ref, type CSSProperties } from 'vue'
import { getTileMeta, getClassicTileSvgPath } from '../utils/tiles'
import { useThemeStore, type ThemeId } from '../stores/theme'
import { useDIYStore } from '../stores/diy'

interface Props {
  code: string // 牌代码，例如 'wan1', 'tiao5', 'dong', 'zhong', 或 'back' (牌背)
  size?: 'xs' | 'sm' | 'md' | 'lg' | 'xl' // 尺寸等级
  theme?: ThemeId // 显式指定主题，未传入时自动遵循 themeStore.currentTheme
  horizontal?: boolean // 横置（吃/碰/明杠横置）
  selected?: boolean // 选中高亮
  selectable?: boolean // 可被点击选中/出牌
  disabled?: boolean // 禁用状态
  isDrawn?: boolean // 是否为摸上来的牌（留出空隙或高亮）
  isLastDiscard?: boolean // 是否为最新打出的弃牌
  showLabel?: boolean // 是否在牌底部显示辅助微标（无障碍辅助）
}

const props = withDefaults(defineProps<Props>(), {
  size: 'md',
  horizontal: false,
  selected: false,
  selectable: false,
  disabled: false,
  isDrawn: false,
  isLastDiscard: false,
  showLabel: false,
})

const emit = defineEmits<{
  (e: 'click', code: string): void
}>()

const themeStore = useThemeStore()
const diyStore = useDIYStore()

// 计算实际使用的主题
const activeTheme = computed<ThemeId>(() => {
  return props.theme || themeStore.currentTheme
})

// 牌元数据
const meta = computed(() => getTileMeta(props.code))

// 是否是牌背
const isBack = computed(() => props.code === 'back')

// 经典矢量图路径
const svgPath = computed(() => getClassicTileSvgPath(props.code))

// DIY 自定义牌面(未自定义时回退经典图案)
const diyStyle = computed(() => diyStore.styleOf(props.code))
const diyUrl = computed(() => {
  if (activeTheme.value !== 'diy') return ''
  return diyStyle.value?.url ?? ''
})
const diyFit = computed<CSSProperties['objectFit']>(() => {
  const fit = diyStyle.value?.fit
  switch (fit) {
    case 'contain':
    case 'fill':
    case 'none':
    case 'scale-down':
      return fit
    case 'cover':
    default:
      return 'cover'
  }
})

/**
 * DIY 图片加载失败标记。
 *
 * 必须跟着 URL 复位: 设置页换图后 URL 会变, 若沿用上一次的失败状态,
 * 这张牌就会一直停在经典图案上 —— 表现是"改了但牌桌没变"。
 */
const diyFailed = ref(false)
const diyFailedUrl = ref('')
function onDiyError() {
  diyFailedUrl.value = diyUrl.value
  diyFailed.value = true
}
/** 当前 URL 是否已经失败过(换图后自动恢复) */
const diyBroken = computed(() => diyFailed.value && diyFailedUrl.value === diyUrl.value)

// 图片加载容错(经典 SVG 同理, 换主题/换牌面后要能重试)
const imgFailed = ref(false)
const imgFailedPath = ref('')
function onImgError() {
  imgFailedPath.value = svgPath.value
  imgFailed.value = true
}
const classicBroken = computed(() => imgFailed.value && imgFailedPath.value === svgPath.value)

// 尺寸配置 (宽高与字号比例)
const sizeClasses = computed(() => {
  if (props.horizontal) {
    switch (props.size) {
      case 'xs':
        return 'w-6 h-5 text-[9px]'
      case 'sm':
        return 'w-8 h-6 text-[10px]'
      case 'md':
        return 'w-11 h-8 text-xs'
      case 'lg':
        return 'w-14 h-10 text-sm'
      case 'xl':
        return 'w-16 h-12 text-base'
    }
  }
  switch (props.size) {
    case 'xs':
      return 'w-5 h-7 text-[9px]'
    case 'sm':
      return 'w-6 h-9 text-[10px]'
    case 'md':
      return 'w-9 h-12 text-xs sm:w-10 sm:h-14 sm:text-sm'
    case 'lg':
      return 'w-11 h-15 text-sm sm:w-13 sm:h-18 sm:text-base'
    case 'xl':
      return 'w-14 h-20 text-base sm:w-16 sm:h-22 sm:text-lg'
  }
})

// 明文主题颜色配置
const inkTextColorClass = computed(() => {
  switch (meta.value.color) {
    case 'red':
      return 'text-rose-700'
    case 'green':
      return 'text-emerald-700'
    case 'blue':
      return 'text-sky-700'
    case 'black':
    default:
      return 'text-stone-900'
  }
})

function handleClick() {
  if (!props.disabled && props.selectable) {
    emit('click', props.code)
  }
}
</script>

<template>
  <button
    type="button"
    role="img"
    :aria-label="isBack ? '麻将牌背' : meta.alt"
    :title="isBack ? '牌背' : meta.name"
    :disabled="disabled || !selectable"
    class="mahjong-tile group relative inline-flex select-none items-center justify-center rounded-[4px] border border-stone-300 font-sans leading-none shadow-sm transition-all focus:outline-none"
    :class="[
      sizeClasses,
      // 动画是否开启
      themeStore.animationEnabled ? 'duration-150 ease-out' : '!transition-none',
      // 可交互态
      selectable && !disabled
        ? 'cursor-pointer hover:-translate-y-1 hover:shadow-md active:translate-y-0 active:shadow-sm'
        : 'cursor-default',
      // 选中状态
      selected ? '-translate-y-2 !border-emerald-600 ring-2 ring-emerald-500 shadow-md' : '',
      // 最新出牌高亮
      isLastDiscard ? 'ring-2 ring-amber-400 ring-offset-1' : '',
      // 摸牌位略微拉开间距
      isDrawn ? 'ml-2 sm:ml-3' : '',
      // 牌身立体质感底色
      isBack
        ? 'bg-gradient-to-b from-emerald-600 via-teal-700 to-emerald-800 border-emerald-900 text-white'
        : 'bg-gradient-to-b from-white via-stone-50 to-amber-50/50'
    ]"
    @click="handleClick"
  >
    <!-- 牌背外观 -->
    <template v-if="isBack">
      <div class="flex h-full w-full items-center justify-center rounded-[3px] p-0.5">
        <div class="h-full w-full rounded-[2px] border border-emerald-500/40 bg-emerald-700/60 flex items-center justify-center shadow-inner">
          <div class="h-1.5 w-1.5 rounded-full bg-emerald-400/50"></div>
        </div>
      </div>
    </template>

    <!-- 牌面内容 -->
    <template v-else>
      <!-- 主题 3: DIY 自定义牌面 (服务端共享的自定义图片) -->
      <div
        v-if="activeTheme === 'diy' && diyUrl && !diyBroken"
        class="flex h-full w-full items-center justify-center overflow-hidden rounded-[3px] bg-white"
      >
        <img
          :src="diyUrl"
          :alt="meta.alt"
          class="h-full w-full pointer-events-none"
          :style="{ objectFit: diyFit }"
          loading="eager"
          @error="onDiyError"
        />
      </div>

      <!-- 主题 1: 经典传统 (Classic SVG 图案; DIY 未自定义的牌也回退到这里) -->
      <div
        v-else-if="(activeTheme === 'classic' || activeTheme === 'diy') && !classicBroken"
        class="flex h-full w-full items-center justify-center p-0.5 overflow-hidden"
      >
        <img
          :src="svgPath"
          :alt="meta.alt"
          class="h-full w-full object-contain pointer-events-none drop-shadow-[0_0.5px_0.5px_rgba(0,0,0,0.15)]"
          loading="eager"
          @error="onImgError"
        />
      </div>

      <!-- 主题 2: 明文水墨主题 (Ink / Clear Text) 或图片加载备选方案 -->
      <div
        v-else
        class="flex h-full w-full flex-col items-center justify-center p-0.5 font-bold tracking-tighter"
        :class="inkTextColorClass"
      >
        <!-- 字牌: 居中大单字 -->
        <template v-if="meta.suit === 'wind' || meta.suit === 'dragon'">
          <!-- 白板特殊处理: 明文下清晰显示方框白板或字 -->
          <div
            v-if="meta.code === 'bai'"
            class="flex flex-col items-center justify-center leading-none"
          >
            <div class="h-3 w-4.5 rounded-[1px] border border-sky-600 bg-sky-50/40 mb-0.5"></div>
            <span class="text-[9px] text-sky-800 scale-90 font-medium">白板</span>
          </div>
          <span
            v-else
            class="text-[1.3em] font-serif font-black leading-none drop-shadow-sm"
          >
            {{ meta.topText }}
          </span>
        </template>

        <!-- 数牌: 上下两字结构 (如 "二万", "三条", "五饼") -->
        <template v-else>
          <span class="text-[1.05em] font-serif font-black leading-[0.95] drop-shadow-sm">
            {{ meta.topText }}
          </span>
          <span
            class="text-[0.78em] font-sans font-bold leading-[0.95] opacity-90 mt-0.5 scale-95"
            :class="meta.suit === 'wan' ? 'text-rose-700' : meta.suit === 'tiao' ? 'text-emerald-700' : 'text-sky-700'"
          >
            {{ meta.bottomText }}
          </span>
        </template>
      </div>

      <!-- 辅助微标 (无障碍或极小尺寸辅助识别) -->
      <span
        v-if="showLabel"
        class="absolute -bottom-1 -right-1 rounded bg-stone-900/80 px-1 py-0 text-[8px] text-white opacity-80 backdrop-blur-xs scale-75"
      >
        {{ meta.shortName }}
      </span>
    </template>
  </button>
</template>

<style scoped>
/* 适度立体感与边缘厚度阴影 (伪 3D) */
.mahjong-tile {
  box-shadow: 0 1px 1px rgba(0, 0, 0, 0.08), 0 2px 0 #d6d3d1, 0 3px 2px rgba(0, 0, 0, 0.12);
}
.mahjong-tile:active {
  box-shadow: 0 1px 0 #d6d3d1, 0 1px 1px rgba(0, 0, 0, 0.1);
}
</style>
