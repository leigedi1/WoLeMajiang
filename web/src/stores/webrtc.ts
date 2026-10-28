/**
 * WebRTC P2P 联机层 (信令 + PeerConnection + DataChannel)。
 * 设计目标: 作为房主时本地运行权威规则/投影, 其他 peer 只看自己的可见状态。
 * DataChannel 直接跑现有 game 协议 (envelope {v,seq,ref,type,payload}), 与在线服务器完全相同。
 * 信令端点: /ws/signal (独立, 不占在线托管 3 上限)。
 * 候选发现: STUN 默认 + 可选 TURN; 智能降级 — 若 LAN 候选直连 30s 失败则切 TURN。
 */
import { defineStore } from 'pinia'
import { basePath } from '../api/client'

export type SigStatus = 'idle' | 'connecting' | 'connected' | 'closed' | 'failed'
export type PeerConnStatus = 'new' | 'connecting' | 'connected' | 'failed' | 'closed'
export type FallbackLevel = 'lan' | 'stun' | 'turn'

export interface SigEnvelope {
  type: string
  peer?: string
  to?: string
  room?: string
  data?: unknown
  err?: { code: string; message: string }
  seq?: number
}

export interface P2PRoomInfo {
  code: string
  name: string
  host: string
  peers: number
  isHost: boolean
  password: boolean
}

/** 屏幕常亮锁的最小结构 (Wake Lock API 尚未进部分 TS DOM lib) */
interface WakeLockSentinelLike {
  release: () => Promise<void>
  addEventListener?: (type: string, listener: () => void) => void
}

export const useWebRTCStore = defineStore('webrtc', {
  state: () => ({
    sigStatus: 'idle' as SigStatus,
    sigError: null as string | null,
    sigSocket: null as WebSocket | null,
    myPeerId: '' as string,
    p2pRoomCode: '' as string,
    p2pIsHost: false as boolean,
    p2pHostPeerId: '' as string,
    p2pPeers: [] as { id: string; isHost: boolean }[],
    /** 已连接 peer → PeerConnection 映射 (房主有多个, 加入者只有 1 个 host) */
    peerConns: {} as Record<string, RTCPeerConnection>,
    /** 已打开 DataChannel 的 peer set */
    peerDCs: {} as Record<string, RTCDataChannel>,
    /** 信令 peer (房主只跟 host 交换 offer/answer) */
    signalingRoomPeers: [] as string[],
    /** 降级栈 */
    fallback: 'lan' as FallbackLevel,
    /** 可选 TURN 服务器 (由用户在 UI 填入) */
    turnUrls: [] as string[],
    turnUsername: '',
    turnCredential: '',
    /** 默认 STUN */
    stunUrls: ['stun:stun.l.google.com:19302', 'stun:stun1.l.google.com:19302'],
    /** 局域网模式: 只用主机候选(不配 STUN/TURN), 同网段直连 */
    lanOnly: false,
    /** 内部 refs */
    _pendingInvites: new Set<string>(),
    _dcOpenHandlers: [] as ((peerId: string, dc: RTCDataChannel) => void)[] as ((peerId: string, dc: RTCDataChannel) => void)[],
    _onMsgHandlers: [] as ((peerId: string, data: string) => void)[] as ((peerId: string, data: string) => void)[],
    /** 阶段7: DataChannel 关闭回调(失去房主连接时用来明确终止对局) */
    _dcCloseHandlers: [] as ((peerId: string) => void)[],
    /** 信令 Promise 握手状态 —— 必须放 state!
     *  放进 actions 会被 Pinia 包成函数 (恒真), 导致 connectSignaling 永不 resolve. */
    _sigResolved: false as boolean,
    /** 挂起中的信令 Promise 回调 (room.create.ok / room.join.ok 等) */
    _tmpHandlers: [] as ((env: SigEnvelope) => void)[],
    /** 远端描述未就绪时缓存的 ICE candidate */
    _pendingIce: {} as Record<string, RTCIceCandidateInit[]>,
    /** 房主重连令牌: 信令断线后凭它用原房间码重新占位 (服务端 room.rehost) */
    p2pHostToken: '' as string,
    /** 信令自动重连状态 */
    _sigReconnectTimer: 0 as number,
    _sigRetry: 0 as number,
    _sigClosing: false as boolean,
    /** 保活探测 */
    _sigPingTimer: 0 as number,
    _sigLastPong: 0 as number,
    /** 屏幕常亮 (P2P 房主必须保持前台, 否则房间会掉线) */
    _wakeLock: null as WakeLockSentinelLike | null,
  }),
  getters: {
    sigConnected: (s) => s.sigStatus === 'connected',
    p2pRoomJoined: (s) => !!s.p2pRoomCode,
    isHost: (s) => s.p2pIsHost,
    allDataChannelsOpen: (s) => {
      if (s.p2pPeers.length === 0) return false
      return s.p2pPeers.every((p) => p.isHost || (s.peerDCs[p.id] && s.peerDCs[p.id].readyState === 'open'))
    },
  },
  actions: {
    /** 建立信令 WebSocket 到 /ws/signal */
    connectSignaling(peerId?: string): Promise<string> {
      return new Promise((resolve, reject) => {
        if (this.sigStatus === 'connected' && this.myPeerId) {
          resolve(this.myPeerId)
          return
        }
        this._sigClosing = false
        const proto = location.protocol === 'https:' ? 'wss' : 'ws'
        const qs = peerId ? `?peer=${encodeURIComponent(peerId)}` : ''
        const url = `${proto}://${location.host}${basePath('/ws/signal')}${qs}`
        this.sigStatus = 'connecting'
        this.sigError = null
        const ws = new WebSocket(url)
        this.sigSocket = ws
        // 连上后 10s 内必须收到带 peer 的首条消息, 否则按失败处理并触发重连。
        const openTimer = window.setTimeout(() => {
          if (this.sigStatus === 'connecting') {
            try { ws.close() } catch { /* ignore */ }
          }
        }, 10_000)

        ws.onopen = () => {
          // 等 room.join.ok 里带回 peer id (或直接查 server 的 echo)
        }
        ws.onerror = () => {
          window.clearTimeout(openTimer)
          if (this.sigStatus === 'connecting') {
            this.sigStatus = 'failed'
            this.sigError = '信令 WebSocket 连接失败'
            reject(new Error('信令连接失败'))
          }
        }
        ws.onclose = () => {
          window.clearTimeout(openTimer)
          this._stopSigPing()
          this.sigSocket = null
          if (this._sigClosing) {
            this.sigStatus = 'closed'
            return
          }
          this.sigStatus = 'closed'
          // 房间还在(房主/牌局中)时自动重连并用原房间码 rehost,
          // 否则手机切后台回来房间就没了、朋友再进也进不来。
          if (this.p2pRoomCode) this._scheduleSigReconnect()
        }
        ws.onmessage = (ev) => {
          this._sigLastPong = Date.now()
          let env: SigEnvelope
          try {
            env = JSON.parse(String(ev.data))
          } catch {
            return
          }
          // 第一条 room.join.ok (或 room.create.ok) 会带 peer 字段
          if (!this.myPeerId && env.peer) {
            this.myPeerId = env.peer
            this.sigStatus = 'connected'
            window.clearTimeout(openTimer)
            this._sigRetry = 0
            this._startSigPing()
          }
          // 如果 Promise 还在等待, 用第一条带 peer 的消息 resolve
          if (this.myPeerId && !this._sigResolved) {
            this._sigResolved = true
            resolve(this.myPeerId)
          }
          this.handleSigEnvelope(env)
          // 分发到 _tmpHandlers 里挂的 Promise (room.create.ok / room.join.ok 等)
          for (const h of [...this._tmpHandlers]) h(env)
        }
      })
    },

    /** 信令断线后按退避重连, 连上后房主自动 rehost 原房间码 */
    _scheduleSigReconnect(): void {
      if (this._sigReconnectTimer) return
      const delay = Math.min(8000, 800 * Math.pow(1.7, this._sigRetry))
      this._sigRetry += 1
      this._sigReconnectTimer = window.setTimeout(async () => {
        this._sigReconnectTimer = 0
        try {
          // 复用旧 peerId: 服务端按 peerId 复用 Peer/Room 成员, rehost 后仍是同一个人
          const oldPeerId = this.myPeerId
          this.myPeerId = ''
          this._sigResolved = false
          await this.connectSignaling(oldPeerId || undefined)
          if (this.p2pIsHost && this.p2pRoomCode && this.p2pHostToken) {
            await this.rehostP2PRoom(this.p2pRoomCode, this.p2pHostToken)
          } else if (this.p2pRoomCode) {
            // 加入者: 重新加入信令房间, 让房主知道我还活着
            this.sendSig({ type: 'room.join', room: this.p2pRoomCode, data: { code: this.p2pRoomCode } })
          }
        } catch {
          this._scheduleSigReconnect()
        }
      }, delay)
    },

    /** 保活: 移动端切网/休眠会把 TCP 变成「假连接」, 定期 ping 才能及时发现 */
    _startSigPing(): void {
      this._stopSigPing()
      this._sigLastPong = Date.now()
      this._sigPingTimer = window.setInterval(() => {
        if (this.sigStatus !== 'connected') return
        if (Date.now() - this._sigLastPong > 45_000) {
          // 45s 没有任何回音: 判定假连接, 主动断开以触发重连
          try { this.sigSocket?.close() } catch { /* ignore */ }
          return
        }
        try { this.sendSig({ type: 'room.ping' }) } catch { /* ignore */ }
      }, 15_000)
    },

    _stopSigPing(): void {
      if (this._sigPingTimer) {
        window.clearInterval(this._sigPingTimer)
        this._sigPingTimer = 0
      }
    },

    /** 房主: 信令断线后用原房间码 + hostToken 重新占位 */
    rehostP2PRoom(code: string, hostToken: string): Promise<void> {
      return new Promise((resolve, reject) => {
        if (this.sigStatus !== 'connected') {
          reject(new Error('信令未连接, 无法恢复房间'))
          return
        }
        const t = window.setTimeout(() => {
          this._removeHandler(h)
          reject(new Error('恢复房间超时'))
        }, 10_000)
        const h = (env: SigEnvelope) => {
          if (env.type === 'room.error') {
            window.clearTimeout(t)
            this._removeHandler(h)
            reject(new Error(env.err?.message || '恢复房间失败'))
            return
          }
          if (env.type !== 'room.rehost.ok') return
          window.clearTimeout(t)
          this.p2pRoomCode = String((env.data as any)?.code ?? code)
          this.p2pIsHost = true
          this.p2pHostToken = String((env.data as any)?.hostToken ?? hostToken)
          this.p2pHostPeerId = this.myPeerId
          this._removeHandler(h)
          resolve()
        }
        this._addHandler(h)
        this.sendSig({ type: 'room.rehost', room: code, data: { code, hostToken } })
      })
    },

    /** 申请屏幕常亮: P2P 房主页面被挂起等于房间掉线 */
    async requestWakeLock(): Promise<void> {
      try {
        const nav = navigator as Navigator & {
          wakeLock?: { request: (type: 'screen') => Promise<WakeLockSentinelLike> }
        }
        if (!nav.wakeLock || document.visibilityState !== 'visible') return
        if (this._wakeLock) return
        const lock = await nav.wakeLock.request('screen')
        this._wakeLock = lock
        // 锁被系统释放(切后台回来)时自动重新申请
        lock.addEventListener?.('release', () => {
          this._wakeLock = null
        })
      } catch {
        /* 不支持或被拒绝: 只影响体验, 不影响功能 */
      }
    },

    async releaseWakeLock(): Promise<void> {
      if (!this._wakeLock) return
      try { await this._wakeLock.release() } catch { /* ignore */ }
      this._wakeLock = null
    },

    /** 发送信令消息 */
    sendSig(env: SigEnvelope): void {
      if (!this.sigSocket || this.sigSocket.readyState !== WebSocket.OPEN) {
        throw new Error('信令未连接')
      }
      this.sigSocket.send(JSON.stringify(env))
    },

    /** 房主创建 P2P 房间 (lanOnly = 局域网模式: 只用主机候选, 不走 STUN/TURN) */
    async createP2PRoom(
      name: string,
      password = '',
      opts: { lanOnly?: boolean } = {},
    ): Promise<P2PRoomInfo> {
      this.lanOnly = !!opts.lanOnly
      await this.connectSignaling()
      this.sendSig({ type: 'room.create', data: { name, password } })
      return new Promise((resolve, reject) => {
        const t = setTimeout(() => reject(new Error('创建房间超时')), 10_000)
        const h = (env: SigEnvelope) => {
          if (env.type === 'room.error') {
            clearTimeout(t)
            this._removeHandler(h)
            reject(new Error(env.err?.message || '创建房间失败'))
            return
          }
          if (env.type !== 'room.create.ok') return
          clearTimeout(t)
          this.p2pRoomCode = String((env.data as any)?.code ?? '')
          this.p2pHostToken = String((env.data as any)?.hostToken ?? '')
          this.p2pIsHost = true
          this.p2pHostPeerId = this.myPeerId
          this.signalingRoomPeers = [this.myPeerId]
          this._removeHandler(h)
          resolve({
            code: this.p2pRoomCode,
            name: String((env.data as any)?.name ?? name),
            host: String((env.data as any)?.host ?? this.myPeerId),
            peers: 1,
            isHost: true,
            password: !!((env.data as any)?.password),
          })
        }
        this._addHandler(h)
      })
    },

    /** 按房间码加入 P2P 房间 */
    async joinP2PRoom(code: string, password = ''): Promise<P2PRoomInfo> {
      await this.connectSignaling()
      this.sendSig({ type: 'room.join', room: code.toUpperCase(), data: { code: code.toUpperCase(), password } })
      return new Promise((resolve, reject) => {
        const t = setTimeout(() => reject(new Error('加入房间超时')), 10_000)
        const h = (env: SigEnvelope) => {
          if (env.type === 'room.error') {
            clearTimeout(t)
            this._removeHandler(h)
            reject(new Error(env.err?.message || '加入房间失败'))
            return
          }
          if (env.type !== 'room.join.ok') return
          clearTimeout(t)
          this.p2pRoomCode = code.toUpperCase()
          this.p2pIsHost = false
          // peers 列表第一位通常是 host
          const snap = env.data as { room?: string; host?: string; peers?: any[] } | undefined
          if (snap) {
            this.p2pHostPeerId = String(snap.host ?? '')
            this.p2pPeers = (snap.peers ?? []).map((p: any) => ({ id: String(p.id), isHost: !!p.isHost }))
            this.signalingRoomPeers = [this.myPeerId, this.p2pHostPeerId].filter(Boolean)
          }
          this._removeHandler(h)
          resolve({
            code: this.p2pRoomCode,
            name: '',
            host: this.p2pHostPeerId,
            peers: (this.p2pPeers.length || 0) + 1,
            isHost: false,
            password: false,
          })
        }
        this._addHandler(h)
      })
    },

    /** 建立到 host 的 PeerConnection + DataChannel (非房主调用) */
    async connectToHost(): Promise<void> {
      if (this.p2pIsHost || !this.p2pHostPeerId) return
      const pc = this._newPeerConnection()
      this.peerConns[this.p2pHostPeerId] = pc

      // 收集 host 的 ICE candidates 并通过信令发回
      pc.onicecandidate = (ev) => {
        if (!ev.candidate) return
        this.sendSig({ type: 'signal.ice', to: this.p2pHostPeerId, data: ev.candidate })
      }
      pc.oniceconnectionstatechange = () => {
        const st = pc.iceConnectionState
        if (st === 'connected' || st === 'completed') {
          this.fallback = 'lan' // 直连成功, 不需要 TURN
        } else if (st === 'failed') {
          this.fallback = 'turn' // 房主未提供 TURN 时此处只提示, 实际 TURN 配不配对取决于房主
          this.sigError = 'P2P 连接失败, 尝试 TURN 中继失败'
        }
      }
      pc.ondatachannel = (ev) => {
        const dc = ev.channel
        this._bindDataChannel(this.p2pHostPeerId, dc)
      }
      // 加入者 (offer 方) 必须自己创建 DataChannel:
      // 若两边都不建, SDP 里没有 m=application, 谁也不会收到 ondatachannel.
      const localDC = pc.createDataChannel('majiang', { ordered: true })
      this._bindDataChannel(this.p2pHostPeerId, localDC)
      // 发 offer → 等 answer → 等 DataChannel 真正 open。
      // 必须等 dc open 再 resolve: App.vue 的 onJoinRoom 随后立刻 conn.joinRoom,
      // 若 dc 尚未就绪, 房主收不到 RoomJoin, 名单里永远没有这个玩家.
      const offer = await pc.createOffer()
      await pc.setLocalDescription(offer)
      // 先把本地候选收集完再发 offer: 少一次 trickle 往返, 局域网内快很多。
      await new Promise<void>((resolve) => {
        if (pc.iceGatheringState === 'complete') {
          resolve()
          return
        }
        const done = (): void => {
          if (pc.iceGatheringState === 'complete') {
            pc.removeEventListener('icegatheringstatechange', done)
            resolve()
          }
        }
        pc.addEventListener('icegatheringstatechange', done)
        setTimeout(resolve, 3000)
      })
      this.sendSig({ type: 'signal.offer', to: this.p2pHostPeerId, data: pc.localDescription ?? offer })
      await new Promise<void>((resolve, reject) => {
        const off = this.onDataChannelOpen((peerId) => {
          if (peerId !== this.p2pHostPeerId) return
          clearTimeout(timer)
          off()
          resolve()
        })
        const timer = setTimeout(() => {
          off()
          reject(new Error('P2P 连接超时 (DataChannel 未打开, 40s)'))
        }, 40000)
      })
    },

    /** 房主收到 join 后创建到新 peer 的 PC + DataChannel, 发 offer */
    async hostIncomingPeer(peerId: string): Promise<void> {
      if (!this.p2pIsHost || this.peerConns[peerId]) return
      const pc = this._newPeerConnection()
      this.peerConns[peerId] = pc
      pc.onicecandidate = (ev) => {
        if (!ev.candidate) return
        this.sendSig({ type: 'signal.ice', to: peerId, data: ev.candidate })
      }
      pc.oniceconnectionstatechange = () => {
        if (pc.iceConnectionState === 'failed') {
          this.sigError = `与 ${peerId} 的 P2P 连接失败 (尝试 TURN 中继)`
          this.fallback = 'turn'
        }
      }
      pc.onconnectionstatechange = () => {
        const st = pc.connectionState
        if (st === 'failed' || st === 'closed') {
          delete this.peerConns[peerId]
          const dc = this.peerDCs[peerId]
          if (dc) { try { dc.close() } catch {}; delete this.peerDCs[peerId] }
        }
      }
      // 房主主动开 DataChannel
      const dc = pc.createDataChannel('majiang', { ordered: true, maxRetransmits: 3 })
      this._bindDataChannel(peerId, dc)
      const offer = await pc.createOffer()
      await pc.setLocalDescription(offer)
      this.sendSig({ type: 'signal.offer', to: peerId, data: offer })
    },

    /** 统一构造 RTCPeerConnection, 含候选策略与 TURN 配置 */
    _newPeerConnection(): RTCPeerConnection {
      const iceServers: RTCIceServer[] = []
      // 局域网模式: 只用主机候选(同网段直连), 不请求 STUN 也不中继
      if (!this.lanOnly) {
        if (this.stunUrls.length) iceServers.push({ urls: this.stunUrls })
        if (this.turnUrls.length) {
          iceServers.push({ urls: this.turnUrls, username: this.turnUsername, credential: this.turnCredential })
        }
      }
      return new RTCPeerConnection({ iceServers })
    },

    _bindDataChannel(peerId: string, dc: RTCDataChannel): void {
      this.peerDCs[peerId] = dc
      dc.onopen = () => {
        // 通知外部 (connection store 在 onDCConnected 里接)
        for (const h of this._dcOpenHandlers) h(peerId, dc)
      }
      dc.onmessage = (ev) => {
        for (const h of this._onMsgHandlers) h(peerId, String(ev.data))
      }
      dc.onclose = () => {
        // 阶段7: 通知外部。P2P 加入者失去房主通道时无法可信恢复牌局
        // (牌墙与手牌只存在于房主浏览器里), 必须明确终止而不是假装还在打。
        for (const h of this._dcCloseHandlers) h(peerId)
      }
    },

    /** 信令分发 */
    handleSigEnvelope(env: SigEnvelope): void {
      switch (env.type) {
        case 'room.join.ok':
        case 'room.create.ok':
        case 'room.error':
        case 'peer.close':
        case 'room.list':
        case 'room.peers':
          // 上面这些已经在各自 Promise handler 里消费了, 这里也存一下 room.peers
          if (env.type === 'room.peers' && this.p2pIsHost) {
            const snap = env.data as { peers?: any[]; host?: string } | undefined
            if (snap?.peers) {
              this.p2pPeers = (snap.peers as any[]).map((p) => ({ id: String(p.id), isHost: !!p.isHost }))
              // 注意: 不再房主主动发 offer (会与加入者 offer 撞车/glare)。
              // 连接一律由加入者 offer + 建 DataChannel, 房主只作答。
            }
          }
          if (env.type === 'room.error') {
            this.sigError = String((env.data as any)?.err?.message ?? (env as any).err?.message ?? '房间错误')
          }
          break
        case 'signal.offer': {
          const pc = this.peerConns[env.peer ?? ''] ?? this._newPeerConnection()
          if (!this.peerConns[env.peer ?? '']) {
            this.peerConns[env.peer ?? ''] = pc
            pc.onicecandidate = (ev) => {
              if (!ev.candidate) return
              this.sendSig({ type: 'signal.ice', to: env.peer!, data: ev.candidate })
            }
            pc.ondatachannel = (ev) => {
              this._bindDataChannel(env.peer!, ev.channel)
            }
          }
          pc.setRemoteDescription(env.data as RTCSessionDescriptionInit).then(async () => {
            this._flushIce(env.peer ?? '', pc)
            const ans = await pc.createAnswer()
            await pc.setLocalDescription(ans)
            this.sendSig({ type: 'signal.answer', to: env.peer!, data: ans })
          })
          break
        }
        case 'signal.answer': {
          const pc = this.peerConns[env.peer ?? '']
          if (pc) {
            pc.setRemoteDescription(env.data as RTCSessionDescriptionInit)
              .then(() => this._flushIce(env.peer ?? '', pc))
              .catch(() => {})
          }
          break
        }
        case 'signal.ice': {
          const peer = env.peer ?? ''
          const pc = this.peerConns[peer]
          const cand = env.data as RTCIceCandidateInit
          // 远端描述尚未设置时先缓存, 否则 addIceCandidate 会因状态错误被丢弃
          if (!pc || !pc.remoteDescription) {
            if (!this._pendingIce[peer]) this._pendingIce[peer] = []
            this._pendingIce[peer].push(cand)
            break
          }
          pc.addIceCandidate(cand).catch(() => {})
          break
        }
        default:
          break
      }
    },

    /** 外部订阅 DataChannel open (connection store 用) */
    onDataChannelOpen(handler: (peerId: string, dc: RTCDataChannel) => void): () => void {
      this._dcOpenHandlers.push(handler)
      return () => {
        this._dcOpenHandlers = this._dcOpenHandlers.filter((h) => h !== handler)
      }
    },
    onDataChannelMessage(handler: (peerId: string, data: string) => void): () => void {
      this._onMsgHandlers.push(handler)
      return () => {
        this._onMsgHandlers = this._onMsgHandlers.filter((h) => h !== handler)
      }
    },
    /** 外部订阅 DataChannel 关闭 (connection store 用来判定"失去房主") */
    onDataChannelClose(handler: (peerId: string) => void): () => void {
      this._dcCloseHandlers.push(handler)
      return () => {
        this._dcCloseHandlers = this._dcCloseHandlers.filter((h) => h !== handler)
      }
    },

    /** 房主: 向所有 DataChannel 广播麻将 envelope */
    broadcastGameEnvelope(json: string): void {
      for (const peerId of Object.keys(this.peerDCs)) {
        const dc = this.peerDCs[peerId]
        if (dc && dc.readyState === 'open') {
          try { dc.send(json) } catch {}
        }
      }
    },

    /** 房主: 只发给某一个 peer (按座位下发私有视图, 手牌不外泄) */
    unicast(peerId: string, json: string): void {
      const dc = this.peerDCs[peerId]
      if (dc && dc.readyState === 'open') {
        try { dc.send(json) } catch {}
      }
    },

    /** 离开 P2P 房间 + 断信令 */
    async leaveP2P(): Promise<void> {
      this._sigClosing = true
      if (this._sigReconnectTimer) {
        window.clearTimeout(this._sigReconnectTimer)
        this._sigReconnectTimer = 0
      }
      this._stopSigPing()
      await this.releaseWakeLock()
      try {
        if (this.p2pRoomCode && this.myPeerId) {
          this.sendSig({ type: 'room.leave', room: this.p2pRoomCode })
        }
      } catch {}
      for (const peerId of Object.keys(this.peerConns)) {
        try { this.peerConns[peerId].close() } catch {}
      }
      for (const peerId of Object.keys(this.peerDCs)) {
        try { this.peerDCs[peerId].close() } catch {}
      }
      this.peerConns = {}
      this.peerDCs = {}
      this.p2pRoomCode = ''
      this.p2pIsHost = false
      this.p2pHostPeerId = ''
      this.p2pHostToken = ''
      this.p2pPeers = []
      if (this.sigSocket) {
        try { this.sigSocket.close() } catch {}
        this.sigSocket = null
      }
      this.sigStatus = 'idle'
      // 清掉握手状态与挂起回调, 否则下次 connectSignaling 会直接短路
      this.myPeerId = ''
      this._sigResolved = false
      this._tmpHandlers = []
    },

    /** 页面回到前台/网络恢复时, 若原本在 P2P 房间里则立刻把信令补回来 */
    ensureSignaling(): void {
      if (!this.p2pRoomCode) return
      this._sigClosing = false
      if (this.sigStatus === 'connected') return
      this._sigRetry = 0
      if (this._sigReconnectTimer) return
      this._scheduleSigReconnect()
    },

    /** 设置 TURN 服务器 (用户在 UI 可选填) */
    setTurn(urls: string[], username: string, credential: string): void {
      this.turnUrls = urls
      this.turnUsername = username
      this.turnCredential = credential
    },

    /** 设置降级栈 */
    setFallback(f: FallbackLevel): void {
      this.fallback = f
    },

    /** 内部 Promise handler 注册表 (room.create.ok 等) — 简化版, 仅用 type 匹配, 每次只留最后一个 */
    _addHandler(h: (env: SigEnvelope) => void): void {
      this._tmpHandlers.push(h)
    },
    _removeHandler(h: (env: SigEnvelope) => void): void {
      this._tmpHandlers = this._tmpHandlers.filter((x) => x !== h)
    },
    /** 远端描述就绪后补灌缓存的 ICE candidate */
    _flushIce(peerId: string, pc: RTCPeerConnection): void {
      const pend = this._pendingIce[peerId]
      if (!pend || pend.length === 0) return
      delete this._pendingIce[peerId]
      for (const c of pend) pc.addIceCandidate(c).catch(() => {})
    },
  },
})
