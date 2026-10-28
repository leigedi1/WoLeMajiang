<script setup lang="ts">
/**
 * 公开大厅: 房间列表 + 刷新 + 密码加入 + 邀请令牌加入。
 * 纯展示组件; 数据来自 connection store 的 room.list 推送。
 */
import { computed, ref } from 'vue'
import type { LobbyRoomView } from '../protocol/types'

interface Props {
  rooms: LobbyRoomView[]
  onlineRooms: number
  connected: boolean
}

const props = defineProps<Props>()

const emit = defineEmits<{
  (e: 'refresh'): void
  (e: 'join', code: string, password: string): void
  (e: 'join-invite', token: string, code: string, password: string): void
}>()

const joinCode = ref('')
const joinPassword = ref('')
const inviteToken = ref('')
const invitePassword = ref('')
const inviteCode = ref('')
const passwordPromptFor = ref<string | null>(null)
const promptPassword = ref('')

const hasRooms = computed(() => props.rooms.length > 0)

function join(code: string, hasPassword: boolean) {
  if (hasPassword) {
    passwordPromptFor.value = code
    promptPassword.value = ''
    return
  }
  emit('join', code, '')
}

function submitPassword() {
  if (!passwordPromptFor.value) return
  emit('join', passwordPromptFor.value, promptPassword.value)
  passwordPromptFor.value = null
  promptPassword.value = ''
}

function submitCode() {
  const code = joinCode.value.trim().toUpperCase()
  if (!code) return
  const target = props.rooms.find((r) => r.code === code)
  emit('join', code, target?.hasPassword ? joinPassword.value : '')
}

function submitInvite() {
  const token = inviteToken.value.trim()
  if (!token) return
  emit('join-invite', token, inviteCode.value.trim().toUpperCase(), invitePassword.value)
}

function statusLabel(status: string) {
  switch (status) {
    case 'lobby':
      return '等待中'
    case 'playing':
      return '对局中'
    case 'finished':
      return '已结束'
    default:
      return status
  }
}
</script>

<template>
  <section class="flex w-full max-w-3xl flex-col gap-3 rounded-2xl border border-stone-200 bg-white p-5 shadow-sm">
    <header class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h2 class="text-base font-bold text-stone-900">公开房间大厅</h2>
        <p class="text-xs text-stone-400">
          在线房间 {{ onlineRooms }} 个 · 只有公开房间会出现在这里
        </p>
      </div>
      <button
        type="button"
        :disabled="!connected"
        class="rounded-lg border border-stone-300 px-3 py-1.5 text-xs text-stone-600 transition hover:bg-stone-50 disabled:opacity-40"
        @click="emit('refresh')"
      >
        刷新列表
      </button>
    </header>

    <ul v-if="hasRooms" class="flex flex-col gap-2">
      <li
        v-for="r in rooms"
        :key="r.code"
        class="flex flex-wrap items-center gap-3 rounded-xl border border-stone-200 bg-stone-50/60 p-3"
      >
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="truncate text-sm font-bold text-stone-900">{{ r.name || '未命名房间' }}</span>
            <span class="rounded bg-stone-900/90 px-1 font-mono text-[10px] tracking-wider text-amber-300">{{ r.code }}</span>
            <span v-if="r.hasPassword" class="rounded bg-amber-100 px-1 text-[10px] font-medium text-amber-700">🔒</span>
            <span
              class="rounded px-1 text-[10px]"
              :class="r.status === 'playing' ? 'bg-sky-100 text-sky-700' : 'bg-stone-200 text-stone-600'"
            >
              {{ statusLabel(r.status) }}
            </span>
          </div>
          <p class="mt-0.5 text-[11px] text-stone-400">
            房主 {{ r.hostNickname }} · {{ r.players }}/{{ r.capacity }} 人 · 主题 {{ r.theme }}
          </p>
        </div>
        <button
          type="button"
          :disabled="r.players >= r.capacity || r.status === 'playing'"
          class="rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-40"
          @click="join(r.code, r.hasPassword)"
        >
          {{ r.players >= r.capacity ? '已满' : '加入' }}
        </button>
      </li>
    </ul>
    <p v-else class="rounded-xl border border-dashed border-stone-200 p-4 text-center text-xs text-stone-400">
      暂无公开房间。可以创建一个公开房间, 或直接用邀请码加入朋友的房间。
    </p>

    <!-- 密码输入 -->
    <div v-if="passwordPromptFor" class="flex flex-wrap items-center gap-2 rounded-xl border border-amber-300 bg-amber-50 p-3 text-xs">
      <span class="font-medium text-amber-800">房间 {{ passwordPromptFor }} 需要密码</span>
      <input
        v-model="promptPassword"
        type="password"
        class="min-w-0 flex-1 rounded-lg border border-amber-300 px-2 py-1.5 text-sm outline-none focus:border-amber-500"
        placeholder="房间密码"
        @keyup.enter="submitPassword"
      />
      <button type="button" class="rounded-lg bg-amber-600 px-3 py-1.5 font-medium text-white" @click="submitPassword">
        加入
      </button>
      <button type="button" class="rounded-lg border border-amber-300 px-3 py-1.5" @click="passwordPromptFor = null">
        取消
      </button>
    </div>

    <!-- 邀请码 / 令牌加入 -->
    <div class="grid grid-cols-1 gap-2 border-t border-stone-100 pt-3 sm:grid-cols-2">
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] font-medium text-stone-500">用邀请码加入</label>
        <div class="flex gap-1.5">
          <input
            v-model="joinCode"
            maxlength="8"
            class="w-24 rounded-lg border border-stone-300 px-2 py-1.5 text-center font-mono text-sm tracking-widest uppercase outline-none focus:border-emerald-500"
            placeholder="ABC123"
            @keyup.enter="submitCode"
          />
          <input
            v-model="joinPassword"
            type="password"
            class="min-w-0 flex-1 rounded-lg border border-stone-300 px-2 py-1.5 text-sm outline-none focus:border-emerald-500"
            placeholder="密码(可选)"
            @keyup.enter="submitCode"
          />
          <button type="button" class="rounded-lg bg-stone-900 px-3 py-1.5 text-xs font-medium text-white" @click="submitCode">
            加入
          </button>
        </div>
      </div>

      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] font-medium text-stone-500">用邀请令牌加入</label>
        <div class="flex gap-1.5">
          <input
            v-model="inviteToken"
            class="min-w-0 flex-1 rounded-lg border border-stone-300 px-2 py-1.5 font-mono text-xs outline-none focus:border-emerald-500"
            placeholder="32 位令牌"
          />
          <input
            v-model="inviteCode"
            class="w-20 rounded-lg border border-stone-300 px-2 py-1.5 text-center font-mono text-xs uppercase outline-none focus:border-emerald-500"
            placeholder="邀请码"
          />
          <button type="button" class="rounded-lg bg-stone-900 px-3 py-1.5 text-xs font-medium text-white" @click="submitInvite">
            加入
          </button>
        </div>
        <input
          v-model="invitePassword"
          type="password"
          class="rounded-lg border border-stone-300 px-2 py-1.5 text-xs outline-none focus:border-emerald-500"
          placeholder="房间密码(令牌未免密时需要)"
        />
      </div>
    </div>
  </section>
</template>
