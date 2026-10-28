/**
 * 端到端 WS 协议验证脚本(Node 22+ 全局 WebSocket/fetch)。
 * 验证: 握手认证 -> 创建房间 -> 第二客户端按邀请码加入 -> 广播 -> 限流 -> 重连同步。
 */
const BASE = 'http://127.0.0.1:8080'

function log(...args) {
  console.log('[e2e]', ...args)
}

function fail(msg) {
  console.error('[e2e][FAIL]', msg)
  process.exitCode = 1
}

async function createGuest(nickname) {
  const resp = await fetch(`${BASE}/api/v1/guests`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ nickname }),
  })
  if (resp.status !== 201) throw new Error(`createGuest status ${resp.status}`)
  return resp.json()
}

/** 打开一条 WS 并完成 guest.hello 握手 */
function openSession(guestId, token) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket('ws://127.0.0.1:8080/ws')
    const inbox = []
    const waiters = []
    const session = {
      ws,
      inbox,
      /** 等待下一条满足条件的事件 */
      waitFor(pred, timeoutMs = 5000) {
        const idx = inbox.findIndex(pred)
        if (idx >= 0) return Promise.resolve(inbox.splice(idx, 1)[0])
        return new Promise((res, rej) => {
          const timer = setTimeout(() => {
            const i = waiters.findIndex((w) => w.pred === pred)
            if (i >= 0) waiters.splice(i, 1)
            rej(new Error('等待事件超时'))
          }, timeoutMs)
          waiters.push({ pred, res: (env) => { clearTimeout(timer); res(env) } })
        })
      },
    }
    ws.onmessage = (ev) => {
      const env = JSON.parse(String(ev.data))
      const i = waiters.findIndex((w) => w.pred(env))
      if (i >= 0) waiters.splice(i, 1)[0].res(env)
      else inbox.push(env)
    }
    ws.onerror = () => reject(new Error('WS 连接失败'))
    ws.onopen = () => {
      ws.send(JSON.stringify({ v: 1, type: 'guest.hello', ref: 'h1', payload: { guestId, token } }))
    }
    // 握手即第一个事件。
    session.waitFor((e) => e.type === 'hello', 5000).then(
      (hello) => {
        if (hello.payload.guestId !== guestId) reject(new Error('握手身份不一致'))
        else resolve(session)
      },
      (err) => reject(err),
    )
  })
}

function send(ws, type, payload, ref) {
  ws.send(JSON.stringify({ v: 1, type, ref, payload }))
}

async function main() {
  // 0. 健康检查。
  const health = await (await fetch(`${BASE}/api/v1/health`)).json()
  log('health:', health.status, 'rooms:', health.onlineRooms, '/', health.maxOnlineRooms)
  if (health.status !== 'ok' || health.protocolVersion !== 1) fail('健康检查异常')

  // 1. 两个游客。
  const g1 = await createGuest('玩家甲')
  const g2 = await createGuest('玩家乙')
  log('游客创建成功:', g1.nickname, g2.nickname)

  // 2. 双客户端握手。
  const [s1, s2] = await Promise.all([openSession(g1.guestId, g1.token), openSession(g2.guestId, g2.token)])
  log('双 WS 握手成功')

  // 3. g1 创建房间。
  send(s1.ws, 'room.create', {}, 'c1')
  const created = await s1.waitFor((e) => e.type === 'room.state' && e.ref === 'c1')
  const code = created.payload.code
  log('房间已创建, 邀请码:', code, '玩家数:', created.payload.players.length)
  if (created.payload.players.length !== 1) fail('创建后应只有房主')
  if (created.payload.you?.seat !== 0) fail('房主应在 0 号位')

  // 4. g2 按邀请码加入(小写输入也应匹配, 由服务端/客户端统一为大写)。
  send(s2.ws, 'room.join', { code: code.toLowerCase() }, 'j1')
  const joined = await s2.waitFor((e) => e.type === 'room.state' && e.ref === 'j1')
  log('g2 已加入, 玩家数:', joined.payload.players.length)
  if (joined.payload.players.length !== 2) fail('加入后应有 2 名玩家')
  if (joined.payload.you?.seat !== 1) fail('g2 应在 1 号位')
  // g1 应收到广播(无 ref)。
  const broadcast = await s1.waitFor((e) => e.type === 'room.state' && !e.ref && e.payload.players.length === 2)
  log('g1 收到加入广播, seq:', broadcast.seq)
  if (broadcast.payload.players[1].nickname !== '玩家乙') fail('广播中应包含新玩家昵称')

  // 5. 序列号单调递增。
  if (!(broadcast.seq >= created.seq)) fail('事件序列号应单调递增')

  // 6. 断线重连增量同步: 记录当前 seq, g2 断开重连后 resync。
  const lastSeq = broadcast.seq
  s2.ws.close()
  await new Promise((r) => setTimeout(r, 300))
  // g1 收到 g2 断线广播。
  const dc = await s1.waitFor((e) => e.type === 'room.state' && e.payload.players?.[1]?.connected === false)
  log('g1 看到 g2 断线标记')
  const s2b = await openSession(g2.guestId, g2.token)
  send(s2b.ws, 'sync.resync', { lastSeq }, 'rs1')
  const resynced = await s2b.waitFor((e) => e.type === 'room.state' && e.ref === 'rs1')
  log('g2 重连同步成功, seq:', resynced.seq, '在线状态恢复:', resynced.payload.players[1].connected)
  if (!resynced.payload.players[1].connected) fail('重连后连接状态应恢复为 true')
  if (resynced.seq < lastSeq) fail('重连后 seq 不应倒退')

  // 7. 未认证连接应被拒绝。
  const badAuth = await new Promise((resolve) => {
    const ws = new WebSocket('ws://127.0.0.1:8080/ws')
    ws.onopen = () => {
      ws.send(JSON.stringify({ v: 1, type: 'guest.hello', ref: 'x', payload: { guestId: 'nope', token: 'bad' } }))
    }
    ws.onmessage = (ev) => {
      const env = JSON.parse(String(ev.data))
      if (env.type === 'error') {
        resolve(env.payload.code)
        ws.close()
      }
    }
    ws.onerror = () => resolve('ws-error')
  })
  log('未认证握手被拒绝, 错误码:', badAuth)
  if (badAuth !== 'auth_failed') fail('未认证应返回 auth_failed')

  // 8. 错误协议版本应被拒绝。
  const badVersion = await new Promise((resolve) => {
    const ws = new WebSocket('ws://127.0.0.1:8080/ws')
    ws.onopen = () => ws.send(JSON.stringify({ v: 99, type: 'ping' }))
    ws.onmessage = (ev) => {
      const env = JSON.parse(String(ev.data))
      resolve(env.type === 'error' ? env.payload.code : env.type)
      ws.close()
    }
    ws.onerror = () => resolve('ws-error')
  })
  log('错误协议版本被拒绝, 错误码:', badVersion)
  if (badVersion !== 'bad_message') fail('错误版本应返回 bad_message')

  // 9. 消息限流: 快速连发 40 条 ping(突发 30 + 10/s), 应触发 rate_limited。
  //    注意: 31 条 pong 会排队送达, 需耐心等到错误事件(放宽超时)。
  const rl = await new Promise((resolve) => {
    const ws = new WebSocket('ws://127.0.0.1:8080/ws')
    let helloed = false
    ws.onopen = () => {
      ws.send(JSON.stringify({ v: 1, type: 'guest.hello', ref: 'h', payload: { guestId: g1.guestId, token: g1.token } }))
    }
    ws.onmessage = (ev) => {
      const env = JSON.parse(String(ev.data))
      if (env.type === 'hello') {
        helloed = true
        for (let i = 0; i < 40; i++) {
          ws.send(JSON.stringify({ v: 1, type: 'ping' }))
        }
        return
      }
      if (helloed && env.type === 'error') {
        // 收到错误: 稍等再关闭, 让脚本记录错误码。
        setTimeout(() => { try { ws.close() } catch {} }, 200)
        resolve(env.payload.code)
      }
    }
    ws.onerror = () => resolve('ws-error')
    setTimeout(() => resolve('timeout'), 10000)
  })
  log('限流触发, 错误码:', rl)
  if (rl !== 'rate_limited') fail('超频应返回 rate_limited(实际: ' + rl + ')')

  // 10. 房间容量上限: 再拉两名游客填满 4 人, 第 5 人应 room_full。
  const g3 = await createGuest('玩家丙')
  const g4 = await createGuest('玩家丁')
  const g5 = await createGuest('玩家戊')
  const s3 = await openSession(g3.guestId, g3.token)
  const s4 = await openSession(g4.guestId, g4.token)
  const s5 = await openSession(g5.guestId, g5.token)
  send(s3.ws, 'room.join', { code }, 'j3')
  send(s4.ws, 'room.join', { code }, 'j4')
  await s3.waitFor((e) => e.type === 'room.state' && e.ref === 'j3')
  await s4.waitFor((e) => e.type === 'room.state' && e.ref === 'j4')
  send(s5.ws, 'room.join', { code }, 'j5')
  const fifth = await s5.waitFor((e) => e.type === 'error' && e.ref === 'j5')
  log('第 5 人被拒绝, 错误码:', fifth.payload.code)
  if (fifth.payload.code !== 'room_full') fail('第 5 人应返回 room_full')

  // 11. 未知房间邀请码。
  send(s5.ws, 'room.join', { code: 'ZZZZZZ' }, 'j6')
  const noRoom = await s5.waitFor((e) => e.type === 'error' && e.ref === 'j6')
  log('未知邀请码被拒绝, 错误码:', noRoom.payload.code)
  if (noRoom.payload.code !== 'room_not_found') fail('未知邀请码应返回 room_not_found')

  // 12. 清理: 全员离开, 房间应回收。
  send(s1.ws, 'room.leave', undefined, 'l1')
  send(s3.ws, 'room.leave', undefined, 'l3')
  send(s4.ws, 'room.leave', undefined, 'l4')
  send(s2b.ws, 'room.leave', undefined, 'l4b')
  await new Promise((r) => setTimeout(r, 500))
  const health2 = await (await fetch(`${BASE}/api/v1/health`)).json()
  log('全员离开后房间数:', health2.onlineRooms)
  if (health2.onlineRooms !== 0) fail('空房应被回收')

  for (const s of [s1, s3, s4, s5, s2b]) {
    try { s.ws.close() } catch {}
  }
  log('全部通过' + (process.exitCode ? '' : ' ✓'))
}

main().catch((err) => {
  fail(err.message ?? String(err))
})
