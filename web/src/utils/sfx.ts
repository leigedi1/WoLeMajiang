/**
 * 极简音效引擎: 全部用 WebAudio 现场合成, 不加载任何音频文件。
 *
 * 为什么不用 mp3: 打包体积、移动端首帧解码、以及"一份资源要在 P2P/离线模式下
 * 也能用"。合成音只需要几十行代码, 且延迟为 0。
 *
 * 音色取向(用户反馈"下牌太突兀了"): 全部用短促、低音量、带低通的木质/瓷质撞击感,
 * 不用刺耳的高频, 也不用长混响, 避免一局里几十次下牌把耳朵磨坏。
 */

type Tone = {
  /** 频率(Hz) */
  freq: number
  /** 相对音量 0..1 */
  gain?: number
  /** 持续秒数 */
  dur?: number
  /** 波形 */
  type?: OscillatorType
  /** 起始延迟(秒) */
  at?: number
}

let ctx: AudioContext | null = null
let master: GainNode | null = null
let enabled = true
/** 上一声音的时间戳, 用于节流(同一毫秒内多次事件只响一次) */
let lastAt = 0

function ensureContext(): AudioContext | null {
  if (typeof window === 'undefined') return null
  if (ctx) return ctx
  const Ctor =
    window.AudioContext ??
    (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (!Ctor) return null
  try {
    ctx = new Ctor()
    master = ctx.createGain()
    master.gain.value = 0.5
    master.connect(ctx.destination)
    return ctx
  } catch {
    ctx = null
    return null
  }
}

/** 浏览器要求音频上下文在用户手势后才能启动: 任意一次点击/按键时调用一次即可 */
export function unlockAudio(): void {
  const c = ensureContext()
  if (!c) return
  if (c.state === 'suspended') void c.resume()
}

export function setAudioEnabled(next: boolean): void {
  enabled = next
  if (!next) return
  // 重新打开时确保上下文可共振
  unlockAudio()
}

export function isAudioEnabled(): boolean {
  return enabled
}

/** 播放一组音符(单音就是组里只有一个) */
function play(tones: Tone[], volume = 1): void {
  if (!enabled) return
  const c = ensureContext()
  if (!c || !master) return
  if (c.state === 'suspended') {
    void c.resume()
    return
  }
  const now = c.currentTime
  for (const t of tones) {
    const start = now + (t.at ?? 0)
    const dur = t.dur ?? 0.12
    const osc = c.createOscillator()
    const gain = c.createGain()
    osc.type = t.type ?? 'sine'
    osc.frequency.setValueAtTime(t.freq, start)
    // 快速的 attack + 指数衰减: 听感像一次轻敲, 而不是一段音
    const peak = Math.max(0.0001, (t.gain ?? 0.3) * volume)
    gain.gain.setValueAtTime(0.0001, start)
    gain.gain.exponentialRampToValueAtTime(peak, start + 0.008)
    gain.gain.exponentialRampToValueAtTime(0.0001, start + dur)
    osc.connect(gain)
    gain.connect(master)
    osc.start(start)
    osc.stop(start + dur + 0.02)
  }
}

/**
 * 噪声脉冲: "牌落到桌面上"的那一下其实是短噪声, 纯正弦听起来像电子音。
 * 这里用 60ms 的白噪声 + 低通, 得到木/瓷质地的"哒"。
 */
function noiseHit(dur = 0.06, cutoff = 1800, volume = 0.35): void {
  if (!enabled) return
  const c = ensureContext()
  if (!c || !master) return
  if (c.state === 'suspended') {
    void c.resume()
    return
  }
  const frames = Math.max(1, Math.floor(c.sampleRate * dur))
  const buffer = c.createBuffer(1, frames, c.sampleRate)
  const data = buffer.getChannelData(0)
  for (let i = 0; i < frames; i++) {
    // 越往后越轻: 让噪声本身就有衰减包络
    const decay = 1 - i / frames
    data[i] = (Math.random() * 2 - 1) * decay * decay
  }
  const src = c.createBufferSource()
  src.buffer = buffer
  const filter = c.createBiquadFilter()
  filter.type = 'lowpass'
  filter.frequency.value = cutoff
  const gain = c.createGain()
  gain.gain.value = volume
  src.connect(filter)
  filter.connect(gain)
  gain.connect(master)
  src.start()
}

/** 节流: 一帧内多个事件不叠加成噪音 */
function throttle(ms = 40): boolean {
  const now = Date.now()
  if (now - lastAt < ms) return false
  lastAt = now
  return true
}

/** 出牌: 一声柔和的木牌轻敲 */
export function sfxDiscard(): void {
  if (!throttle(30)) return
  noiseHit(0.055, 2200, 0.5)
  play([{ freq: 320, gain: 0.12, dur: 0.07, type: 'triangle' }])
}

/** 摸牌: 比出牌更轻的一点纸牌摩擦感 */
export function sfxDraw(): void {
  if (!throttle(30)) return
  noiseHit(0.035, 3200, 0.22)
}

/** 吃/碰: 两声上行短音, 明确但柔和 */
export function sfxMeld(): void {
  play([
    { freq: 523.25, gain: 0.16, dur: 0.1, type: 'triangle' },
    { freq: 783.99, gain: 0.14, dur: 0.12, type: 'triangle', at: 0.06 },
  ])
}

/** 杠: 比碰更厚重(多一个低音) */
export function sfxKong(): void {
  noiseHit(0.08, 1200, 0.35)
  play([
    { freq: 261.63, gain: 0.18, dur: 0.16, type: 'triangle' },
    { freq: 392.0, gain: 0.15, dur: 0.18, type: 'triangle', at: 0.05 },
  ])
}

/** 和牌: 三音上行大琶音 */
export function sfxWin(): void {
  play([
    { freq: 523.25, gain: 0.2, dur: 0.18, type: 'sine' },
    { freq: 659.25, gain: 0.18, dur: 0.18, type: 'sine', at: 0.09 },
    { freq: 783.99, gain: 0.18, dur: 0.22, type: 'sine', at: 0.18 },
    { freq: 1046.5, gain: 0.14, dur: 0.3, type: 'sine', at: 0.28 },
  ])
}

/** 流局: 两声下行, 收束感 */
export function sfxDrawWall(): void {
  play([
    { freq: 392.0, gain: 0.16, dur: 0.2, type: 'sine' },
    { freq: 293.66, gain: 0.16, dur: 0.3, type: 'sine', at: 0.14 },
  ])
}

/** 全员过: 一声很轻的下行提示 */
export function sfxPass(): void {
  if (!throttle(80)) return
  play([{ freq: 349.23, gain: 0.1, dur: 0.1, type: 'sine' }])
}

/** 轮到你: 柔和的双铃, 比默认提示音低一个八度更不刺耳 */
export function sfxYourTurn(): void {
  play([
    { freq: 783.99, gain: 0.14, dur: 0.14, type: 'sine' },
    { freq: 1046.5, gain: 0.12, dur: 0.18, type: 'sine', at: 0.1 },
  ])
}

/** 按钮/UI: 一次极轻的点击 */
export function sfxTap(): void {
  if (!throttle(60)) return
  noiseHit(0.02, 4000, 0.12)
}
