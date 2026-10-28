<script setup lang="ts">
import { useThemeStore, THEME_IDS, THEME_NAMES, isThemeId } from '../stores/theme'
import { sfxTap, setAudioEnabled, unlockAudio } from '../utils/sfx'

const themeStore = useThemeStore()

/**
 * 主题用下拉框而不是按钮组: 手机上三个按钮 + 动效 + 音效把标题栏挤满,
 * 「回房间」会被顶出屏幕; 以后再加主题也不会更糟。
 */
function onSelectTheme(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  if (!isThemeId(value)) return
  themeStore.setPlayerPreference(value)
}

/** 音效开关: 关闭时立刻静音, 打开时顺便解锁音频上下文并给一声确认 */
function toggleSound(): void {
  themeStore.toggleSound()
  setAudioEnabled(themeStore.soundEnabled)
  if (themeStore.soundEnabled) {
    unlockAudio()
    sfxTap()
  }
}
</script>

<template>
  <div class="theme-switcher flex min-w-0 items-center gap-2">
    <!-- 主题选择下拉框(外观沿用原按钮组: 浅色底 + 白底选中态) -->
    <div class="relative inline-flex min-w-0 items-center">
      <select
        class="max-w-[7.5rem] cursor-pointer appearance-none truncate rounded-md border border-stone-300 bg-white py-1 pl-2.5 pr-6 text-xs font-medium text-stone-900 shadow-sm transition hover:border-stone-400 focus:border-emerald-500 focus:outline-none disabled:cursor-not-allowed disabled:opacity-60 sm:max-w-none"
        :value="themeStore.currentTheme"
        :disabled="themeStore.isForced"
        :title="themeStore.isForced ? '当前房间权威配置了强制主题, 个人偏好暂不生效' : '切换牌面主题'"
        aria-label="选择牌面主题"
        @change="onSelectTheme"
      >
        <option v-for="id in THEME_IDS" :key="id" :value="id">{{ THEME_NAMES[id] }}</option>
      </select>
      <svg
        class="pointer-events-none absolute right-1.5 h-3 w-3 text-stone-500"
        viewBox="0 0 20 20"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        aria-hidden="true"
      >
        <path d="M5 8l5 5 5-5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </div>

    <!-- 强制状态提示微标 -->
    <span
      v-if="themeStore.isForced"
      class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] text-amber-800 font-medium"
      title="当前房间权威配置了强制主题，个人偏好暂不生效"
    >
      房间锁定
    </span>

    <!-- 动画开关 -->
    <button
      type="button"
      class="rounded-md border border-stone-300 bg-white px-2 py-1 text-xs text-stone-600 hover:border-stone-400 hover:text-stone-900 shadow-xs transition"
      :title="themeStore.animationEnabled ? '点击关闭动效 (无障碍/省电)' : '点击开启摸打动效'"
      aria-label="切换动效"
      @click="themeStore.toggleAnimation()"
    >
      {{ themeStore.animationEnabled ? '动效: 开' : '动效: 关' }}
    </button>

    <!-- 音效开关 -->
    <button
      type="button"
      class="rounded-md border border-stone-300 bg-white px-2 py-1 text-xs text-stone-600 hover:border-stone-400 hover:text-stone-900 shadow-xs transition"
      :title="themeStore.soundEnabled ? '点击关闭音效' : '点击开启音效'"
      :aria-pressed="themeStore.soundEnabled"
      aria-label="切换音效"
      @click="toggleSound"
    >
      {{ themeStore.soundEnabled ? '音效: 开' : '音效: 关' }}
    </button>
  </div>
</template>
