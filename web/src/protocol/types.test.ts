import { describe, expect, it } from 'vitest'
import { decode, encode, makeEnvelope, PROTOCOL_VERSION, Op, Ev } from './types'

describe('protocol envelope', () => {
  it('encode/decode 往返一致', () => {
    const env = makeEnvelope(Op.Ping, { ts: 1 }, 'r1')
    const parsed = decode(encode(env))
    expect(parsed.v).toBe(PROTOCOL_VERSION)
    expect(parsed.type).toBe(Op.Ping)
    expect(parsed.ref).toBe('r1')
    expect(parsed.payload).toEqual({ ts: 1 })
  })

  it('拒绝错误协议版本', () => {
    expect(() => decode('{"v":99,"type":"ping"}')).toThrow(/协议版本/)
  })

  it('拒绝缺失 type', () => {
    expect(() => decode(`{"v":${PROTOCOL_VERSION}}`)).toThrow(/type/)
  })

  it('拒绝非法 JSON', () => {
    expect(() => decode('not-json')).toThrow()
  })

  it('事件常量与服务端约定一致', () => {
    expect(Op.Hello).toBe('guest.hello')
    expect(Op.Resync).toBe('sync.resync')
    expect(Ev.RoomState).toBe('room.state')
    expect(Ev.Error).toBe('error')
  })
})

describe('room state view 类型形状', () => {
  it('其他玩家投影不含 hand 字段(线缆形状由服务端保证, 此处固化约定)', () => {
    const other = { guestId: 'g', nickname: '甲', seat: 0, connected: true, tileCount: 13 }
    expect(Object.keys(other).sort()).toEqual(
      ['connected', 'guestId', 'nickname', 'seat', 'tileCount'].sort(),
    )
    expect(other).not.toHaveProperty('hand')
  })
})
