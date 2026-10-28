import { describe, expect, it, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { resolveEffectiveTheme, useThemeStore } from './theme'

describe('resolveEffectiveTheme (4-tier priority)', () => {
  it('默认无配置时回退到系统默认 classic', () => {
    const res = resolveEffectiveTheme({})
    expect(res.theme).toBe('classic')
    expect(res.source).toBe('system_default')
    expect(res.isForced).toBe(false)
  })

  it('仅有房间默认主题时，生效房间默认', () => {
    const res = resolveEffectiveTheme({ roomDefaultTheme: 'ink' })
    expect(res.theme).toBe('ink')
    expect(res.source).toBe('room_default')
    expect(res.isForced).toBe(false)
  })

  it('玩家个人偏好优先于房间默认主题', () => {
    const res = resolveEffectiveTheme({
      roomDefaultTheme: 'ink',
      playerPreference: 'classic',
    })
    expect(res.theme).toBe('classic')
    expect(res.source).toBe('player_preference')
    expect(res.isForced).toBe(false)
  })

  it('房间强制主题优先于玩家个人偏好与房间默认', () => {
    const res = resolveEffectiveTheme({
      roomForceTheme: 'classic',
      playerPreference: 'ink',
      roomDefaultTheme: 'ink',
    })
    expect(res.theme).toBe('classic')
    expect(res.source).toBe('room_forced')
    expect(res.isForced).toBe(true)
  })

  it('房间强制主题优先且强制生效', () => {
    const res = resolveEffectiveTheme({
      roomForceTheme: 'ink',
      playerPreference: 'classic',
    })
    expect(res.theme).toBe('ink')
    expect(res.source).toBe('room_forced')
    expect(res.isForced).toBe(true)
  })
})

describe('useThemeStore reactive lifecycle', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('个人偏好设置与持久化', () => {
    const theme = useThemeStore()
    expect(theme.currentTheme).toBe('classic')

    theme.setPlayerPreference('ink')
    expect(theme.currentTheme).toBe('ink')
    expect(theme.playerPreference).toBe('ink')
    expect(localStorage.getItem('majiang.theme.preference')).toBe('ink')
  })

  it('强制主题期间个人偏好被抑制但保留，解除后立刻恢复', () => {
    const theme = useThemeStore()
    theme.setPlayerPreference('ink')
    expect(theme.currentTheme).toBe('ink')

    // 房间强制为 classic
    theme.setRoomThemes({ forceTheme: 'classic' })
    expect(theme.currentTheme).toBe('classic')
    expect(theme.isForced).toBe(true)
    // 本人偏好仍存未丢
    expect(theme.playerPreference).toBe('ink')

    // 解除房间强制
    theme.setRoomThemes({ forceTheme: null })
    expect(theme.currentTheme).toBe('ink')
    expect(theme.isForced).toBe(false)
  })

  it('动画开关切换与存储', () => {
    const theme = useThemeStore()
    expect(theme.animationEnabled).toBe(true)

    theme.toggleAnimation()
    expect(theme.animationEnabled).toBe(false)
    expect(localStorage.getItem('majiang.animations.enabled')).toBe('false')

    theme.toggleAnimation()
    expect(theme.animationEnabled).toBe(true)
  })
})
