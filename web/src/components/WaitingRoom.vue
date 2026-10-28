<script setup lang="ts">
/**
 * 等待房间: 房间码 / 邀请链接 / 在座玩家 / 满员后房主开始。
 * 不做大厅、不做准备状态 —— 人数到齐即可开。
 */
import { computed, ref } from 'vue'
import type { RoomStateView } from '../protocol/types'

const props = defineProps<{
  room: RoomStateView
  myGuestId: string
  isHost: boolean
  transportLabel: string
  presetName?: string
  poolTotal?: number
  errorText?: string
  /** 房间里还有未结束的对局: 显示「回到牌桌」 */
  canResume?: boolean
  /** 我是不是 P2P/局域网房主(这类房间必须保持页面在前台) */
  p2pHost?: boolean
  /** P2P 已连上的 peer 数(含房主自己) */
  peerCount?: number
}>()

const emit = defineEmits<{
  (e: 'start'): void
  (e: 'leave'): void
  (e: 'resume'): void
  (e: 'bot', seat: number, add: boolean): void
}>()

const copied = ref('')

const capacity = computed(() => props.room.capacity ?? 4)
const players = computed(() => props.room.players ?? [])
const seats = computed(() => {
  const out: Array<{ seat: number; name: string; empty: boolean; isMe: boolean; isHost: boolean; isBot: boolean }> = []
  for (let i = 0; i < capacity.value; i++) {
    const p = players.value.find((x) => x.seat === i)
    out.push({
      seat: i,
      name: p?.nickname ?? '',
      empty: !p,
      isMe: !!p && p.guestId === props.myGuestId,
      isHost: !!p?.isHost || (i === 0 && !players.value.some((x) => x.isHost)),
      isBot: !!p?.isBot,
    })
  }
  return out
})

const full = computed(() => players.value.length >= capacity.value)
const canStart = computed(() => props.isHost && full.value && !playing.value)
/** 对局进行中: 此时不该再「开始游戏」, 而是「回到牌桌」 */
const playing = computed(() => props.room.status === 'playing')
const canResume = computed(() => !!props.canResume)
/** 刚从牌桌回来: 上一局已结算, 这里就是「续玩」入口 */
const justFinished = computed(() => props.room.status === 'finished')

const inviteUrl = computed(() => {
  if (!props.room.code) return ''
  const base = `${location.origin}${location.pathname}`
  return `${base}?join=${encodeURIComponent(props.room.code)}`
})

// ---- 规则摘要(阶段10: 计时/截牌/和牌条件) ----
const rules = computed(() => props.room.rules)

function secLabel(v: number | undefined): string {
  if (!v) return '关闭'
  return `${v}s`
}

/** 一句式摘要, 房间页给玩家确认本局细则 */
const rulesSummary = computed(() => {
  const r = rules.value
  if (!r) return ''
  const parts = [
    `碰杠 ${secLabel(r.respond_meld_sec)}`,
    `胡 ${secLabel(r.respond_win_sec)}`,
    `出牌 ${secLabel(r.discard_sec)}`,
  ]
  const flags: string[] = []
  flags.push(r.rob_win ? '截胡' : '不截胡')
  flags.push(r.rob_meld ? '截碰' : '先按者得')
  if (!r.chow) flags.push('不吃')
  if (r.seven_pairs) flags.push('七对')
  if (r.rob_kong_win) flags.push('抢杠胡')
  if (r.kong_flower) flags.push('杠上开花')
  if (r.last_tile) flags.push('海底捞月')
  flags.push(`底分 ${r.base_score}`)
  return `${parts.join(' · ')}｜${flags.join(' · ')}`
})

async function copy(text: string, tag: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = tag
    setTimeout(() => { if (copied.value === tag) copied.value = '' }, 1600)
  } catch {
    copied.value = ''
  }
}
</script>

<template>
  <div class="mx-auto w-full max-w-2xl px-4 py-8">
    <div class="rounded-2xl border border-stone-200 bg-white p-6 shadow-sm">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-stone-800">{{ room.name || '房间' }}</h1>
          <p class="mt-1 text-sm text-stone-500">
            {{ presetName || '自定义玩法' }}
            <span v-if="poolTotal"> · {{ poolTotal }} 张牌</span>
            · {{ transportLabel }}
          </p>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <button
            v-if="canResume"
            class="rounded-xl bg-emerald-600 px-3 py-2 text-sm font-medium text-white hover:bg-emerald-700"
            @click="emit('resume')"
          >
            回到牌桌
          </button>
          <button class="rounded-xl border border-stone-200 px-3 py-2 text-sm text-stone-600 hover:bg-stone-50" @click="emit('leave')">
            离开房间
          </button>
        </div>
      </div>

      <!-- P2P 房主提示: 这类房间跑在浏览器里, 页面一被系统挂起别人就进不来了 -->
      <div v-if="p2pHost" class="mt-4 rounded-xl border border-sky-200 bg-sky-50 px-3 py-2 text-xs leading-relaxed text-sky-800">
        你是 P2P 房主：对局跑在这台设备的浏览器里。
        <strong>请保持本页面在前台</strong>（已自动申请屏幕常亮），不要锁屏或切到其他 App，否则朋友会连不上。
        <span v-if="peerCount" class="ml-1">当前信令在线 {{ peerCount }} 人。</span>
      </div>

      <!-- 规则摘要: 让玩家开局前知道本局的计时与截牌规则 -->
      <div v-if="rulesSummary" class="mt-4 rounded-xl border border-stone-200 bg-stone-50 px-3 py-2 text-[11px] leading-relaxed text-stone-600">
        <span class="font-medium text-stone-700">本局规则</span>
        <span class="ml-1">{{ rulesSummary }}</span>
        <span class="mt-0.5 block text-stone-400">优先级：胡 &gt; 碰/杠 &gt; 吃</span>
      </div>

      <!-- 房间码 -->
      <div class="mt-5 rounded-2xl bg-emerald-50 p-4">
        <p class="text-xs text-emerald-700">房间码（发给朋友，或在另一台设备输入）</p>
        <div class="mt-1 flex items-center gap-3">
          <span class="font-mono text-3xl font-bold tracking-[0.25em] text-emerald-800">{{ room.code || '——' }}</span>
          <button class="rounded-lg bg-white px-2.5 py-1 text-xs text-emerald-700 shadow-sm hover:bg-emerald-100" @click="copy(room.code, 'code')">
            {{ copied === 'code' ? '已复制' : '复制' }}
          </button>
        </div>
        <div class="mt-3 flex flex-wrap items-center gap-2 text-xs text-emerald-700">
          <span class="text-emerald-600/80">邀请链接：</span>
          <code class="max-w-full truncate rounded bg-white/70 px-2 py-0.5">{{ inviteUrl }}</code>
          <button class="rounded-lg bg-white px-2.5 py-1 text-emerald-700 shadow-sm hover:bg-emerald-100" @click="copy(inviteUrl, 'url')">
            {{ copied === 'url' ? '已复制' : '复制链接' }}
          </button>
        </div>
      </div>

      <!-- 人数 -->
      <div class="mt-5">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-stone-700">在座玩家</h2>
          <span class="text-sm" :class="full ? 'text-emerald-600' : 'text-stone-500'">
            {{ players.length }} / {{ capacity }} {{ full ? '已到齐' : '等待加入…' }}
          </span>
        </div>
        <ul class="mt-2 grid gap-2 sm:grid-cols-2">
          <li
            v-for="s in seats"
            :key="s.seat"
            class="flex items-center gap-2 rounded-xl border px-3 py-2.5 text-sm"
            :class="s.empty ? 'border-dashed border-stone-300 text-stone-400' : 'border-stone-200 text-stone-700'"
          >
            <span class="flex h-6 w-6 items-center justify-center rounded-full text-xs"
              :class="s.empty ? 'bg-stone-100 text-stone-400' : 'bg-emerald-100 text-emerald-700'">{{ s.seat + 1 }}</span>
            <span class="min-w-0 flex-1 truncate">{{ s.empty ? '等待加入' : s.name }}</span>
            <span v-if="s.isBot" class="rounded bg-stone-100 px-1.5 py-0.5 text-[11px] text-stone-500">电脑</span>
            <span v-else-if="s.isHost" class="rounded bg-amber-100 px-1.5 py-0.5 text-[11px] text-amber-700">房主</span>
            <span v-if="s.isMe" class="rounded bg-sky-100 px-1.5 py-0.5 text-[11px] text-sky-700">我</span>
            <button
              v-if="isHost && s.isBot"
              type="button"
              class="rounded border border-stone-200 px-1.5 py-0.5 text-[11px] text-stone-500 hover:border-rose-300 hover:text-rose-600"
              @click="emit('bot', s.seat, false)"
            >
              移除
            </button>
            <button
              v-else-if="isHost && s.empty"
              type="button"
              class="rounded border border-dashed border-stone-300 px-1.5 py-0.5 text-[11px] text-stone-500 hover:border-emerald-400 hover:text-emerald-600"
              @click="emit('bot', s.seat, true)"
            >
              + 电脑
            </button>
          </li>
        </ul>
      </div>

      <p v-if="props.errorText" class="mt-4 rounded-xl bg-red-50 px-3 py-2 text-sm text-red-600">{{ props.errorText }}</p>

      <div v-if="justFinished" class="mt-5 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
        上一局已结束。{{ isHost ? '人齐后可直接开始下一局。' : '等房主开始下一局，或点右上角离开房间。' }}
      </div>

      <div class="mt-6">
        <button
          v-if="playing && canResume"
          class="w-full rounded-xl bg-emerald-600 px-4 py-3 text-base font-medium text-white transition hover:bg-emerald-700"
          @click="emit('resume')"
        >
          🀄 回到牌桌继续对局
        </button>
        <p v-else-if="playing" class="rounded-xl bg-stone-50 px-4 py-3 text-center text-sm text-stone-500">
          对局进行中……
        </p>
        <button
          v-else-if="isHost"
          class="w-full rounded-xl px-4 py-3 text-base font-medium text-white transition disabled:opacity-50"
          :class="canStart ? 'bg-emerald-600 hover:bg-emerald-700' : 'bg-stone-300'"
          :disabled="!canStart"
          @click="emit('start')"
        >
          {{ full ? (justFinished ? '开始下一局' : '开始游戏') : `等待玩家到齐（${players.length}/${capacity}）` }}
        </button>
        <p v-else class="rounded-xl bg-stone-50 px-4 py-3 text-center text-sm text-stone-500">
          {{ full ? (justFinished ? '等待房主开始下一局…' : '人已到齐，等待房主开始游戏…') : '等待其他玩家加入…' }}
        </p>
      </div>
    </div>
  </div>
</template>
