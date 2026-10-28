<script setup lang="ts">
/**
 * 房间大厅: 座位/准备/机器人补位/邀请/房主控制。
 * 纯展示组件: 所有动作以事件上抛, 由 App 转发给 connection store。
 */
import { computed, ref } from 'vue'
import type { InviteView, RoomStateView } from '../protocol/types'

interface Props {
  room: RoomStateView
  myGuestId: string
  canStart?: boolean
  isHost?: boolean
  invites?: InviteView[]
  /** 最近一次创建的邀请令牌(明文, 仅创建后一次) */
  lastInviteToken?: string
}

const props = withDefaults(defineProps<Props>(), {
  canStart: false,
  isHost: false,
  invites: () => [],
  lastInviteToken: '',
})

const emit = defineEmits<{
  (e: 'start'): void
  (e: 'leave'): void
  (e: 'resync'): void
  (e: 'ready', ready: boolean): void
  (e: 'bot', seat: number, add: boolean): void
  (e: 'dissolve'): void
  (e: 'invite-create', opts: { maxUses: number; expiry: number; bypass: boolean }): void
  (e: 'invite-revoke', id: string): void
  (e: 'invite-list'): void
  (e: 'update-room', patch: { name?: string; public?: boolean; password?: string }): void
}>()

const seatWinds = ['东', '南', '西', '北']
const copied = ref(false)
const tokenCopied = ref(false)

const capacity = computed(() => props.room.capacity ?? 4)
const players = computed(() => props.room.players ?? [])
const me = computed(() => players.value.find((p) => p.guestId === props.myGuestId) ?? null)
const isHost = computed(() => props.isHost || me.value?.isHost === true || me.value?.seat === 0)
const amReady = computed(() => me.value?.ready === true)
const unready = computed(() => players.value.filter((p) => !p.ready))
/** 旧版房间快照没有 ready 字段, 视为已准备 */
const allReady = computed(() => players.value.every((p) => p.ready !== false))
const canStartMatch = computed(
  () => props.canStart || (isHost.value && players.value.length >= 2 && allReady.value),
)
const emptySeats = computed(() => {
  const taken = new Set(players.value.map((p) => p.seat))
  const out: number[] = []
  for (let s = 0; s < capacity.value; s++) if (!taken.has(s)) out.push(s)
  return out
})
const rulesLocked = computed(() => props.room.rulesLocked === true)

// 邀请表单
const inviteMaxUses = ref(0)
const inviteExpiry = ref(86400)
const inviteBypass = ref(true)

// 房间改设置
const editName = ref('')
const editing = ref(false)

function copyCode() {
  if (!navigator.clipboard) return
  navigator.clipboard.writeText(props.room.code).then(() => {
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  })
}

function copyToken() {
  if (!navigator.clipboard || !props.lastInviteToken) return
  navigator.clipboard.writeText(props.lastInviteToken).then(() => {
    tokenCopied.value = true
    setTimeout(() => {
      tokenCopied.value = false
    }, 2000)
  })
}

function submitInvite() {
  emit('invite-create', {
    maxUses: inviteMaxUses.value,
    expiry: inviteExpiry.value,
    bypass: inviteBypass.value,
  })
}

function startEdit() {
  editName.value = props.room.name ?? ''
  editing.value = true
}

function submitEdit() {
  const patch: { name?: string; public?: boolean } = {}
  const name = editName.value.trim()
  if (name && name !== props.room.name) patch.name = name
  if (Object.keys(patch).length > 0) emit('update-room', patch)
  editing.value = false
}

function togglePublic() {
  emit('update-room', { public: !props.room.isPublic })
}

function fmtTime(unix: number) {
  if (!unix) return '不过期'
  const d = new Date(unix * 1000)
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
</script>

<template>
  <div class="room-lobby flex w-full max-w-3xl flex-col gap-4 rounded-2xl border border-stone-200 bg-white p-5 shadow-sm">
    <!-- 顶栏: 房间信息与操作 -->
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-stone-100 pb-4">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="truncate text-lg font-bold text-stone-900">{{ room.name || '未命名房间' }}</span>
          <span
            class="rounded px-1.5 py-0.5 text-[10px] font-semibold"
            :class="room.isPublic ? 'bg-sky-100 text-sky-700' : 'bg-stone-100 text-stone-500'"
          >
            {{ room.isPublic ? '公开' : '私有' }}
          </span>
          <span v-if="room.hasPassword" class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold text-amber-700">
            🔒 有密码
          </span>
        </div>
        <div class="mt-1 flex items-center gap-2">
          <span class="text-xs font-semibold tracking-wider text-stone-400 uppercase">邀请码</span>
          <span class="font-mono text-2xl font-black tracking-[0.22em] text-stone-900">{{ room.code }}</span>
          <button
            type="button"
            class="rounded-md border border-stone-300 bg-stone-50 px-2 py-0.5 text-xs text-stone-700 transition hover:bg-stone-100 active:scale-95"
            @click="copyCode"
          >
            {{ copied ? '已复制 ✓' : '复制' }}
          </button>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="rounded-lg border border-stone-300 px-2.5 py-1.5 text-xs text-stone-600 transition hover:border-stone-400 hover:text-stone-900"
          @click="emit('resync')"
        >
          重新同步
        </button>
        <button
          v-if="isHost && !rulesLocked"
          type="button"
          class="rounded-lg border border-stone-300 px-2.5 py-1.5 text-xs text-stone-600 transition hover:bg-stone-50"
          @click="startEdit"
        >
          改房间名
        </button>
        <button
          v-if="isHost && !rulesLocked"
          type="button"
          class="rounded-lg border border-stone-300 px-2.5 py-1.5 text-xs text-stone-600 transition hover:bg-stone-50"
          @click="togglePublic"
        >
          {{ room.isPublic ? '设为私有' : '设为公开' }}
        </button>
        <button
          type="button"
          class="rounded-lg border border-rose-200 bg-rose-50 px-2.5 py-1.5 text-xs font-medium text-rose-700 transition hover:bg-rose-100"
          @click="emit('leave')"
        >
          离开
        </button>
        <button
          v-if="isHost"
          type="button"
          class="rounded-lg border border-rose-300 bg-rose-600 px-2.5 py-1.5 text-xs font-medium text-white transition hover:bg-rose-700"
          @click="emit('dissolve')"
        >
          解散房间
        </button>
      </div>
    </div>

    <div v-if="editing" class="flex items-center gap-2 text-xs">
      <input
        v-model="editName"
        maxlength="24"
        class="flex-1 rounded-lg border border-stone-300 px-2 py-1.5 text-sm outline-none focus:border-emerald-500"
        placeholder="房间名(最多 24 字)"
        @keyup.enter="submitEdit"
      />
      <button type="button" class="rounded-lg bg-emerald-600 px-3 py-1.5 font-medium text-white" @click="submitEdit">
        保存
      </button>
      <button type="button" class="rounded-lg border border-stone-300 px-3 py-1.5" @click="editing = false">取消</button>
    </div>

    <!-- 座位槽位 -->
    <div>
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs font-medium text-stone-500">
        <span>座位 ({{ players.length }} / {{ capacity }})</span>
        <span v-if="!rulesLocked && unready.length > 0" class="font-normal text-amber-600">
          等待 {{ unready.map((p) => p.nickname).join('、') }} 准备
        </span>
      </div>

      <div class="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        <div
          v-for="p in players"
          :key="p.guestId"
          class="flex items-center gap-3 rounded-xl border p-3 transition"
          :class="p.guestId === myGuestId ? 'border-emerald-600/60 bg-emerald-50/50 shadow-xs' : 'border-stone-200 bg-stone-50/50'"
        >
          <div class="flex h-9 w-9 items-center justify-center rounded-lg bg-stone-900 text-sm font-bold text-amber-300 shadow-sm">
            {{ seatWinds[p.seat] ?? p.seat }}
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-1.5">
              <span class="truncate text-sm font-bold text-stone-900">{{ p.nickname }}</span>
              <span v-if="p.guestId === myGuestId" class="rounded bg-emerald-600 px-1 text-[9px] font-bold text-white">我</span>
              <span v-if="p.isHost" class="rounded border border-amber-300/40 bg-amber-100 px-1 text-[9px] font-medium text-amber-800">房主</span>
              <span v-if="p.isBot" class="rounded border border-sky-300/40 bg-sky-100 px-1 text-[9px] font-medium text-sky-700">机器人</span>
            </div>
            <div class="mt-0.5 flex items-center gap-2 text-[11px] text-stone-400">
              <span class="inline-block h-1.5 w-1.5 rounded-full" :class="p.connected || p.isBot ? 'bg-emerald-500' : 'bg-stone-300'" />
              <span>{{ p.isBot ? 'AI 托管' : p.connected ? '在线' : '离线' }}</span>
              <span v-if="p.ready" class="font-medium text-emerald-600">已准备</span>
              <span v-else class="text-stone-400">未准备</span>
            </div>
          </div>
          <button
            v-if="isHost && p.isBot"
            type="button"
            class="rounded-lg border border-stone-300 px-2 py-1 text-[11px] text-stone-500 transition hover:bg-rose-50 hover:text-rose-600"
            @click="emit('bot', p.seat, false)"
          >
            移除
          </button>
        </div>

        <!-- 空位: 房主可补机器人 -->
        <div
          v-for="seat in emptySeats"
          :key="'empty-' + seat"
          class="flex items-center justify-between gap-3 rounded-xl border border-dashed border-stone-200 p-3 text-stone-400"
        >
          <div class="flex items-center gap-3">
            <div class="flex h-9 w-9 items-center justify-center rounded-lg border border-dashed border-stone-200 text-xs">
              {{ seatWinds[seat] }}
            </div>
            <span class="text-xs font-medium">空位 · 等待玩家</span>
          </div>
          <button
            v-if="isHost && !rulesLocked"
            type="button"
            class="rounded-lg border border-sky-300 bg-sky-50 px-2 py-1 text-[11px] font-medium text-sky-700 transition hover:bg-sky-100"
            @click="emit('bot', seat, true)"
          >
            + 机器人
          </button>
        </div>
      </div>
    </div>

    <!-- 邀请面板(房主) -->
    <div v-if="isHost" class="rounded-xl border border-stone-200 bg-stone-50/60 p-3 text-xs">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="font-semibold text-stone-700">邀请链接令牌</span>
        <button type="button" class="text-stone-500 underline-offset-2 hover:underline" @click="emit('invite-list')">
          刷新列表
        </button>
      </div>
      <div class="mt-2 flex flex-wrap items-end gap-2">
        <label class="flex flex-col gap-1">
          <span class="text-[10px] text-stone-500">有效期</span>
          <select v-model.number="inviteExpiry" class="rounded-lg border border-stone-300 px-2 py-1 text-xs">
            <option :value="3600">1 小时</option>
            <option :value="86400">1 天</option>
            <option :value="604800">7 天</option>
            <option :value="0">不过期</option>
          </select>
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-[10px] text-stone-500">可用次数</span>
          <select v-model.number="inviteMaxUses" class="rounded-lg border border-stone-300 px-2 py-1 text-xs">
            <option :value="1">1 次</option>
            <option :value="4">4 次</option>
            <option :value="0">不限</option>
          </select>
        </label>
        <label class="flex items-center gap-1.5 pb-1.5">
          <input v-model="inviteBypass" type="checkbox" class="h-3.5 w-3.5" />
          <span class="text-stone-600">免房间密码</span>
        </label>
        <button
          type="button"
          class="rounded-lg bg-stone-900 px-3 py-1.5 font-medium text-white transition hover:bg-stone-800"
          @click="submitInvite"
        >
          生成邀请
        </button>
      </div>

      <div v-if="lastInviteToken" class="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-emerald-300 bg-emerald-50 p-2">
        <span class="text-[10px] font-semibold text-emerald-700">新令牌(仅显示一次)</span>
        <code class="min-w-0 flex-1 truncate font-mono text-[11px] text-emerald-900">{{ lastInviteToken }}</code>
        <button type="button" class="rounded bg-emerald-600 px-2 py-0.5 text-[11px] text-white" @click="copyToken">
          {{ tokenCopied ? '已复制 ✓' : '复制' }}
        </button>
      </div>

      <ul v-if="invites.length" class="mt-2 divide-y divide-stone-200">
        <li v-for="inv in invites" :key="inv.id" class="flex flex-wrap items-center gap-2 py-1.5">
          <code class="font-mono text-[10px] text-stone-400">{{ inv.id.slice(0, 8) }}</code>
          <span class="text-stone-600">用 {{ inv.uses }}/{{ inv.maxUses || '∞' }}</span>
          <span class="text-stone-400">{{ fmtTime(inv.expiresAt) }}</span>
          <span v-if="inv.bypassRoomPassword" class="rounded bg-sky-100 px-1 text-[10px] text-sky-700">免密</span>
          <span v-if="inv.revoked" class="rounded bg-stone-200 px-1 text-[10px] text-stone-500">已撤销</span>
          <button
            v-else
            type="button"
            class="ml-auto text-[11px] text-rose-600 hover:underline"
            @click="emit('invite-revoke', inv.id)"
          >
            撤销
          </button>
        </li>
      </ul>
      <p v-else class="mt-2 text-[11px] text-stone-400">还没有邀请。生成的令牌只保存哈希, 明文仅显示一次。</p>
    </div>

    <!-- 底部: 准备与开局 -->
    <div class="mt-1 flex flex-col gap-2 rounded-xl border border-stone-200/80 bg-stone-50 p-3 text-xs">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="text-stone-500">
          规则：基础四人麻将(136 张 · 吃/碰/杠 · 七对子 · 自摸与点炮)
          <span v-if="rulesLocked" class="ml-1 font-semibold text-amber-600">已锁定</span>
        </span>
        <span class="text-stone-400">单步 30 秒未操作将自动托管</span>
      </div>

      <div class="flex flex-wrap items-center justify-end gap-2 pt-1">
        <button
          v-if="!amReady && !rulesLocked"
          type="button"
          class="rounded-xl bg-emerald-600 px-4 py-2 font-bold text-white shadow-sm transition hover:bg-emerald-700 active:scale-95"
          @click="emit('ready', true)"
        >
          准备
        </button>
        <button
          v-else-if="!rulesLocked"
          type="button"
          class="rounded-xl border border-stone-300 px-4 py-2 font-medium text-stone-600 transition hover:bg-stone-100"
          @click="emit('ready', false)"
        >
          取消准备
        </button>

        <template v-if="isHost">
          <button
            type="button"
            :disabled="!canStartMatch"
            class="flex items-center gap-1.5 rounded-xl bg-gradient-to-r from-emerald-600 to-teal-700 px-6 py-2.5 font-bold text-white shadow-md transition hover:scale-[1.02] active:scale-95 disabled:cursor-not-allowed disabled:opacity-50"
            @click="emit('start')"
          >
            <span>开始对局</span>
            <span class="text-xs font-normal opacity-90">({{ players.length }}/{{ capacity }})</span>
          </button>
        </template>
        <span v-else-if="!rulesLocked" class="text-stone-400">等待房主开始…</span>
      </div>
      <p v-if="isHost && !canStartMatch" class="text-right text-[11px] text-amber-600">
        需满 {{ capacity }} 人且全部准备(可用机器人补位)才能开局
      </p>
    </div>
  </div>
</template>
