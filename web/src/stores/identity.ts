/**
 * 游客身份存储: 会话级持久化 + 创建/验证流程。
 * 注册账户体系在后续阶段预留(接口位置不变, 实现可替换)。
 *
 * 刻意用 sessionStorage 而不是 localStorage: 同一浏览器开多个标签页时,
 * 每个标签应当是独立的玩家(localStorage 会被标签间互相覆盖, 导致
 * "加入自己的房间"被服务端判成同一游客而报「请先离开当前房间」)。
 * 首次访问会把旧的 localStorage 身份迁移进本标签并清掉, 老用户无感。
 */
import { defineStore } from 'pinia'
import { createGuest, verifyGuest, type GuestCredential } from '../api/client'

const STORAGE_KEY = 'majiang.identity.v1'
/** 旧版用同一个 key 存在 localStorage(跨标签共享); 首次加载时迁移进 sessionStorage。 */

interface StoredIdentity {
  guestId: string
  nickname: string
  token: string
}

function session(): Storage | null {
  try {
    return window.sessionStorage
  } catch {
    return null
  }
}

function loadStored(): StoredIdentity | null {
  const st = session()
  try {
    let raw = st?.getItem(STORAGE_KEY) ?? null
    if (!raw) {
      // 一次性迁移: 把旧 localStorage 里的身份搬到本标签, 之后标签间互不影响
      const legacy = window.localStorage.getItem(STORAGE_KEY)
      if (legacy) {
        st?.setItem(STORAGE_KEY, legacy)
        window.localStorage.removeItem(STORAGE_KEY)
        raw = legacy
      }
    }
    if (!raw) return null
    const parsed = JSON.parse(raw) as StoredIdentity
    if (!parsed.guestId || !parsed.token) return null
    return parsed
  } catch {
    return null
  }
}

function saveStored(identity: StoredIdentity): void {
  try {
    session()?.setItem(STORAGE_KEY, JSON.stringify(identity))
  } catch {
    /* 隐私模式下 sessionStorage 可能不可写: 退化为仅内存身份 */
  }
}

function clearStored(): void {
  try {
    session()?.removeItem(STORAGE_KEY)
  } catch {
    /* 忽略 */
  }
}

export type IdentityPhase = 'unready' | 'checking' | 'ready' | 'expired'

export const useIdentityStore = defineStore('identity', {
  state: () => ({
    phase: 'unready' as IdentityPhase,
    guestId: '' as string,
    nickname: '' as string,
    token: '' as string,
    lastError: '' as string,
  }),
  getters: {
    hasIdentity: (s) => s.guestId !== '' && s.token !== '',
  },
  actions: {
    /** 启动时调用: 尝试恢复既有身份, 失败则回到未就绪态 */
    async restore(): Promise<boolean> {
      const stored = loadStored()
      if (!stored) {
        this.phase = 'unready'
        return false
      }
      this.phase = 'checking'
      try {
        const guest = await verifyGuest(stored.guestId, stored.token)
        this.guestId = guest.guestId
        this.nickname = guest.nickname
        this.token = stored.token
        this.phase = 'ready'
        return true
      } catch {
        // 令牌失效或网络不可用: 清除本地身份, 走重新创建流程。
        clearStored()
        this.guestId = ''
        this.nickname = ''
        this.token = ''
        this.phase = 'unready'
        return false
      }
    },
    /** 创建新游客身份并持久化 */
    async register(nickname: string): Promise<void> {
      const cred: GuestCredential = await createGuest(nickname)
      this.guestId = cred.guestId
      this.nickname = cred.nickname
      this.token = cred.token
      this.phase = 'ready'
      saveStored({ guestId: cred.guestId, nickname: cred.nickname, token: cred.token })
    },
    /**
     * 采用本地离线身份(P2P/局域网): 不访问后端, 本地生成 guestId。
     * 必须持久化 —— 否则刷新页面就换了身份, 房主侧的 seat→peer 映射失效,
     * P2P 断线重连/视图自愈(刷新后 rejoin)会直接找不到自己原来的座位。
     */
    adoptLocal(nickname: string): void {
      const guestId = `p2p-${Math.random().toString(36).slice(2, 8)}`
      this.guestId = guestId
      this.nickname = nickname
      this.token = 'p2p'
      this.phase = 'ready'
      saveStored({ guestId, nickname, token: this.token })
    },
    /** 登出: 清除本地身份 */
    forget(): void {
      clearStored()
      this.guestId = ''
      this.nickname = ''
      this.token = ''
      this.phase = 'unready'
      this.lastError = ''
    },
    /** 记录错误(供 UI 展示) */
    fail(message: string): void {
      this.lastError = message
      this.phase = 'unready'
    },
  },
})
