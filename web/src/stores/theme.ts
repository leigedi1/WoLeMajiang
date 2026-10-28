/**
 * 主题与偏好设置系统
 * 严格执行规则优先级:
 *   房间强制主题 > 玩家个人主题 > 房间默认主题 > 系统默认主题
 * 强制期间个人偏好保留在本地存储但不生效，解除强制后即刻恢复。
 * 主题切换为纯响应式驱动，无需刷新或重启对局。
 *
 * 三套主题:
 *   classic 经典图案(内置 SVG)
 *   ink     明文水墨(纯文字排版)
 *   diy     自定义牌面(图片来自服务端, 所有玩家共享同一套)
 */
import { defineStore } from 'pinia'

export type ThemeId = 'classic' | 'ink' | 'diy'

/** 全部合法主题, 供下拉/按钮渲染与校验复用。 */
export const THEME_IDS: readonly ThemeId[] = ['classic', 'ink', 'diy'] as const

/** 主题显示名。 */
export const THEME_NAMES: Record<ThemeId, string> = {
  classic: '经典传统',
  ink: '明文水墨',
  diy: 'DIY 自定义',
}

/** 类型守卫: 判断任意字符串是否为合法主题标识。 */
export function isThemeId(value: unknown): value is ThemeId {
  return typeof value === 'string' && (THEME_IDS as readonly string[]).includes(value)
}

export interface ThemeResolutionInput {
  roomForceTheme?: string | null
  playerPreference?: string | null
  roomDefaultTheme?: string | null
  systemDefault?: ThemeId
}

export interface ThemeResolutionResult {
  theme: ThemeId
  source: 'room_forced' | 'player_preference' | 'room_default' | 'system_default'
  isForced: boolean
}

/** 纯函数: 根据四级优先级裁定最终生效的主题 */
export function resolveEffectiveTheme(input: ThemeResolutionInput): ThemeResolutionResult {
  const sysDefault = input.systemDefault || 'classic'

  // 1. 房间强制主题 (最高优先级)
  if (isThemeId(input.roomForceTheme)) {
    return {
      theme: input.roomForceTheme,
      source: 'room_forced',
      isForced: true,
    }
  }

  // 2. 玩家个人偏好 (个人偏好保留在本地，未被强制时生效)
  if (isThemeId(input.playerPreference)) {
    return {
      theme: input.playerPreference,
      source: 'player_preference',
      isForced: false,
    }
  }

  // 3. 房间默认主题
  if (isThemeId(input.roomDefaultTheme)) {
    return {
      theme: input.roomDefaultTheme,
      source: 'room_default',
      isForced: false,
    }
  }

  // 4. 系统默认主题
  return {
    theme: sysDefault,
    source: 'system_default',
    isForced: false,
  }
}

const STORAGE_KEY_PREFERENCE = 'majiang.theme.preference'
const STORAGE_KEY_ANIMATION = 'majiang.animations.enabled'
const STORAGE_KEY_SOUND = 'majiang.sound.enabled'

function loadSavedPreference(): ThemeId | null {
  try {
    const val = localStorage.getItem(STORAGE_KEY_PREFERENCE)
    return isThemeId(val) ? val : null
  } catch {
    return null
  }
}

function loadSavedAnimation(): boolean {
  try {
    const val = localStorage.getItem(STORAGE_KEY_ANIMATION)
    if (val === 'false') return false
    return true
  } catch {
    return true
  }
}

/** 音效默认开: 用户提出"下牌太突兀", 所以默认给一点声音, 但随时可关。 */
function loadSavedSound(): boolean {
  try {
    const val = localStorage.getItem(STORAGE_KEY_SOUND)
    if (val === 'false') return false
    return true
  } catch {
    return true
  }
}

export const useThemeStore = defineStore('theme', {
  state: () => ({
    // 本地持久化的玩家个人偏好
    playerPreference: loadSavedPreference() as ThemeId | null,
    // 房间下发的主题配置
    roomDefaultTheme: null as string | null,
    roomForceTheme: null as string | null,
    // 动画开关 (关掉后动画时长置 0，适配低性能和无障碍)
    animationEnabled: loadSavedAnimation(),
    // 音效开关(阶段7: 下牌/碰杠/和牌的提示音)
    soundEnabled: loadSavedSound(),
  }),
  getters: {
    resolved(s): ThemeResolutionResult {
      return resolveEffectiveTheme({
        roomForceTheme: s.roomForceTheme,
        playerPreference: s.playerPreference,
        roomDefaultTheme: s.roomDefaultTheme,
        systemDefault: 'classic',
      })
    },
    currentTheme(): ThemeId {
      return this.resolved.theme
    },
    isForced(): boolean {
      return this.resolved.isForced
    },
    themeName(): string {
      return THEME_NAMES[this.currentTheme] ?? THEME_NAMES.classic
    },
  },
  actions: {
    /** 玩家切换个人偏好 */
    setPlayerPreference(theme: ThemeId | null): void {
      this.playerPreference = theme
      try {
        if (theme) {
          localStorage.setItem(STORAGE_KEY_PREFERENCE, theme)
        } else {
          localStorage.removeItem(STORAGE_KEY_PREFERENCE)
        }
      } catch {
        /* storage unavailable */
      }
    },
    /** 同步房间主题配置 */
    setRoomThemes(options: { defaultTheme?: string | null; forceTheme?: string | null }): void {
      if (options.defaultTheme !== undefined) {
        this.roomDefaultTheme = options.defaultTheme
      }
      if (options.forceTheme !== undefined) {
        this.roomForceTheme = options.forceTheme
      }
    },
    /** 清理房间主题限制 (如退房时) */
    clearRoomThemes(): void {
      this.roomDefaultTheme = null
      this.roomForceTheme = null
    },
    /** 切换动画开关 */
    setAnimationEnabled(enabled: boolean): void {
      this.animationEnabled = enabled
      try {
        localStorage.setItem(STORAGE_KEY_ANIMATION, String(enabled))
      } catch {
        /* storage unavailable */
      }
    },
    toggleAnimation(): void {
      this.setAnimationEnabled(!this.animationEnabled)
    },
    /** 切换音效开关 */
    setSoundEnabled(enabled: boolean): void {
      this.soundEnabled = enabled
      try {
        localStorage.setItem(STORAGE_KEY_SOUND, String(enabled))
      } catch {
        /* storage unavailable */
      }
    },
    toggleSound(): void {
      this.setSoundEnabled(!this.soundEnabled)
    },
  },
})
