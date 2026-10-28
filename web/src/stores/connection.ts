/**
 * WS 连接会话存储: 握手认证、上行操作分发、下行事件状态化、心跳与自动重连。
 * 只做传输与状态镜像, 不包含任何麻将规则。
 * 阶段6: transportType 支持 'ws'(在线服务器) 和 'p2p'(P2P 房主权威本地)。
 *       p2p 模式下房主直接调用 game.ApplyAction + 本地状态机, 再广播到 DataChannel。
 *       加入者只把 GameAction send 到 host DataChannel, 等 host 广播事件回来。
 */
import { defineStore } from 'pinia'
import { wsUrl } from '../api/client'
import {
  Op,
  Ev,
  decode,
  encode,
  PROTOCOL_VERSION,
  type Envelope,
  type RoomStateView,
  type ErrorOut,
  type HelloOut,
  type GameEventOut,
  type LobbyListView,
  type LobbyRoomView,
  type InviteView,
  type InviteCreatedView,
} from '../protocol/types'
import { useMatchStore } from './match'
import { useWebRTCStore } from './webrtc'
import { useLocalGameStore } from './localGame'
import type { SeatSpec } from '../engine/engine'
import { useIdentityStore } from './identity'

export type ConnStatus = 'idle' | 'connecting' | 'connected' | 'closed' | 'failed'
export type TransportType = 'ws' | 'p2p'

/** 心跳间隔: 服务端 30s 无消息不主动断开, 但代理/CDN 常在 60s 剪断空闲连接。 */
export const HEARTBEAT_MS = 20_000
/** 断线后自动重连的退避序列(毫秒), 用尽后停止并提示用户手动重试。 */
export const RECONNECT_DELAYS = [500, 1_500, 4_000, 8_000]

export const useConnectionStore = defineStore('connection', {
  state: () => ({
    status: 'idle' as ConnStatus,
    transport: 'ws' as TransportType,
    lastSeq: 0 as number,
    room: null as RoomStateView | null,
    lastError: null as ErrorOut | null,
    /** 阶段7: P2P 房主彻底消失(明确终止, 不可恢复) */
    hostLost: false,
    socket: null as WebSocket | null,
    _refCounter: 0,
    /** 记忆的身份, 用于自动重连 */
    _auth: null as { guestId: string; token: string } | null,
    _heartbeat: null as ReturnType<typeof setInterval> | null,
    _reconnectAt: 0,
    _reconnectTimer: null as ReturnType<typeof setTimeout> | null,
    // 阶段5: 大厅与邀请
    lobby: [] as LobbyRoomView[],
    onlineRooms: 0,
    invites: [] as InviteView[],
    /** 最近一次创建的邀请(含令牌明文, 仅此一次) */
    lastInviteToken: '' as string,
    /** 对局动作序号(幂等去重; 重连后从 0 重新开始) */
    actionSeq: 0,
    /** 阶段6: P2P DataChannel 监听器清理函数数组 */
    _p2pHandlers: [] as (() => void)[],
    /** guestId → 信令 peerId (房主按座位单播私有视图时用; 不随房间投影重置) */
    _peerByGuest: {} as Record<string, string>,
    /** seat → 信令 peerId (与 engine 座位一一对应, 开局后投影重建也不丢) */
    _peerBySeat: {} as Record<number, string>,
    /** 信令 peerId → engine 座位 (远端真人动作按座位执行时的反查表) */
    _seatByPeer: {} as Record<string, number>,
    /** 房主私有视图推送定时器 (对局中每 300ms 检查座位视图变化) */
    _viewPumpTimer: null as ReturnType<typeof setInterval> | null,
    /** P2P 房主开局参数(人数/牌集), 来自 createRoom 的入参, 开局时传给本地引擎 */
    _p2pStartOpts: null as {
      players?: number
      pool?: Record<string, number>
      rules?: Record<string, unknown>
    } | null,
  }),
  getters: {
    inRoom: (s) => s.room !== null,
    mySeat(): number | null {
      return this.room?.you?.seat ?? null
    },
    /** 是否房主(按座位 0 与 isHost 标记判断, 以服务端投影为准) */
    isHost(): boolean {
      const me = this.room?.players?.find((p) => p.guestId === this.room?.you?.guestId)
      return me?.isHost === true
    },
    /** 是否已准备 */
    amReady(): boolean {
      const me = this.room?.players?.find((p) => p.guestId === this.room?.you?.guestId)
      return me?.ready === true
    },
    /** 其他未准备的人类成员 */
    unreadyPlayers(): { nickname: string; isBot: boolean }[] {
      return (this.room?.players ?? [])
        .filter((p) => !p.ready && p.guestId !== this.room?.you?.guestId)
        .map((p) => ({ nickname: p.nickname, isBot: p.isBot === true }))
    },
    /** 是否正在等待一次自动重连 */
    reconnecting: (s) => s._reconnectTimer !== null,
    canStart(): boolean {
      const room = this.room
      if (!room) return false
      const capacity = room.capacity ?? 4
      if (room.players.length < capacity) return false
      return room.players.every((p) => p.ready)
    },
  },
  actions: {
    /** 建立 WS 并完成 guest.hello 握手; remember=true 时记住身份以便自动重连 */
    connect(guestId: string, token: string, remember = true, transport: TransportType = 'ws'): Promise<void> {
      return new Promise((resolve, reject) => {
        this.transport = transport
        this.hostLost = false
        if (transport === 'p2p') {
          this._connectP2P(guestId, token).then(resolve).catch(reject)
          return
        }
        if (this.socket && this.status === 'connected') {
          resolve()
          return
        }
        if (remember) this._auth = { guestId, token }
        this.status = 'connecting'
        this.lastError = null
        this.clearReconnectTimer()
        const socket = new WebSocket(wsUrl())
        this.socket = socket

        const helloRef = `h${++this._refCounter}`
        const fail = (err: string) => {
          this.status = 'failed'
          this.lastError = { code: 'connect_failed', message: err }
          socket.close()
          reject(new Error(err))
        }

        socket.onopen = () => {
          socket.send(
            encode({
              v: PROTOCOL_VERSION,
              type: Op.Hello,
              ref: helloRef,
              payload: { guestId, token },
            }),
          )
        }
        socket.onerror = () => {
          if (this.status === 'connecting') fail('WebSocket 连接失败')
        }
        socket.onclose = () => {
          this.stopHeartbeat()
          const wasConnected = this.status === 'connected'
          this.socket = null
          if (this.status !== 'failed') this.status = 'closed'
          if (wasConnected) {
            // 意外断线: 自动重连(指数退避), 重连成功后补拉增量事件。
            this.scheduleReconnect()
          } else if (this.status === 'closed') {
            reject(new Error('连接已关闭'))
          }
        }
        socket.onmessage = (ev: MessageEvent) => {
          let env: Envelope
          try {
            env = decode(String(ev.data))
          } catch (err) {
            this.lastError = { code: 'bad_message', message: String(err) }
            return
          }
          if (env.type === Ev.Hello) {
            const hello = env.payload as HelloOut
            if (hello.guestId !== guestId) {
              fail('握手返回身份不一致')
              return
            }
            this.status = 'connected'
            this.startHeartbeat()
            // 只有确实在房间里才 resync: 刚离房/刚连上就发 resync 会被服务端
            // 当成「不在房间却要同步」, 弹出误导性的报错。
            if (this.room) this.resync()
            resolve()
            return
          }
          if (env.type === Ev.Error && this.status === 'connecting') {
            const err = env.payload as ErrorOut
            fail(err.message)
            return
          }
          this.handleEvent(env)
        }
      })
    },

    /** 处理已连接后的下行事件 */
    handleEvent(env: Envelope): void {
      switch (env.type) {
        case Ev.RoomState: {
          if (env.seq !== undefined) this.lastSeq = env.seq
          const view = env.payload as RoomStateView | null
          this.room = view && view.roomId ? view : null
          // P2P 房主广播的 lobby 视图不带 you, 按 guestId 认出自己。
          if (this.room && !this.room.you) {
            const myId = useIdentityStore().guestId
            const me = this.room.players.find(p => p.guestId === myId)
            if (me) this.room.you = { seat: me.seat, guestId: me.guestId }
          }
          // 离房/房间结束时清空对局镜像与动作序号。
          if (!this.room) {
            useMatchStore().clear()
            this.actionSeq = 0
            this.invites = []
          } else if (this.room.status === 'playing' && !useMatchStore().view) {
            // 阶段7 断线恢复: 握手后服务端会把房间状态推回来(哪怕客户端是
            // 刷新/返回后"什么都不记得"的新页面)。此时本地还没有对局镜像,
            // 主动补一次 resync 才能拿回牌桌; 否则会被一直留在等待房间。
            this.resync()
          }
          break
        }
        case Ev.GameEvent: {
          if (env.seq !== undefined) this.lastSeq = env.seq
          useMatchStore().applyGameEvent(env.payload as GameEventOut)
          break
        }
        case Ev.RoomList: {
          const list = env.payload as LobbyListView | null
          this.lobby = list?.rooms ?? []
          this.onlineRooms = list?.onlineRooms ?? 0
          break
        }
        case Ev.InviteCreated: {
          const created = env.payload as InviteCreatedView | null
          if (created) {
            this.lastInviteToken = created.token
            this.invites = [...this.invites.filter((i) => i.id !== created.invite.id), created.invite]
          }
          break
        }
        case Ev.InviteList: {
          const payload = env.payload as { invites?: InviteView[] } | null
          this.invites = payload?.invites ?? []
          break
        }
        case Ev.Error: {
          this.lastError = env.payload as ErrorOut
          break
        }
        case Ev.Pong:
        case Ev.Hello:
          break
        default:
          // 未来扩展事件: 暂存序列号即可。
          if (env.seq !== undefined) this.lastSeq = env.seq
          break
      }
    },

    /** 发送上行操作 */
    send(type: string, payload?: unknown): void {
      const env: Envelope = { v: PROTOCOL_VERSION, type, ref: `r${++this._refCounter}`, payload }
      if (this.transport === 'p2p') {
        // P2P 模式: 房主本地执行; 加入者只发到 host DataChannel
        this._sendP2P(env)
        return
      }
      if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
        throw new Error('连接未就绪')
      }
      this.socket.send(encode(env))
    },

    /** P2P 上行: 房主本地引擎执行 + 广播; 加入者只把 GameAction 转发 host */
    _sendP2P(env: Envelope): void {
      const wrtc = useWebRTCStore()
      const local = useLocalGameStore()
      const isHost = wrtc.isHost
      const hostPeerId = wrtc.p2pHostPeerId

      if (!isHost) {
        // 加入者: 只把业务消息转发 host
        const allDcs = Object.values(wrtc.peerDCs ?? {}) as RTCDataChannel[]
        const dc =
          wrtc.peerDCs[hostPeerId] ??
          allDcs.find((d) => d && d.readyState === 'open')
        if (!dc || dc.readyState !== 'open') {
          this.lastError = { code: 'p2p_not_ready', message: '还没连上房主, 不能发动作' }
          return
        }
        dc.send(encode(env))
        return
      }

      // —— 房主本地执行 ——
      switch (env.type) {
        case Op.Hello: {
          this.status = 'connected'
          // 房主端 Hello: 如果 engine 已启动则把当前视图作为 room.state 投影下发
          void this._projectLocalToRoom().then((view) => {
            if (view) {
              this.room = view
              this.handleEvent({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view })
              wrtc.broadcastGameEnvelope(encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view } as Envelope))
            }
          })
          break
        }
        case Op.RoomCreate: {
          // 房主刚建好 P2P 信令房间: 标记连接已就绪, 同时手写一份 pre-match room
          // (因为 engine 还没启动, _projectLocalToRoom 此时返回 null). engine 启动走 Op.GameStart.
          this.status = 'connected'
          const guest = useIdentityStore()
          const opts = ((env as any).payload ?? {}) as {
            name?: string
            theme?: string
            players?: number
            pool?: Record<string, number>
            ruleSet?: Record<string, unknown>
          }
          // 记住房主开局设置(人数/自定义牌集/规则细则), 开局时交给本地引擎。
          this._p2pStartOpts = {
            players: opts.players,
            pool: opts.pool,
            rules: opts.ruleSet,
          }
          const cap = opts.players && opts.players >= 2 ? opts.players : 4
          const code = wrtc.p2pRoomCode ?? ''
          this.room = {
            roomId: code ? `p2p-${code}` : 'p2p-local',
            code,
            name: opts.name ?? '',
            theme: opts.theme ?? 'classic',
            capacity: cap,
            players: [
              {
                seat: 0,
                guestId: guest.guestId || 'host',
                name: guest.nickname || '房主',
                nickname: guest.nickname || '房主',
                ready: false,
                joinedAt: Date.now(),
                online: true,
                isMe: true,
                tileCount: 0,
              },
            ],
            bots: [],
            invites: [],
            hostSeat: 0,
            you: { seat: 0, guestId: guest.guestId || 'host' },
            status: 'waiting',
            seq: 0,
          } as any
          break
        }
        case Op.RoomJoin: {
          if (!this.room) break
          const pl = (env as any).payload ?? (env as any).data ?? {}
          const guestId: string = pl.guestId ?? `peer-${(env as any).peer ?? ''}`
          const name: string = pl.name ?? guestId.slice(0, 12)
          const players: any[] = (this.room as any).players ?? []
          const usedSeats = new Set(players.map(p => p.seat))
          let seat = 1
          while (usedSeats.has(seat) && seat < 4) seat++
          players.push({
            seat,
            guestId,
            name,
            nickname: name,
            ready: false,
            joinedAt: Date.now(),
            online: true,
            isMe: false,
            tileCount: 0,
            // 信令 peer: 房主按座位单播私有视图(手牌)时用
            peerId: (env as any).__peer ?? '',
          } as any)
          ;(this.room as any).players = players.slice(0, 4)
          ;(this.room as any).status = 'waiting'
          ;(this.room as any).seq = ((this.room as any).seq ?? 0) + 1
          // 记下 guestId → peerId: 开局后房间投影会重建 players, 这个映射必须独立保存
          const fromPeer = (env as any).__peer ?? ''
          if (fromPeer) {
            this._peerByGuest[guestId] = fromPeer
            this._peerBySeat[seat] = fromPeer
            this._seatByPeer[fromPeer] = seat
          }
          wrtc.broadcastGameEnvelope(
            encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: this.room } as Envelope),
          )
          break
        }
        case Op.RoomLeave: {
          if (!this.room) break
          const pl = (env as any).payload ?? (env as any).data ?? {}
          const guestId: string = pl.guestId ?? (env as any).peer ?? ''
          const players: any[] = (this.room as any).players ?? []
          const idx = players.findIndex((p: any) => p.guestId === guestId)
          if (idx >= 0) players.splice(idx, 1)
          ;(this.room as any).players = players
          ;(this.room as any).seq = ((this.room as any).seq ?? 0) + 1
          wrtc.broadcastGameEnvelope(
            encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: this.room } as Envelope),
          )
          break
        }
        case Op.SeatReady: {
          if (!this.room) break
          const pl = (env as any).payload ?? (env as any).data ?? {}
          const ready: boolean = pl.ready !== false
          const guestId: string = pl.guestId ?? (env as any).peer ?? ''
          const players: any[] = (this.room as any).players ?? []
          const p = players.find((x: any) => x.guestId === guestId)
          if (p) p.ready = ready
          ;(this.room as any).seq = ((this.room as any).seq ?? 0) + 1
          wrtc.broadcastGameEnvelope(
            encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: this.room } as Envelope),
          )
          break
        }
        case Op.SeatBot: {
          if (!this.room) break
          const pl = (env as any).payload ?? (env as any).data ?? {}
          const { add = true, seat } = pl as { add?: boolean; seat?: number }
          const players: any[] = (this.room as any).players ?? []
          if (add) {
            const usedSeats = new Set(players.map((p: any) => p.seat))
            let botSeat = typeof seat === 'number' ? seat : 1
            while (usedSeats.has(botSeat) && botSeat < 4) botSeat++
            if (players.length < 4) {
              players.push({
                seat: botSeat,
                guestId: `bot-${botSeat}`,
                name: `AI ${botSeat}`,
                ready: true,
                joinedAt: Date.now(),
                online: true,
                isMe: false,
              })
            }
          } else if (typeof seat === 'number') {
            const idx = players.findIndex(
              (p: any) => p.seat === seat && String(p.guestId).startsWith('bot'),
            )
            if (idx >= 0) players.splice(idx, 1)
          }
          ;(this.room as any).players = players
          ;(this.room as any).seq = ((this.room as any).seq ?? 0) + 1
          wrtc.broadcastGameEnvelope(
            encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: this.room } as Envelope),
          )
          break
        }
        case Op.RoomList:
        case Op.RoomUpdate:
        case Op.RoomDissolve:
        case Op.InviteCreate:
        case Op.InviteList:
        case Op.InviteRevoke:
        case Op.InviteJoin:
          // WS 专属 op 在 P2P 房主端忽略.
          break
        case Op.GameStart: {
          // 房主: 启动本地 engine — 座位表以 pre-match 维护的 players 为准
          // (含房主本人、加入的真人 peer、已添加的 AI), 而不是信令层 peer 数.
          // 每个真人占一个真实座位(P2P 远端动作按座位进入同一个权威引擎),
          // 空位由 AI 补足, 因此房主端不再是"1 真人 + AI"的单机局.
          const roster = (((this.room as any)?.players as any[] | undefined) ?? []).slice()
          const cap = Math.max(
            2,
            Math.min(4, this._p2pStartOpts?.players ?? Math.max(roster.length, 2)),
          )
          const bySeat = new Map<number, any>()
          for (const p of roster) {
            const s = Number(p.seat)
            if (Number.isInteger(s) && s >= 0 && s < cap && !bySeat.has(s)) bySeat.set(s, p)
          }
          const seats: SeatSpec[] = []
          const owners: string[] = []
          for (let i = 0; i < cap; i++) {
            const p = bySeat.get(i)
            if (p) {
              const gid = String(p.guestId ?? `seat-${i}`)
              const ai = gid.startsWith('bot') || p.isBot === true
              seats.push({
                name: String(p.name ?? p.nickname ?? (i === 0 ? '房主' : `玩家 ${i + 1}`)),
                ai,
                difficulty: 'normal',
              })
              owners.push(gid)
            } else {
              seats.push({ name: `电脑 ${i + 1}`, ai: true, difficulty: 'normal' })
              owners.push(`local-ai-${i}`)
            }
          }
          const startOpts = this._p2pStartOpts
          void local
            .start({
              seats,
              seatOwners: owners,
              hostSeat: 0,
              seed: Date.now() & 0xffffffff,
              ...(startOpts?.pool ? { pool: startOpts.pool } : {}),
              ...(startOpts?.rules ? { rules: startOpts.rules } : {}),
            })
            .then(() => {
            void this._projectLocalToRoom().then((view) => {
              if (view) {
                this.room = view
                this.handleEvent({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view })
                wrtc.broadcastGameEnvelope(encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view } as Envelope))
              }
              // 按座位单播私有视图: 加入者据此拿到自己的手牌
              this._unicastPrivateViews()
              // 引擎自动推进不会触发 op 分发, 开启持续推送
              this._startPrivateViewPump()
            })
          })
          break
        }
        case Op.GameAction: {
          const act = (env.payload as any)?.action ?? ''
          const pl = (env.payload as any)?.payload ?? {}
          // 远端真人的动作按其座位执行; 房主自己的动作走 humanSeat(=0)
          const fromPeer = (env as any).__peer as string | undefined
          const peerSeat = fromPeer !== undefined ? this._seatByPeer[fromPeer] : undefined
          if (typeof peerSeat === 'number') local.actAs(peerSeat, act, pl)
          else local.act(act, pl)
          // 等 pump 跑完 (最多几秒), 然后投影 + 广播
          setTimeout(() => {
            void this._projectLocalToRoom().then((view) => {
              if (view) {
                this.room = view
                this.handleEvent({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view })
              }
            })
            // 广播 GameEvent: 拿 local.recentEvents 最后若干条作为增量
            const last = local.recentEvents.slice(-2)
            for (const ev of last) {
              wrtc.broadcastGameEnvelope(encode({ v: PROTOCOL_VERSION, type: Ev.GameEvent, payload: ev } as Envelope))
            }
            // 广播 RoomState 给加入者
            void this._projectLocalToRoom().then((view) => {
              if (view) wrtc.broadcastGameEnvelope(encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view } as Envelope))
              // 按座位单播私有视图(手牌/可行动作)
              this._unicastPrivateViews()
            })
          }, 50)
          break
        }
        case Op.Ping:
          this.handleEvent({ v: PROTOCOL_VERSION, type: Ev.Pong, ref: env.ref })
          break
        case Op.Resync:
          void this._projectLocalToRoom().then((view) => {
            if (view) {
              this.room = view
              this.handleEvent({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view })
              wrtc.broadcastGameEnvelope(encode({ v: PROTOCOL_VERSION, type: Ev.RoomState, payload: view } as Envelope))
            }
            this._unicastPrivateViews()
          })
          break
        default:
          break
      }
    },

    /** 把 localGame 当前 view 投影成 RoomStateView (与联机协议兼容) */
    async _projectLocalToRoom(): Promise<RoomStateView | null> {
      const local = useLocalGameStore()
      const wrtc = useWebRTCStore()
      if (!local._engine) return null
      const seatView = local.view ?? (await local.refreshView())
      if (!seatView) return null
      const players: RoomStateView['players'] = local.players.map((p) => ({
        guestId: p.guestId,
        nickname: p.nickname,
        seat: p.seat,
        connected: true,
        tileCount: p.tileCount,
        ready: true,
        isHost: p.seat === 0,
        // AI 补位可能是引擎内置的 local-ai-* 或房间层添加的 bot-*
        isBot: p.guestId.startsWith('local-ai-') || p.guestId.startsWith('bot'),
      }))
      const myGuestId = players.find((p) => p.seat === local.humanSeat)?.guestId ?? 'local-human'
      return {
        roomId: 'p2p-local',
        code: wrtc.p2pRoomCode || 'P2P',
        mode: 'p2p',
        status: seatView.phase === 'finished' ? 'finished' : 'playing',
        theme: 'classic',
        name: wrtc.p2pRoomCode ? `P2P ${wrtc.p2pRoomCode}` : 'P2P Local',
        seq: local.seq,
        capacity: local.playersCount,
        players,
        you: { guestId: myGuestId, seat: local.humanSeat },
      } as any
    },

    /** 保活 */
    ping(): void {
      this.send(Op.Ping)
    },

    startHeartbeat(): void {
      this.stopHeartbeat()
      this._heartbeat = setInterval(() => {
        if (this.socket?.readyState === WebSocket.OPEN) {
          try {
            this.ping()
          } catch {
            /* 连接刚断开, 由 onclose 处理 */
          }
        }
      }, HEARTBEAT_MS)
    },

    stopHeartbeat(): void {
      if (this._heartbeat !== null) {
        clearInterval(this._heartbeat)
        this._heartbeat = null
      }
    },

    clearReconnectTimer(): void {
      if (this._reconnectTimer !== null) {
        clearTimeout(this._reconnectTimer)
        this._reconnectTimer = null
      }
    },

    /** 安排一次自动重连(退避序列用尽后停止) */
    scheduleReconnect(): void {
      const auth = this._auth
      if (!auth) return
      const attempt = this._reconnectAt
      if (attempt >= RECONNECT_DELAYS.length) {
        this.lastError = { code: 'reconnect_exhausted', message: '连接已断开, 自动重连失败, 请手动重试' }
        return
      }
      const delay = RECONNECT_DELAYS[attempt]
      this._reconnectAt = attempt + 1
      this.clearReconnectTimer()
      this._reconnectTimer = setTimeout(() => {
        this._reconnectTimer = null
        this.status = 'connecting'
        this.connect(auth.guestId, auth.token, false).catch(() => {
          // connect 内部 onclose 会再次调度; 这里只吞掉 Promise 拒绝。
        })
      }, delay)
    },

    /** 手动重连 */
    retry(): void {
      const auth = this._auth
      if (!auth) return
      this._reconnectAt = 0
      this.status = 'connecting'
      this.connect(auth.guestId, auth.token, false).catch(() => {})
    },

    // ---- 房间/大厅 ----

    /** 创建房间 (players/presetId/pool/ruleSet 由创建房间设置界面决定) */
    createRoom(
      opts: {
        name?: string
        password?: string
        public?: boolean
        theme?: string
        players?: number
        presetId?: string
        pool?: unknown
        ruleSet?: Record<string, unknown>
      } = {},
    ): void {
      this.send(Op.RoomCreate, opts)
    },

    /** 房主开局 */
    startMatch(): void {
      this.send(Op.GameStart, {})
    },

    /** 对局动作(带单调递增序号, 服务端据此丢弃重放) */
    gameAction(action: string, payload: Record<string, unknown> = {}): void {
      this.actionSeq += 1
      this.send(Op.GameAction, { action, payload, actionSeq: this.actionSeq })
    },

    /** 加入房间 (P2P 模式下必须带上本机身份, 房主据此把玩家加进名单) */
    joinRoom(code: string, password = ''): void {
      const guest = useIdentityStore()
      this.send(Op.RoomJoin, {
        code: code.trim().toUpperCase(),
        password,
        guestId: guest.guestId || undefined,
        name: guest.nickname || undefined,
      })
    },

    /** 用邀请令牌加入 */
    joinByInvite(token: string, code = '', password = ''): void {
      this.send(Op.InviteJoin, { token: token.trim(), code: code.trim().toUpperCase(), password })
    },

    /** 设置准备状态 (P2P 下带上身份, 房主据此定位玩家) */
    setReady(ready: boolean): void {
      const guest = useIdentityStore()
      this.send(Op.SeatReady, { ready, guestId: guest.guestId || undefined })
    },

    /** 离开房间 (P2P 下带上身份, 房主据此把玩家移出名单) */
    leaveRoom(): void {
      const guest = useIdentityStore()
      this.send(Op.RoomLeave, { guestId: guest.guestId || undefined })
    },

    /** 拉取公开大厅 */
    listRooms(): void {
      this.send(Op.RoomList)
    },

    /** 房主修改房间设置(仅大厅阶段) */
    updateRoom(patch: { name?: string; password?: string; public?: boolean }): void {
      this.send(Op.RoomUpdate, patch)
    },

    /** 房主解散房间 */
    dissolveRoom(): void {
      this.send(Op.RoomDissolve)
    },

    /** 房主增删机器人 */
    setBot(seat: number, add: boolean): void {
      this.send(Op.SeatBot, { seat, add })
    },

    /** 房主创建邀请 */
    createInvite(opts: { expiresInSec?: number; maxUses?: number; password?: string; bypassRoomPassword?: boolean } = {}): void {
      this.lastInviteToken = ''
      this.send(Op.InviteCreate, opts)
    },

    /** 房主查看邀请列表 */
    listInvites(): void {
      this.send(Op.InviteList)
    },

    /** 房主撤销邀请 */
    revokeInvite(id: string): void {
      this.send(Op.InviteRevoke, { id })
    },

    /** 断线重连: 请求增量事件与最新状态 */
    resync(): void {
      this.send(Op.Resync, { lastSeq: this.lastSeq })
    },

    /** P2P 模式连接 — 注册 webrtc DataChannel, 处理下行事件 */
    async _connectP2P(guestId: string, token: string): Promise<void> {
      const wrtc = useWebRTCStore()
      this.status = 'connecting'
      this._auth = { guestId, token }
      this._cleanupP2PListeners()
      const uplink = new Set<string>([
        Op.RoomJoin, Op.RoomLeave, Op.SeatReady, Op.SeatBot, Op.GameStart, Op.GameAction, Op.Ping, Op.Resync,
      ])
      this._p2pHandlers = [
        wrtc.onDataChannelMessage((peerId, data) => {
          let env: Envelope
          try { env = decode(data) } catch { return }
          // 记下来源 peer: 房主据此把 guestId 与信令 peer 绑定, 后续按座位单播私有视图.
          ;(env as any).__peer = peerId
          // 房主: 对端发来的上行 op 必须走 _sendP2P 的 op 分发,
          // 否则只进 handleEvent 会被忽略, 加入者永远进不了房间.
          if (wrtc.isHost && uplink.has(env.type)) {
            this._sendP2P(env)
            return
          }
          this.handleEvent(env)
        }),
      ]
      if (wrtc.isHost) {
        this.status = 'connected'
        // 房主初始 hello —— 先本地驱动, 再投影
        const helloEnv: Envelope = { v: PROTOCOL_VERSION, type: Ev.Hello, payload: { guestId, nickname: guestId.slice(0, 8) } }
        this.handleEvent(helloEnv)
        void this._projectLocalToRoom().then((view) => {
          if (view) this.room = view
        })
      } else {
        // 加入者: 对局中若本地视图丢失(页面刷新/HMR/丢包), 主动向房主 resync,
        // 否则房主会一直等这一步决策 —— 表现为「卡住」.
        const watch = setInterval(() => {
          if (this.transport !== 'p2p' || !this._auth) return
          if (this.room?.status !== 'playing') return
          if (useMatchStore().view) return
          this.send(Op.Resync)
        }, 2500)
        this._p2pHandlers.push(() => clearInterval(watch))
        // 阶段7: 房主的 DataChannel 断了就是"权威引擎随人走了"。
        // P2P 的牌墙/隐藏手牌只存在于房主浏览器内存里, 没有可信的备份可以
        // 交给新房主 —— 因此这里明确终止, 而不是让客户端假装还在打。
        this._p2pHandlers.push(
          wrtc.onDataChannelClose((peerId) => {
            if (this.transport !== 'p2p' || wrtc.isHost) return
            if (peerId !== wrtc.p2pHostPeerId) return
            this.hostLost = true
            this.status = 'closed'
            this.lastError = {
              code: 'p2p_host_lost',
              message: '房主已离开，P2P 对局无法恢复（牌墙与手牌只在房主浏览器里）。请重新创建房间开局。',
            }
          }),
        )
        // 加入者: DataChannel 打开即视为已连接 (可能已打开, 也可能稍后打开)
        const hostPeer = wrtc.p2pHostPeerId
        const dc = wrtc.peerDCs[hostPeer]
        if (dc && dc.readyState === 'open') {
          this.status = 'connected'
        } else {
          const off = wrtc.onDataChannelOpen((peerId) => {
            if (peerId === hostPeer) this.status = 'connected'
          })
          this._p2pHandlers.push(off)
        }
      }
    },
    _cleanupP2PListeners(): void {
      for (const h of this._p2pHandlers) h()
      this._p2pHandlers = []
    },

    /**
     * 房主侧: 把本地引擎给「房主自己座位」的投影送进对局 store。
     * 房主的牌桌视图与远端一样只来自引擎投影, 不走任何前端拼装。
     */
    _syncHostView(): void {
      const loc = useLocalGameStore()
      const v = loc.view
      if (!v) return
      useMatchStore().applyGameEvent({ kind: 'sync', view: v })
    },

    /**
     * 房主侧: 给每个 P2P 加入者单播「只属于该座位」的对局视图(含手牌)。
     * 房间级 RoomState 是公开投影; 手牌/摸牌/可行动作只能按座位私有下发,
     * 否则加入者看不到自己的牌(也不能作弊看到别人的)。
     */
    _unicastPrivateViews(): void {
      const wrtc = useWebRTCStore()
      const loc = useLocalGameStore()
      if (!wrtc.isHost || !loc.view) return
      this._syncHostView()
      // 以座位为准: pre-match 阶段在 RoomJoin 里记下的 seat → peerId 映射
      for (const [seatKey, peerId] of Object.entries(this._peerBySeat)) {
        const seat = Number(seatKey)
        if (!peerId || seat === loc.humanSeat) continue
        const seatView = loc.viewForSeat(seat)
        if (!seatView) continue
        wrtc.unicast(
          peerId,
          encode({
            v: PROTOCOL_VERSION,
            type: Ev.GameEvent,
            payload: { kind: 'sync', seat, view: seatView },
          } as Envelope),
        )
      }
    },

    /**
     * 房主侧: 对局中持续把「每个座位的私有视图」推给对应 peer。
     * 引擎的自动推进(摸牌/机器人/响应窗口)不会触发我们的 op 分发,
     * 所以只靠动作后的单播会漏状态 —— 这里用 300ms 轮询 + 变化检测补齐。
     */
    _startPrivateViewPump(): void {
      const wrtc = useWebRTCStore()
      const loc = useLocalGameStore()
      if (!wrtc.isHost || this._viewPumpTimer) return
      const last: Record<number, string> = {}
      this._viewPumpTimer = setInterval(() => {
        if (!wrtc.isHost || !loc.view) return
        // 房主自己的牌桌也要随引擎推进刷新(摸牌/机器人/AI 补位)
        this._syncHostView()
        for (const [seatKey, peerId] of Object.entries(this._peerBySeat)) {
          const seat = Number(seatKey)
          if (!peerId || seat === loc.humanSeat) continue
          const v = loc.viewForSeat(seat)
          if (!v) continue
          const key = [
            v.phase, v.turnSeat, v.turnStage, v.wallLeft,
            (v.hand || []).join(','), (v.actions || []).join(','),
            v.pendingTile ?? '', v.drawn ?? '',
            (v.melds || []).length,
            (v.discards || []).map((d) => (d || []).length).join('-'),
            v.result ? 'R' : '',
          ].join('|')
          if (last[seat] === key) continue
          last[seat] = key
          wrtc.unicast(
            peerId,
            encode({
              v: PROTOCOL_VERSION,
              type: Ev.GameEvent,
              payload: { kind: 'sync', seat, view: v },
            } as Envelope),
          )
        }
      }, 300)
    },

    _stopPrivateViewPump(): void {
      if (this._viewPumpTimer) {
        clearInterval(this._viewPumpTimer)
        this._viewPumpTimer = null
      }
    },

    /** 主动断开 */
    disconnect(): void {
      this._cleanupP2PListeners()
      this._stopPrivateViewPump()
      this._peerBySeat = {}
      this._peerByGuest = {}
      this._seatByPeer = {}
      this.stopHeartbeat()
      this.clearReconnectTimer()
      this._auth = null
      this.socket?.close()
      this.socket = null
      this.status = 'idle'
      this.transport = 'ws'
      this.room = null
      this.lastError = null
      this.actionSeq = 0
      this.lobby = []
    },
  },
})
