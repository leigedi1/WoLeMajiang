#!/usr/bin/env node
/**
 * 阶段5 在线联机端到端验证(真实 WebSocket, 多客户端)。
 *
 * 覆盖:
 *  1. 游客身份 / 健康检查
 *  2. 建房(名称/密码/公开性) + 公开大厅只列公开房间
 *  3. 在线房间上限(默认 3)阻止超额创建
 *  4. 房间密码: 错误密码被拒, 正确密码通过
 *  5. 邀请令牌: 一次性(maxUses=1)消费, 重复使用被拒, 得不到房间密码也可进入
 *  6. 准备/满座/开局约束
 *  7. 4 客户端真实对局打到终局, 各端分数守恒且一致
 *  8. 对局中断线重连: 用同一游客身份重连 + resync 恢复座位与手牌
 *  9. 房主解散房间后所有客户端收到空房间状态
 *
 * 用法: node scripts/e2e-online.mjs [base]
 *   base 默认 http://127.0.0.1:18080
 */
import process from 'node:process'

const BASE = (process.argv[2] || 'http://127.0.0.1:18080').replace(/\/$/, '')
const WS_URL = BASE.replace(/^http/, 'ws') + '/ws'
const PROTOCOL_VERSION = 1

let pass = 0
const failures = []
const notes = []

function ok(label) {
  pass++
  notes.push(`  ✓ ${label}`)
}
function fail(label, detail) {
  failures.push(`${label}${detail ? ` :: ${detail}` : ''}`)
  notes.push(`  ✗ ${label}${detail ? ` — ${detail}` : ''}`)
}
function check(label, cond, detail) {
  if (cond) ok(label)
  else fail(label, detail)
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function waitFor(fn, timeoutMs = 4000, label = 'condition') {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (fn()) return true
    await sleep(40)
  }
  throw new Error(`等待超时: ${label}`)
}

async function createGuest(nickname) {
  const resp = await fetch(`${BASE}/api/v1/guests`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ nickname }),
  })
  if (!resp.ok) throw new Error(`创建游客失败 ${resp.status}: ${await resp.text()}`)
  return resp.json()
}

class Client {
  constructor(label) {
    this.label = label
    this.ws = null
    this.ref = 0
    this.hello = null
    this.room = null
    this.view = null
    this.errors = []
    this.seqs = []
    this.lobby = null
    this.invite = null
    this.invites = null
    this.actionSeq = 0
    this.sawRoomGone = false
  }

  connect(cred, timeoutMs = 6000) {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(WS_URL)
      this.ws = ws
      const timer = setTimeout(() => reject(new Error(`${this.label} 连接超时`)), timeoutMs)
      ws.addEventListener('open', () => {
        this.send('guest.hello', { guestId: cred.guestId, token: cred.token })
      })
      ws.addEventListener('message', (ev) => {
        let env
        try {
          env = JSON.parse(String(ev.data))
        } catch {
          return
        }
        if (typeof env.seq === 'number' && env.seq > 0) {
          if (this.seqs.length && env.seq <= this.seqs[this.seqs.length - 1]) {
            this.errors.push({ code: 'seq_not_monotonic', message: `seq ${env.seq}` })
          }
          this.seqs.push(env.seq)
        }
        switch (env.type) {
          case 'hello':
            this.hello = env.payload
            clearTimeout(timer)
            resolve(this)
            break
          case 'room.state':
            this.room = env.payload && env.payload.roomId ? env.payload : null
            if (!this.room) this.sawRoomGone = true
            break
          case 'game.event':
            if (env.payload?.view) this.view = env.payload.view
            break
          case 'room.list':
            this.lobby = env.payload
            break
          case 'invite.created':
            this.invite = env.payload
            break
          case 'invite.list':
            this.invites = env.payload
            break
          case 'error':
            this.errors.push(env.payload)
            break
          default:
            break
        }
      })
      ws.addEventListener('error', () => {
        clearTimeout(timer)
        reject(new Error(`${this.label} WS 错误`))
      })
    })
  }

  send(type, payload) {
    if (!this.ws || this.ws.readyState !== 1) throw new Error(`${this.label} 未连接`)
    this.ws.send(
      JSON.stringify({ v: PROTOCOL_VERSION, type, ref: `r${++this.ref}`, ...(payload === undefined ? {} : { payload }) }),
    )
  }

  action(action, payload = {}) {
    this.actionSeq += 1
    this.send('game.action', { action, payload, actionSeq: this.actionSeq })
  }

  takeErrors() {
    const out = this.errors
    this.errors = []
    return out
  }

  close() {
    try {
      this.ws?.close()
    } catch {
      /* ignore */
    }
  }
}

function myTurnStage(c) {
  const v = c.view
  if (!v) return null
  return { v, myTurn: v.turnSeat === v.seat }
}

/** 把一局打到终局; 掉的客户端会用同一游客身份重连一次。 */
async function playMatch(clients, { reconnectAt = 40, timeoutMs = 120000 } = {}) {
  const deadline = Date.now() + timeoutMs
  let acts = 0
  let reconnected = false
  let reconnectedSeat = null
  let reconnectedHand = null

  while (Date.now() < deadline) {
    // 中途断线重连验证(只做一次), 放在终局判断之前:
    // 否则短局可能在触发重连前就结束了, 用例就永远跑不到。
    if (!reconnected && acts >= reconnectAt) {
      reconnected = true
      const victim = clients[2]
      const cred = victim.cred
      reconnectedSeat = victim.view?.seat ?? null
      reconnectedHand = JSON.stringify(victim.view?.hand ?? null)
      victim.close()
      await sleep(150)
      const fresh = new Client(`${victim.label}·重连`)
      fresh.cred = cred
      await fresh.connect(cred)
      fresh.send('sync.resync', { lastSeq: 0 })
      await waitFor(() => fresh.room !== null, 5000, '重连后房间状态')
      await waitFor(() => fresh.view !== null, 8000, '重连后对局视图')
      const idx = clients.indexOf(victim)
      clients[idx] = fresh
      fresh.reconnectedSeat = reconnectedSeat
      fresh.reconnectedHand = reconnectedHand
      fresh.reconnectSeatMatch = fresh.view.seat === reconnectedSeat
      fresh.reconnectHandMatch = JSON.stringify(fresh.view.hand ?? null) === reconnectedHand
    }

    if (clients.every((c) => c.view?.phase === 'finished' || c.view?.result)) {
      return { acts, reconnected, clients }
    }

    for (const c of clients) {
      const v = c.view
      if (!v || v.phase === 'finished' || v.result) continue
      if (v.actions?.includes('hu.self')) {
        c.action('hu.self')
        acts++
        continue
      }
      if (v.pendingTile && v.actions?.includes('pass')) {
        c.action('pass')
        acts++
        continue
      }
      const mine = v.turnSeat === v.seat
      if (!mine) continue
      if (v.turnStage === 'draw') {
        c.action('draw')
        acts++
      } else if (v.turnStage === 'discard') {
        const tile = v.drawn || v.hand?.[v.hand.length - 1]
        if (tile) {
          c.action('discard', { tile })
          acts++
        }
      }
    }

    await sleep(60)
  }
  return { acts, reconnected, clients, timedOut: true }
}

// ---------------------------------------------------------------- 主流程

async function main() {
  // 1. 健康检查
  const health = await (await fetch(`${BASE}/api/v1/health`)).json()
  check('健康检查返回 ok', health.status === 'ok', JSON.stringify(health))
  check('健康检查含在线房间上限', typeof health.maxOnlineRooms === 'number', JSON.stringify(health))

  // 2. 游客身份
  const [gHost, gP2, gP3, gP4, gInvitee, gR2, gR3, gR4] = await Promise.all([
    createGuest('房主'),
    createGuest('玩家二'),
    createGuest('玩家三'),
    createGuest('玩家四'),
    createGuest('邀请客'),
    createGuest('限额二'),
    createGuest('限额三'),
    createGuest('限额四'),
  ])
  check('创建 8 个游客身份', [gHost, gP2, gP3, gP4].every((g) => g.guestId && g.token))

  const host = new Client('房主')
  await host.connect(gHost)
  host.cred = gHost
  check('WS 握手成功并确认身份', host.hello?.guestId === gHost.guestId)

  // 3. 建房(公开 + 密码)
  host.send('room.create', { name: '密码测试房', password: '1234', public: true, theme: 'classic' })
  await waitFor(() => host.room !== null, 5000, '建房房间状态')
  const roomCode = host.room.code
  check('房间邀请码为 6 位', /^[A-Z0-9]{6}$/.test(roomCode || ''), String(roomCode))
  check('房间名生效', host.room.name === '密码测试房', String(host.room.name))
  check('公开房间标记正确', host.room.isPublic === true)
  check('密码标记正确(不含明文)', host.room.hasPassword === true && !JSON.stringify(host.room).includes('1234'))
  check('房主被标记', host.room.players.some((p) => p.isHost && p.guestId === gHost.guestId))
  check('开局前规则未锁定', host.room.rulesLocked === false)

  // 4. 公开大厅
  host.send('room.list')
  await waitFor(() => host.lobby !== null, 3000, '大厅快照')
  const listed = (host.lobby.rooms || []).find((r) => r.code === roomCode)
  check('公开房间出现在大厅', Boolean(listed))
  check('大厅条目含房主昵称与人数', listed?.hostNickname === '房主' && listed?.players === 1, JSON.stringify(listed))

  // 5. 在线房间上限
  const r2 = new Client('限额二')
  await r2.connect(gR2)
  r2.cred = gR2
  r2.send('room.create', { name: '第二间', public: true })
  await waitFor(() => r2.room !== null, 5000, '第二间房')

  const r3 = new Client('限额三')
  await r3.connect(gR3)
  r3.cred = gR3
  r3.send('room.create', { name: '私有第三间', public: false })
  await waitFor(() => r3.room !== null, 5000, '第三间房')

  host.send('room.list')
  await sleep(200)
  const codes = (host.lobby?.rooms || []).map((r) => r.code)
  check('私有房间不出现在公开大厅', !codes.includes(r3.room.code), JSON.stringify(codes))

  const r4 = new Client('限额四')
  await r4.connect(gR4)
  r4.cred = gR4
  r4.send('room.create', { name: '第四间', public: true })
  await waitFor(() => r4.errors.length > 0 || r4.room !== null, 4000, '超额建房结果')
  const overErr = r4.errors
  check(
    '第 4 个在线房间被拒绝(max_online_rooms)',
    r4.room === null && (overErr.some((e) => e.code === 'max_online_rooms') || overErr.some((e) => e.code === 'max_online_rooms')),
    JSON.stringify(overErr),
  )
  r4.close()

  // 释放两间房, 让主房间继续使用
  r2.send('room.dissolve')
  r3.send('room.dissolve')
  await sleep(300)
  r2.close()
  r3.close()

  // 6. 密码验证
  const p2 = new Client('玩家二')
  await p2.connect(gP2)
  p2.cred = gP2
  p2.send('room.join', { code: roomCode, password: 'wrong' })
  await waitFor(() => p2.errors.length > 0 || p2.room !== null, 4000, '错误密码结果')
  const badPw = p2.errors
  check('错误房间密码被拒绝', p2.room === null && badPw.some((e) => e.code === 'bad_room_password'), JSON.stringify(badPw))

  p2.send('room.join', { code: roomCode.toLowerCase(), password: '1234' })
  await waitFor(() => p2.room !== null, 5000, '正确密码加入')
  check('正确密码加入并规范化小写邀请码', p2.room?.code === roomCode)
  check('房主看到新玩家', await waited(() => host.room?.players?.length === 2, 4000))

  // 7. 邀请令牌(一次性, 免房间密码)
  host.send('invite.create', { maxUses: 1, expiresInSec: 3600, bypassRoomPassword: true })
  await waitFor(() => host.invite !== null, 4000, '创建邀请')
  const token = host.invite?.token
  check('邀请令牌长度足够', typeof token === 'string' && token.length >= 32, String(token))
  check('邀请视图不回传令牌哈希', !JSON.stringify(host.invite.invite).includes('tokenHash'))

  const p3 = new Client('玩家三')
  await p3.connect(gP3)
  p3.cred = gP3
  p3.send('invite.join', { token, code: roomCode })
  await waitFor(() => p3.room !== null, 5000, '令牌加入')
  check('免密令牌可直接加入', p3.room?.players?.length === 3)

  const inv = new Client('邀请客')
  await inv.connect(gInvitee)
  inv.cred = gInvitee
  inv.send('invite.join', { token, code: roomCode })
  await waitFor(() => inv.errors.length > 0 || inv.room !== null, 4000, '令牌复用结果')
  const reuseErr = inv.errors
  check('一次性令牌不可重复使用', inv.room === null && reuseErr.some((e) => e.code === 'invite_used_up'), JSON.stringify(reuseErr))
  inv.close()

  // 第 4 人用房间密码加入
  const p4 = new Client('玩家四')
  await p4.connect(gP4)
  p4.cred = gP4
  p4.send('room.join', { code: roomCode, password: '1234' })
  await waitFor(() => p4.room !== null, 5000, '第四人加入')
  check('房间满 4 人', await waited(() => host.room?.players?.length === 4, 4000))

  // 8. 准备约束: 未全员准备不得开局
  host.send('game.start')
  await waitFor(() => host.errors.length > 0 || host.view !== null, 4000, '未准备开局结果')
  const notReadyErr = host.errors
  check('未全员准备时开局被拒', host.view === null && notReadyErr.length > 0, JSON.stringify(notReadyErr))

  for (const c of [host, p2, p3, p4]) c.send('seat.ready', { ready: true })
  check('全员准备状态同步', await waited(() => host.room?.players?.every((p) => p.ready), 5000))

  // 9. 开局
  host.send('game.start')
  const started = await waited(() => [host, p2, p3, p4].every((c) => c.view !== null), 8000)
  check('开局后各端收到私有视图', started)
  if (!started) throw new Error('开局失败, 无法继续对局验证')
  check('开局后规则锁定', await waited(() => host.room?.rulesLocked === true, 4000))

  const hands = [host, p2, p3, p4].map((c) => c.view.hand?.length)
  check('四端手牌均为 13~14 张', hands.every((n) => n === 13 || n === 14), JSON.stringify(hands))
  const priv = host.view
  check(
    '视图不泄漏他人手牌(只有张数)',
    Array.isArray(priv.othersHandCounts) && priv.othersHandCounts.length === 4 && !('othersHands' in priv),
  )
  const seats = [host, p2, p3, p4].map((c) => c.view.seat)
  check('四端座位互不相同', new Set(seats).size === 4, JSON.stringify(seats))

  // 10. 打到终局(中途断线重连一名玩家)
  const result = await playMatch([host, p2, p3, p4], { reconnectAt: 40, timeoutMs: 150000 })
  check('对局在限定时间内结束', !result.timedOut, `已执行 ${result.acts} 个动作`)
  const finalClients = result.clients
  const finished = finalClients.every((c) => c.view?.phase === 'finished' || c.view?.result)
  check('四端都收到终局状态', finished)

  if (result.reconnected) {
    const back = finalClients.find((c) => c.reconnectedSeat !== undefined)
    check('断线重连后恢复原座位', back?.reconnectSeatMatch === true,
      back ? `原座位 ${back.reconnectedSeat}, 重连后 ${back.view?.seat}` : '未找到重连客户端')
    check('断线重连后手牌与断线前一致', back?.reconnectHandMatch === true,
      back ? `断线前 ${back.reconnectedHand}, 重连后 ${JSON.stringify(back.view?.hand ?? null)}` : '未找到重连客户端')
  } else {
    fail('断线重连用例未触发')
  }

  if (finished) {
    const sums = finalClients.map((c) => (c.view.result?.scores || []).reduce((a, b) => a + b, 0))
    check('各端结算分数守恒为 0', sums.every((s) => s === 0), JSON.stringify(sums))
    const winners = finalClients.map((c) => c.view.result?.winner)
    check('各端结算赢家一致', new Set(winners).size === 1, JSON.stringify(winners))
  }

  // 11. seq 单调
  const seqBad = finalClients.filter((c) => c.errors.some((e) => e.code === 'seq_not_monotonic'))
  check('事件序列号在每端严格递增', seqBad.length === 0, seqBad.map((c) => c.label).join(','))

  // 12. 解散房间
  host.send('room.dissolve')
  const gone = await waited(() => finalClients.every((c) => c.sawRoomGone || c === host), 6000)
  check('解散后其他客户端收到空房间状态', gone)

  for (const c of finalClients) c.close()
}

/** 轮询式断言辅助: 返回布尔而不是抛错 */
async function waited(fn, timeoutMs = 3000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      if (fn()) return true
    } catch {
      /* 忽略 */
    }
    await sleep(40)
  }
  return false
}

try {
  await main()
} catch (err) {
  fail('主流程异常中断', err instanceof Error ? err.stack || err.message : String(err))
} finally {
  console.log('\n=== 阶段5 在线联机 E2E ===')
  for (const line of notes) console.log(line)
  const total = pass + failures.length
  console.log(`\n通过 ${pass}/${total}`)
  if (failures.length) {
    console.log('\n失败项:')
    for (const f of failures) console.log(` - ${f}`)
    process.exitCode = 1
  } else {
    console.log('全部通过 ✓')
    process.exitCode = 0
  }
  await sleep(100)
  process.exit(process.exitCode ?? 0)
}
