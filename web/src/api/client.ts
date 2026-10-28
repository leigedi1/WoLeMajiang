/**
 * REST API 客户端: 游客身份与服务器健康。
 * 开发环境经 Vite 代理同源访问; 生产环境同域部署(Caddy 反代), 均无需额外配置。
 */
import type { HelloOut } from '../protocol/types'

/**
 * 部署前缀: 根路径部署时为空串(现线上 https://mj.wole110.xyz/ 即如此); 子路径部署(如 /mj/)时由 Vite 的 base 注入。
 * 所有 REST 与 WebSocket 地址都必须经 basePath(), 否则子路径部署会请求到站点根部。
 */
export const DEPLOY_BASE = (import.meta.env.BASE_URL || '/').replace(/\/+$/, '')

/** 给以 / 开头的绝对路径加上部署前缀 */
export function basePath(path: string): string {
  return `${DEPLOY_BASE}${path}`
}

export interface GuestCredential {
  guestId: string
  nickname: string
  token: string
}

export interface HealthInfo {
  status: string
  service: string
  go: string
  uptimeSeconds: number
  onlineRooms: number
  maxOnlineRooms: number
  guests: number
  protocolVersion: number
}

async function postJSON<T>(url: string, body: unknown): Promise<T> {
  const resp = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = (await resp.json().catch(() => null)) as (T & { error?: { message?: string } }) | null
  if (!resp.ok) {
    const msg = data?.error?.message ?? `请求失败(${resp.status})`
    throw new Error(msg)
  }
  return data as T
}

/** 创建游客身份; 重连令牌仅此一次返回, 调用方须持久保存 */
export async function createGuest(nickname: string): Promise<GuestCredential> {
  return postJSON<GuestCredential>(basePath('/api/v1/guests'), { nickname })
}

/** 用令牌验证既有游客身份(断线/换设备重连) */
export async function verifyGuest(guestId: string, token: string): Promise<HelloOut> {
  return postJSON<HelloOut>(basePath('/api/v1/reconnect'), { guestId, token })
}

/** 查询服务器健康信息 */
export async function fetchHealth(): Promise<HealthInfo> {
  const resp = await fetch(basePath('/api/v1/health'))
  if (!resp.ok) throw new Error(`健康检查失败(${resp.status})`)
  return (await resp.json()) as HealthInfo
}

/** 房间码对应的联机方式(加入端据此自动选择通道, 无需玩家手选) */
export type ResolvedTransport = 'server' | 'p2p'

export interface RoomResolveInfo {
  code: string
  transport: ResolvedTransport
  /** P2P 房间当前在线端点数(含房主)。0 表示房主信令刚断开, 仍在重连宽限期内 */
  peers?: number
}

/** 按房间码判定该房间是在线托管(server)还是 P2P/局域网(p2p); 房间不存在则抛错。 */
export async function resolveRoom(code: string): Promise<RoomResolveInfo> {
  const c = code.trim().toUpperCase()
  const resp = await fetch(basePath(`/api/v1/rooms/${encodeURIComponent(c)}/resolve`))
  const data = (await resp.json().catch(() => null)) as
    | (RoomResolveInfo & { error?: { message?: string } })
    | null
  if (!resp.ok) {
    // 404 最常见的原因是 P2P 房主把页面切后台/锁屏, 信令断线后房间过期。
    // 直接给出可操作的提示, 比只显示「房间不存在或已结束」有用得多。
    if (resp.status === 404) {
      throw new Error('房间不存在或已失效。P2P 房间需要房主保持页面打开（手机请勿锁屏/切后台）；也可以让房主改用「在线服务器」模式。')
    }
    throw new Error(data?.error?.message ?? `房间不存在或已结束(${resp.status})`)
  }
  return data as RoomResolveInfo
}

/** 计算 WS 地址(同源, 协议自适应, 带部署前缀) */
export function wsUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}${basePath('/ws')}`
}

// ---- DIY 自定义牌面主题 ----

/** 一张牌的自定义样式(服务端返回) */
export interface DIYTileStyle {
  code: string
  /** 已带部署前缀, 可直接作为 img src */
  url: string
  /** 填充方式: cover/contain/fill/scale-down/none */
  fit: string
  updatedAt: number
}

export interface DIYState {
  tiles: DIYTileStyle[]
  /** 是否已设置管理密码 */
  passwordSet: boolean
  fits: string[]
  maxBytes: number
}

/** 读取全部 DIY 牌面(公开接口, 所有玩家共享) */
export async function fetchDIYState(): Promise<DIYState> {
  const resp = await fetch(basePath('/api/v1/diy'))
  if (!resp.ok) throw new Error(`读取 DIY 主题失败(${resp.status})`)
  const data = (await resp.json()) as DIYState
  return {
    ...data,
    tiles: (data.tiles ?? []).map((t) => ({ ...t, url: basePath(t.url) })),
  }
}

/** 校验管理密码(进入设置页) */
export async function diyAuth(password: string): Promise<void> {
  await postJSON(basePath('/api/v1/diy/auth'), { password })
}

/** 首次设置或修改管理密码 */
export async function diySetPassword(currentPassword: string, newPassword: string): Promise<void> {
  await postJSON(basePath('/api/v1/diy/password'), { currentPassword, newPassword })
}

/** 上传/替换一张牌的自定义图片 */
export async function diySaveTile(input: {
  password: string
  code: string
  fit: string
  dataUrl: string
}): Promise<DIYTileStyle> {
  const saved = await postJSON<DIYTileStyle>(basePath('/api/v1/diy/tiles'), input)
  return { ...saved, url: basePath(saved.url) }
}

/** 恢复某张牌的默认外观 */
export async function diyDeleteTile(password: string, code: string): Promise<void> {
  await postJSON(basePath(`/api/v1/diy/tiles/${encodeURIComponent(code)}/delete`), { password })
}
