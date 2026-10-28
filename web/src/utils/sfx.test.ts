import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import {
  isAudioEnabled,
  setAudioEnabled,
  sfxDiscard,
  sfxDraw,
  sfxDrawWall,
  sfxKong,
  sfxMeld,
  sfxPass,
  sfxTap,
  sfxWin,
  sfxYourTurn,
  unlockAudio,
} from './sfx'
import { useThemeStore } from '../stores/theme'

// 阶段7: 音效是纯 WebAudio 现场合成, 没有音频资源可断言。
// 这里守住三条底线:
//   1. 没有 AudioContext 的环境(SSR / 老浏览器 / jsdom)不能抛异常;
//   2. 关闭音效后依然不抛, 且 isAudioEnabled 反映状态;
//   3. 音效偏好能落到 localStorage 并被 store 读回来。
describe('sfx (WebAudio 合成)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('没有 AudioContext 时所有音效都是安全的空操作', () => {
    expect(() => {
      unlockAudio()
      sfxTap()
      sfxDiscard()
      sfxDraw()
      sfxMeld()
      sfxKong()
      sfxWin()
      sfxDrawWall()
      sfxPass()
      sfxYourTurn()
    }).not.toThrow()
  })

  it('关闭后仍可安全调用, 状态可读回', () => {
    setAudioEnabled(false)
    expect(isAudioEnabled()).toBe(false)
    expect(() => sfxDiscard()).not.toThrow()

    setAudioEnabled(true)
    expect(isAudioEnabled()).toBe(true)
  })

  it('音效偏好写入 localStorage 并由 store 读回', () => {
    const store = useThemeStore()
    store.setSoundEnabled(false)
    expect(localStorage.getItem('majiang.sound.enabled')).toBe('false')

    // 重新建一个 store 模拟刷新页面
    setActivePinia(createPinia())
    expect(useThemeStore().soundEnabled).toBe(false)

    store.setSoundEnabled(true)
    expect(localStorage.getItem('majiang.sound.enabled')).toBe('true')
    setActivePinia(createPinia())
    expect(useThemeStore().soundEnabled).toBe(true)
  })
})
