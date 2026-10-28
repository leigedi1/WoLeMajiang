// 璐熻浇娴嬮噺: 寮€ N 涓埧闂?姣忔埧 1 鐪熶汉 + 3 鏈哄櫒浜?, 寮€灞€鍚庣粺璁℃湇鍔＄鍐呭瓨/CPU 澧為噺銆?// 鐢ㄦ硶: node scripts/measure-load.mjs [鎴块棿鏁癩 [绔彛]
const port = Number(process.argv[3] || 8790)
const rooms = Number(process.argv[2] || 5)
const base = `http://127.0.0.1:${port}`

function openSession(guestId, token) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws`)
    const pending = new Map()
    let seq = 0
    ws.onopen = () => {
      ws.send(JSON.stringify({ v: 1, type: 'guest.hello', ref: 'h', payload: { guestId, token } }))
    }
    ws.onerror = (e) => reject(new Error('ws error ' + e.message))
    ws.onmessage = (ev) => {
      const env = JSON.parse(ev.data)
      if (env.type === 'hello') return resolve(session)
      if (env.ref && pending.has(env.ref)) {
        const { res, rej } = pending.get(env.ref)
        pending.delete(env.ref)
        if (env.type === 'error') rej(new Error(env.payload?.message || 'error'))
        else res(env)
      } else if (env.type === 'game.event' || env.type === 'room.state') {
        session.events.push(env.type)
      }
    }
    const session = {
      events: [],
      send(type, payload) {
        const ref = 'r' + ++seq
        return new Promise((res, rej) => {
          pending.set(ref, { res, rej })
          ws.send(JSON.stringify({ v: 1, type, ref, payload }))
          setTimeout(() => {
            if (pending.has(ref)) {
              pending.delete(ref)
              rej(new Error('timeout ' + type))
            }
          }, 8000)
        })
      },
      close() {
        try { ws.close() } catch {}
      },
    }
  })
}

async function newGuest(name) {
  const r = await fetch(`${base}/api/v1/guests`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ nickname: name }),
  })
  const j = await r.json()
  return { guestId: j.guestId, token: j.token }
}

const created = []
for (let i = 0; i < rooms; i++) {
  const g = await newGuest(`璐熻浇${i}`)
  const s = await openSession(g.guestId, g.token)
  const env = await s.send('room.create', { name: `璐熻浇鎴?{i}`, password: '', public: false, players: 4, presetId: 'jinan' })
  const code = env.payload?.room?.code || env.payload?.code
  for (const seat of [1, 2, 3]) { s.send('seat.bot', { seat, add: true }).catch(() => {}); await new Promise((r) => setTimeout(r, 120)) }
  await new Promise((r) => setTimeout(r, 200)); s.send('game.start', {}).catch(() => {})
  created.push({ code, s })
}
// 璁╂満鍣ㄤ汉璺戜竴浼氬効, 浜х敓瀵瑰眬鐘舵€?await new Promise((r) => setTimeout(r, 3000))
const health = await (await fetch(`${base}/healthz`)).text()
console.log('rooms=', created.length, 'codes=', created.map((c) => c.code).join(','))
console.log('healthz=', health)
console.log('events seen=', created.map((c) => c.s.events.length).join('/'))
// 淇濇寔杩炴帴 8 绉掑悗閫€鍑?杩涚▼鍐呭唴瀛樼敱澶栭儴娴嬮噺)
await new Promise((r) => setTimeout(r, 8000))
for (const c of created) c.s.close()
console.log('done')


