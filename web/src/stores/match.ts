/**
 * 对局状态镜像: 由 game.event 驱动, 只存引擎投影, 不含规则判定。
 */
import { defineStore } from 'pinia'
import type { SeatView } from '../protocol/types'

export const useMatchStore = defineStore('match', {
  state: () => ({
    view: null as SeatView | null,
    /** 最近一条对局事件(音效/动画用来做"刚刚发生了什么"的判断) */
    lastEvent: null as { kind: string; seat?: number; tiles?: string[]; tick: number } | null,
    /** 事件计数: 同种事件连续出现时也能触发 watch */
    eventTick: 0,
  }),
  getters: {
    inMatch: (s) => s.view !== null && s.view.phase !== 'lobby',
    isFinished: (s) => s.view?.phase === 'finished',
    myActions(): string[] {
      return this.view?.actions ?? []
    },
    seatWind(): string {
      const winds = ['东', '南', '西', '北']
      return winds[this.view?.seat ?? 0] ?? '?'
    },
  },
  actions: {
    /** 应用一条 game.event 事件负载 */
    applyGameEvent(payload: { kind?: string; seat?: number; tiles?: string[]; view?: SeatView | null }): void {
      if (payload && payload.view) {
        this.view = payload.view
      }
      if (payload && (payload.kind || payload.tiles?.length)) {
        this.eventTick += 1
        this.lastEvent = {
          kind: payload.kind ?? '',
          seat: payload.seat,
          tiles: payload.tiles ? [...payload.tiles] : undefined,
          tick: this.eventTick,
        }
      }
    },
    /** 离开对局(离房/重置) */
    clear(): void {
      this.view = null
      this.lastEvent = null
    },
  },
})
