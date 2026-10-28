import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useIdentityStore } from './identity'

// 模拟 REST 客户端; 身份存储只依赖这两个函数。
vi.mock('../api/client', () => ({
  createGuest: vi.fn(),
  verifyGuest: vi.fn(),
  fetchHealth: vi.fn(),
  wsUrl: () => 'ws://localhost/ws',
}))

import { createGuest, verifyGuest } from '../api/client'

const mockCreate = vi.mocked(createGuest)
const mockVerify = vi.mocked(verifyGuest)

const KEY = 'majiang.identity.v1'

describe('identity store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    // 身份是「每标签一份」的: 存在 sessionStorage, 不再写 localStorage。
    sessionStorage.clear()
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('register 成功后持久化身份并可恢复', async () => {
    mockCreate.mockResolvedValue({
      guestId: 'g1',
      nickname: '阿豪',
      token: 'tok123',
    })
    const id = useIdentityStore()
    await id.register('  阿豪  ')

    expect(id.phase).toBe('ready')
    expect(id.guestId).toBe('g1')
    expect(id.nickname).toBe('阿豪')
    expect(id.token).toBe('tok123')
    // sessionStorage 已持久化(标签级), localStorage 保持干净。
    expect(sessionStorage.getItem(KEY)).toContain('"guestId":"g1"')
    expect(localStorage.getItem(KEY)).toBeNull()

    // 新 store 实例模拟当前标签刷新后恢复。
    mockVerify.mockResolvedValue({ guestId: 'g1', nickname: '阿豪' })
    const id2 = useIdentityStore()
    const ok = await id2.restore()
    expect(ok).toBe(true)
    expect(id2.phase).toBe('ready')
    expect(id2.guestId).toBe('g1')
    expect(id2.token).toBe('tok123')
  })

  it('令牌失效时清除本地身份', async () => {
    sessionStorage.setItem(KEY, JSON.stringify({ guestId: 'g2', nickname: '旧人', token: 'old' }))
    mockVerify.mockRejectedValue(new Error('游客身份或令牌无效'))
    const id = useIdentityStore()
    const ok = await id.restore()
    expect(ok).toBe(false)
    expect(id.phase).toBe('unready')
    expect(id.guestId).toBe('')
    expect(sessionStorage.getItem(KEY)).toBeNull()
  })

  it('损坏的本地数据按无身份处理', async () => {
    sessionStorage.setItem(KEY, '{broken json')
    const id = useIdentityStore()
    const ok = await id.restore()
    expect(ok).toBe(false)
    expect(mockVerify).not.toHaveBeenCalled()
  })

  it('缺少 token 的存储记录被视为无效', async () => {
    sessionStorage.setItem(KEY, JSON.stringify({ guestId: 'g3', nickname: '半身份', token: '' }))
    const id = useIdentityStore()
    await id.restore()
    expect(mockVerify).not.toHaveBeenCalled()
  })

  it('forget 清空身份与存储', async () => {
    mockCreate.mockResolvedValue({ guestId: 'g9', nickname: '九', token: 't9' })
    const id = useIdentityStore()
    await id.register('九')
    id.forget()
    expect(id.phase).toBe('unready')
    expect(id.hasIdentity).toBe(false)
    expect(sessionStorage.getItem(KEY)).toBeNull()
  })

  it('register 失败时不落库', async () => {
    mockCreate.mockRejectedValue(new Error('昵称不能为空'))
    const id = useIdentityStore()
    await expect(id.register('')).rejects.toThrow('昵称不能为空')
    expect(id.hasIdentity).toBe(false)
    expect(sessionStorage.getItem(KEY)).toBeNull()
  })

  it('旧 localStorage 身份迁移进本标签后清除, 其他标签不再共享', async () => {
    localStorage.setItem(KEY, JSON.stringify({ guestId: 'old1', nickname: '老用户', token: 'tk' }))
    mockVerify.mockResolvedValue({ guestId: 'old1', nickname: '老用户' })

    const id = useIdentityStore()
    expect(await id.restore()).toBe(true)
    expect(id.guestId).toBe('old1')
    // 原位置被清掉 → 新标签拿不到, 各自独立
    expect(localStorage.getItem(KEY)).toBeNull()
    expect(sessionStorage.getItem(KEY)).toContain('"guestId":"old1"')
  })
})
