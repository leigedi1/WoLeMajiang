/**
 * 端到端完整对局验证: 4 客户端开局, 随机策略推进到终局。
 * 策略只使用视图中的合法动作(actions 数组), 并按手牌实际内容选择搭配。
 */
const BASE = 'http://127.0.0.1:8080'

function log(...args) { console.log('[e2e-match]', ...args) }
function fail(msg) { console.error('[e2e-match][FAIL]', String(msg)); process.exitCode = 1 }

async function createGuest(nickname) {
  const resp = await fetch(`${BASE}/api/v1/guests`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ nickname }),
  })
  return resp.json()
}

function openSession(guestId, token) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket('ws://127.0.0.1:8080/ws')
    const inbox = []
    const waiters = []
    const session = {
      ws, inbox,
      waitFor(pred, timeoutMs = 8000) {
        const idx = inbox.findIndex(pred)
        if (idx >= 0) return Promise.resolve(inbox.splice(idx, 1)[0])
        return new Promise((res, rej) => {
          const timer = setTimeout(() => rej(new Error('等待事件超时')), timeoutMs)
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
    ws.onopen = () => ws.send(JSON.stringify({ v: 1, type: 'guest.hello', ref: 'h', payload: { guestId, token } }))
    session.waitFor((e) => e.type === 'hello').then(
      () => resolve(session),
      (err) => reject(err),
    )
  })
}

function send(ws, type, payload, ref) {
  ws.send(JSON.stringify({ v: 1, type, ref, payload }))
}

async function main() {
  const names = ['玩家甲', '玩家乙', '玩家丙', '玩家丁']
  const guests = []
  for (const n of names) guests.push(await createGuest(n))
  const sessions = []
  for (const g of guests) sessions.push(await openSession(g.guestId, g.token))
  log('4 客户端握手完成')

  send(sessions[0].ws, 'room.create', {}, 'c')
  const created = await sessions[0].waitFor((e) => e.type === 'room.state' && e.ref === 'c')
  const code = created.payload.code
  for (let i = 1; i < 4; i++) send(sessions[i].ws, 'room.join', { code }, 'j' + i)
  for (const s of sessions.slice(1)) {
    await s.waitFor((e) => e.type === 'room.state' && typeof e.ref === 'string' && e.ref.startsWith('j'))
  }
  log('房间就绪, 邀请码:', code)

  send(sessions[0].ws, 'game.start', {}, 's')
  const startEvents = []
  for (const s of sessions) {
    const e = await s.waitFor((e) => e.type === 'game.event' && e.payload.kind === 'match.started')
    startEvents.push(e.payload)
  }
  log('开局完成')
  const views = startEvents.map((p) => p.view || {})
  // 座位映射: 服务器按 join 处理顺序分配座位, 与本脚本 sessions 索引无关。
  // seatOfSession[i] = 第 i 个客户端的真实座位; sessionOfSeat[s] = 持有座位 s 的客户端索引。
  const seatOfSession = views.map((v) => v.seat)
  const sessionOfSeat = []
  seatOfSession.forEach((seat, idx) => { sessionOfSeat[seat] = idx })
  const hands = views.map((v) => v.hand || [])
  // 庄家(座位0)持有者由服务器分配, 经映射取真实客户端的手牌。
  const handLenOfSeat = []
  seatOfSession.forEach((seat, idx) => { handLenOfSeat[seat] = hands[idx].length })
  if (handLenOfSeat[0] !== 14) fail('庄家(座位0)应 14 张: ' + handLenOfSeat[0])
  for (let seat = 1; seat < 4; seat++) {
    if (handLenOfSeat[seat] !== 13) fail(`座位 ${seat} 应 13 张: ${handLenOfSeat[seat]}`)
  }
  // othersHandCounts 按座位索引; hands 按客户端索引 —— 经座位映射对齐后比较。
  for (let idx = 0; idx < 4; idx++) {
    const v = views[idx]
    for (let j = 0; j < 4; j++) {
      if (v.othersHandCounts[j] !== handLenOfSeat[j]) {
        fail(`视图 ${idx}(座位${seatOfSession[idx]}) 中座位 ${j} 手牌数不符`)
      }
    }
  }
  log('隐私校验通过: 庄家 14 张, 其他 13 张, 他人仅数量')

  let finished = false
  let steps = 0
  // lastViews/cooldown 一律按"座位"索引(事件内嵌 view.seat 即归属座位)。
  const lastViews = []
  seatOfSession.forEach((seat, idx) => { lastViews[seat] = views[idx] })
  let errorCount = 0
  let lastErrors = []
  // 动作冷却: 玩家发出动作后, 只有当"自己的视图被刷新"(收到嵌入
  // view.seat===自己座位 的事件)才允许再次行动 —— 杜绝过期视图重复提交。
  const cooldown = [false, false, false, false]
  sessions.forEach((s) => {
    s.ws.addEventListener('message', (ev) => {
      const env = JSON.parse(String(ev.data))
      if (env.type === 'game.event') {
        const v = env.payload.view || null
        if (v) {
          lastViews[v.seat] = v
          cooldown[v.seat] = false
        }
        if (env.payload.kind === 'win' || env.payload.kind === 'draw_wall') finished = true
      } else if (env.type === 'error') {
        errorCount++
        lastErrors.push(env.payload && env.payload.code)
        if (lastErrors.length > 5) lastErrors.shift()
      }
    })
  })

  let stepId = 0
  const ACT = (gi, action, payload) =>
    send(sessions[gi].ws, 'game.action', { action, payload }, 'a' + (stepId++))

  // 手牌计数工具。
  const countTile = (hand, t) => hand.filter((h) => h === t).length

  // 决策: 基于视图 v(玩家 i 的私有视图)产出一条动作。
  function decide(i, v) {
    const actions = v.actions || []
    if (actions.length === 0) return null
    const isDiscardTurn = v.turnStage === 'discard' && v.turnSeat === i
    const isDrawTurn = v.turnStage === 'draw' && v.turnSeat === i
    const isRespond = v.turnStage === 'respond' && v.turnSeat !== i
    if (isDrawTurn && actions.includes('draw')) return { action: 'draw', payload: {} }
    if (isDiscardTurn) {
      if (actions.includes('hu.self')) return { action: 'hu.self', payload: {} }
      if (actions.includes('kong.concealed')) {
        const counts = {}
        v.hand.forEach((h) => (counts[h] = (counts[h] || 0) + 1))
        const four = Object.entries(counts).find(([, n]) => n >= 4)
        if (four) return { action: 'kong.concealed', payload: { tile: four[0] } }
      }
      if (actions.includes('kong.added')) {
        // 找已碰且有第 4 张的: melds 里 pong 的 tiles[0] 在手牌中。
        for (const m of v.melds || []) {
          if (m.kind === 'pong' && countTile(v.hand, m.tiles[0]) >= 1) {
            return { action: 'kong.added', payload: { tile: m.tiles[0] } }
          }
        }
      }
      if (actions.includes('discard')) return { action: 'discard', payload: { tile: v.hand[0] } }
      return null
    }
    if (isRespond && actions.includes('pass')) {
      if (actions.includes('hu.robbing')) return { action: 'hu.robbing', payload: {} }
      if (actions.includes('kong.exposed') && countTile(v.hand, v.pendingTile) >= 3) {
        return { action: 'kong.exposed', payload: {} }
      }
      if (actions.includes('pong') && countTile(v.hand, v.pendingTile) >= 2) {
        return { action: 'pong', payload: {} }
      }
      if (actions.includes('chow')) {
        const pending = v.pendingTile
        const suit = pending.slice(0, -1)
        const rank = parseInt(pending.slice(-1), 10)
        if (!isNaN(rank)) {
          for (const [a, b] of [[-2, -1], [-1, 1], [1, 2]]) {
            const ra = rank + a, rb = rank + b
            if (ra >= 1 && ra <= 9 && rb >= 1 && rb <= 9) {
              const ta = suit + ra, tb = suit + rb
              if (countTile(v.hand, ta) >= 1 && countTile(v.hand, tb) >= 1) {
                // 吃的搭子里不含 pending 本身。
                if (ta !== pending && tb !== pending) {
                  return { action: 'chow', payload: { with: [ta, tb] } }
                }
              }
            }
          }
        }
      }
      return { action: 'pass', payload: {} }
    }
    return null
  }

  let noProgress = 0
  while (!finished && steps < 4000) {
    steps++
    let acted = false
    // 按"座位"遍历, 经 sessionOfSeat 路由到真实客户端连接。
    for (let seat = 0; seat < 4; seat++) {
      if (cooldown[seat]) continue
      const d = decide(seat, lastViews[seat])
      if (d) {
        ACT(sessionOfSeat[seat], d.action, d.payload)
        cooldown[seat] = true
        acted = true
        break
      }
    }
    if (!acted) {
      noProgress++
      if (noProgress > 300) {
        fail('停滞: ' + lastViews.map((v) => v && `${v.phase}/${v.turnStage}/${v.turnSeat}`).join(' | '))
        break
      }
      // 全员冷却中: 等事件解除。
      await new Promise((r) => setTimeout(r, 50))
      continue
    } else {
      noProgress = 0
    }
    await new Promise((r) => setTimeout(r, 60))
  }
  log('对局终局:', finished ? '是' : '否', '步数:', steps, '错误数:', errorCount, lastErrors.join(','))
  if (!finished) fail('对局未终局')

  await new Promise((r) => setTimeout(r, 400))
  const anyFinished = lastViews.some((v) => v && v.phase === 'finished')
  if (!anyFinished) fail('未观察到终局 phase')
  const winView = lastViews.find((v) => v && v.phase === 'finished')
  if (winView && winView.result) {
    log('结算: winner=', winView.result.winner, 'byDraw=', winView.result.byDraw, 'fan=', winView.result.fan, 'scores=', JSON.stringify(winView.result.scores))
  }

  for (const s of sessions) {
    try { send(s.ws, 'room.leave', {}, 'leave') } catch {}
  }
  await new Promise((r) => setTimeout(r, 200))
  for (const s of sessions) { try { s.ws.close() } catch {} }
  log('完成' + (process.exitCode ? '(有失败)' : ' ✓'))
}

main().catch((err) => fail(err.message ?? String(err)))
