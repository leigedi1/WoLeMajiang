import { describe, expect, it, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { setActivePinia, createPinia } from 'pinia'
import MahjongTile from './MahjongTile.vue'
import { useThemeStore } from '../stores/theme'
import { useDIYStore } from '../stores/diy'

describe('MahjongTile.vue', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('经典主题下渲染 SVG 图片及正确的 alt 描述', () => {
    const themeStore = useThemeStore()
    themeStore.setPlayerPreference('classic')

    const wrapper = mount(MahjongTile, {
      props: {
        code: 'wan1',
      },
    })
    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('/tiles/classic/wan1.svg')
    expect(wrapper.attributes('aria-label')).toBe('一万')
  })

  it('明文水墨主题下渲染清晰汉字 (例如二条渲染“二”与“条”)', () => {
    const themeStore = useThemeStore()
    themeStore.setPlayerPreference('ink')

    const wrapper = mount(MahjongTile, {
      props: {
        code: 'tiao2',
      },
    })
    // 明文主题下不渲染经典 img，直接渲染文字结构
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('二')
    expect(wrapper.text()).toContain('条')
    expect(wrapper.attributes('aria-label')).toBe('二条')
  })

  it('牌背正常显示且附带无障碍属性', () => {
    const wrapper = mount(MahjongTile, {
      props: {
        code: 'back',
      },
    })
    expect(wrapper.attributes('aria-label')).toBe('麻将牌背')
    expect(wrapper.classes()).toContain('border-emerald-900')
  })

  it('selectable 为 true 时支持点击事件，disabled 时阻止', async () => {
    const wrapper = mount(MahjongTile, {
      props: {
        code: 'tong5',
        selectable: true,
      },
    })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')?.[0]).toEqual(['tong5'])

    // disabled
    const disabledWrapper = mount(MahjongTile, {
      props: {
        code: 'tong5',
        selectable: true,
        disabled: true,
      },
    })
    await disabledWrapper.trigger('click')
    expect(disabledWrapper.emitted('click')).toBeUndefined()
  })

  it('DIY 主题下有自定义图时渲染自定义图, 未自定义的牌回退经典', () => {
    const themeStore = useThemeStore()
    const diyStore = useDIYStore()
    themeStore.setPlayerPreference('diy')
    diyStore.applyTile({
      code: 'wan1',
      url: '/api/v1/diy/images/abc.png',
      fit: 'contain',
      updatedAt: 1,
    })

    const custom = mount(MahjongTile, { props: { code: 'wan1' } })
    expect(custom.find('img').attributes('src')).toBe('/api/v1/diy/images/abc.png')
    expect(custom.find('img').attributes('style')).toContain('object-fit: contain')

    // 没有自定义图的牌仍用经典 SVG
    const fallback = mount(MahjongTile, { props: { code: 'tong5' } })
    expect(fallback.find('img').attributes('src')).toBe('/tiles/classic/tong5.svg')
  })

  it('DIY 图片加载失败后, 换成新图片能重新尝试(失败标记随 URL 复位)', async () => {
    const themeStore = useThemeStore()
    const diyStore = useDIYStore()
    themeStore.setPlayerPreference('diy')
    diyStore.applyTile({
      code: 'wan1',
      url: '/api/v1/diy/images/broken.png',
      fit: 'cover',
      updatedAt: 1,
    })

    const wrapper = mount(MahjongTile, { props: { code: 'wan1' } })
    // 模拟图片 404
    await wrapper.find('img').trigger('error')
    await nextTick()
    // 失败后回退经典图案
    expect(wrapper.find('img').attributes('src')).toBe('/tiles/classic/wan1.svg')

    // 换一张新图(URL 变了) → 必须重新尝试自定义图, 而不是永久停在经典
    diyStore.applyTile({
      code: 'wan1',
      url: '/api/v1/diy/images/good.png',
      fit: 'cover',
      updatedAt: 2,
    })
    await nextTick()
    expect(wrapper.find('img').attributes('src')).toBe('/api/v1/diy/images/good.png')
  })
})
