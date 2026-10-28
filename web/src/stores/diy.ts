/**
 * DIY 自定义牌面主题的共享状态。
 *
 * 牌面数据存在服务端, 任何玩家选择 DIY 主题都会看到同一套自定义图片,
 * 因此这里只在应用启动时拉一次, 之后由设置页在保存后主动刷新。
 * 图片本身由浏览器按 URL 缓存(文件名带时间戳, 天然不冲突)。
 */
import { defineStore } from 'pinia'
import {
  fetchDIYState,
  type DIYState,
  type DIYTileStyle,
} from '../api/client'

export const useDIYStore = defineStore('diy', {
  state: () => ({
    /** 是否已经拉取过(避免重复请求) */
    loaded: false,
    /** 拉取中 */
    loading: false,
    /** 拉取失败时的错误信息(不阻塞打牌, 只是 DIY 主题退化成经典) */
    error: '' as string,
    /** 服务端是否已设置管理密码 */
    passwordSet: false,
    /** 可选的填充方式 */
    fits: ['cover', 'contain', 'fill', 'scale-down', 'none'] as string[],
    /** 单张图片字节上限 */
    maxBytes: 2 * 1024 * 1024,
    /** code -> 自定义样式 */
    tiles: {} as Record<string, DIYTileStyle>,
  }),
  getters: {
    /** 已自定义的牌数 */
    count(s): number {
      return Object.keys(s.tiles).length
    },
    /** 取某张牌的自定义样式; 没有则返回 undefined */
    styleOf(s) {
      return (code: string): DIYTileStyle | undefined => s.tiles[code]
    },
  },
  actions: {
    /** 从服务端刷新 DIY 状态(公开接口) */
    async refresh(force = false): Promise<void> {
      if (this.loading) return
      if (this.loaded && !force) return
      this.loading = true
      this.error = ''
      try {
        const state: DIYState = await fetchDIYState()
        this.passwordSet = state.passwordSet
        if (state.fits?.length) this.fits = state.fits
        if (state.maxBytes > 0) this.maxBytes = state.maxBytes
        const map: Record<string, DIYTileStyle> = {}
        for (const t of state.tiles) map[t.code] = t
        this.tiles = map
        this.loaded = true
      } catch (err) {
        this.error = err instanceof Error ? err.message : '读取 DIY 主题失败'
      } finally {
        this.loading = false
      }
    },
    /** 保存成功后写入本地缓存, 让牌桌立即生效(无需再拉一次) */
    applyTile(style: DIYTileStyle): void {
      this.tiles = { ...this.tiles, [style.code]: style }
    },
    /** 删除某张牌的自定义样式 */
    dropTile(code: string): void {
      const next = { ...this.tiles }
      delete next[code]
      this.tiles = next
    },
    /** 重置为未加载, 下次进入牌桌会重新拉取 */
    invalidate(): void {
      this.loaded = false
    },
  },
})
