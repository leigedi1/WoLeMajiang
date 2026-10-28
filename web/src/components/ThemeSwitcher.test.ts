import { describe, expect, it, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ThemeSwitcher from './ThemeSwitcher.vue'
import { useThemeStore, THEME_IDS } from '../stores/theme'

/**
 * 主题选择必须是"一个下拉框"而不是一排按钮。
 *
 * 回归背景: 手机上「经典图案 / 明文水墨 / DIY」三个按钮 + 动效 + 音效把牌桌
 * 标题栏挤满,「回房间」整个被顶出屏幕; 而且以后每加一个主题都会更糟。
 * 所以这里锁死"用 select、选项来自 THEME_IDS、换主题写回偏好"。
 */
describe('ThemeSwitcher', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('主题是一个下拉框, 选项覆盖全部主题', () => {
    const wrapper = mount(ThemeSwitcher)
    const selects = wrapper.findAll('select')
    expect(selects).toHaveLength(1)
    // 用按钮组会随主题数量变宽; 下拉框只有一个 select, 宽度固定
    expect(wrapper.findAll('select[aria-label="选择牌面主题"]').length).toBe(1)
    expect(selects[0].findAll('option')).toHaveLength(THEME_IDS.length)
  })

  it('选择下拉项会写回个人偏好', async () => {
    const theme = useThemeStore()
    const wrapper = mount(ThemeSwitcher)
    await wrapper.find('select').setValue('ink')
    expect(theme.playerPreference).toBe('ink')
    expect(theme.currentTheme).toBe('ink')
  })

  it('房间强制主题时下拉框被禁用且显示锁定标记', () => {
    const theme = useThemeStore()
    theme.setRoomThemes({ forceTheme: 'classic' })
    const wrapper = mount(ThemeSwitcher)
    expect(wrapper.find('select').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('房间锁定')
  })

  it('动效与音效是图标开关, 点击可切换状态', async () => {
    const theme = useThemeStore()
    const wrapper = mount(ThemeSwitcher)
    const animBefore = theme.animationEnabled
    await wrapper.find('[aria-label="切换动效"]').trigger('click')
    expect(theme.animationEnabled).toBe(!animBefore)

    const soundBefore = theme.soundEnabled
    await wrapper.find('[aria-label="切换音效"]').trigger('click')
    expect(theme.soundEnabled).toBe(!soundBefore)
  })
})
