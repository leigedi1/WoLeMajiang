import { describe, expect, it, vi } from 'vitest'
import { wsUrl, createGuest } from './client'

describe('api client', () => {
  it('wsUrl 按页面协议自适应', () => {
    // jsdom 默认 http。
    expect(location.protocol).toBe('http:')
    expect(wsUrl()).toBe(`ws://${location.host}/ws`)
  })

  it('createGuest 解析成功响应并抛出服务端错误', async () => {
    const ok = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: () => Promise.resolve({ guestId: 'g1', nickname: '甲', token: 't' }),
    })
    vi.stubGlobal('fetch', ok)
    await expect(createGuest('甲')).resolves.toEqual({ guestId: 'g1', nickname: '甲', token: 't' })

    const bad = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      json: () => Promise.resolve({ error: { message: '昵称不能为空' } }),
    })
    vi.stubGlobal('fetch', bad)
    await expect(createGuest('')).rejects.toThrow('昵称不能为空')
    vi.unstubAllGlobals()
  })
})
