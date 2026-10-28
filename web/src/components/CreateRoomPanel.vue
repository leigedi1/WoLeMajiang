<script setup lang="ts">
/**
 * 创建在线房间表单: 名称/可见性/密码/主题。
 * 规则人数固定 4(阶段5 仅实现四人规则, 2/3 人未开放, 显式标注)。
 */
import { computed, ref } from 'vue'

interface Props {
  connected: boolean
  disabled?: boolean
}

const props = withDefaults(defineProps<Props>(), { disabled: false })

const emit = defineEmits<{
  (e: 'create', opts: { name: string; password: string; public: boolean; theme: string }): void
}>()

const name = ref('')
const password = ref('')
const isPublic = ref(true)
const theme = ref('classic')
const showPassword = ref(false)

const canSubmit = computed(() => props.connected && !props.disabled)
const passwordValid = computed(() => password.value.length === 0 || password.value.length >= 4)

function submit() {
  if (!canSubmit.value || !passwordValid.value) return
  emit('create', {
    name: name.value.trim(),
    password: password.value,
    public: isPublic.value,
    theme: theme.value,
  })
}
</script>

<template>
  <section class="flex w-full max-w-3xl flex-col gap-3 rounded-2xl border border-stone-200 bg-white p-5 shadow-sm">
    <header>
      <h2 class="text-base font-bold text-stone-900">创建在线房间</h2>
      <p class="text-xs text-stone-400">同一时刻最多 3 个在线房间；房间内最多 4 人（可用机器人补位）</p>
    </header>

    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <label class="flex flex-col gap-1">
        <span class="text-[11px] font-medium text-stone-500">房间名</span>
        <input
          v-model="name"
          maxlength="24"
          class="rounded-lg border border-stone-300 px-2.5 py-2 text-sm outline-none focus:border-emerald-500"
          placeholder="例如：老友局（默认未命名房间）"
        />
      </label>

      <label class="flex flex-col gap-1">
        <span class="text-[11px] font-medium text-stone-500">主题</span>
        <select v-model="theme" class="rounded-lg border border-stone-300 px-2.5 py-2 text-sm outline-none focus:border-emerald-500">
          <option value="classic">经典图案</option>
          <option value="ink">明文水墨</option>
          <option value="diy">DIY 自定义</option>
        </select>
      </label>

      <label class="flex flex-col gap-1">
        <span class="text-[11px] font-medium text-stone-500">房间密码（可选，至少 4 位）</span>
        <div class="flex gap-1.5">
          <input
            v-model="password"
            :type="showPassword ? 'text' : 'password'"
            maxlength="64"
            class="min-w-0 flex-1 rounded-lg border border-stone-300 px-2.5 py-2 text-sm outline-none focus:border-emerald-500"
            :class="passwordValid ? '' : 'border-rose-400'"
            placeholder="留空表示无密码"
          />
          <button
            type="button"
            class="rounded-lg border border-stone-300 px-2 text-xs text-stone-500"
            @click="showPassword = !showPassword"
          >
            {{ showPassword ? '隐藏' : '显示' }}
          </button>
        </div>
        <span v-if="!passwordValid" class="text-[11px] text-rose-600">密码长度需为 4~64 位</span>
      </label>

      <div class="flex flex-col justify-center gap-2">
        <label class="flex items-center gap-2 text-sm text-stone-600">
          <input v-model="isPublic" type="checkbox" class="h-4 w-4" />
          在公开大厅显示（关闭后只能靠邀请码/令牌加入）
        </label>
        <p class="text-[11px] text-stone-400">规则：四人基础麻将（136 张 · 吃碰杠 · 七对子 · 自摸与点炮）</p>
      </div>
    </div>

    <div class="flex items-center justify-end gap-2">
      <span v-if="!connected" class="text-[11px] text-amber-600">请先连接服务器</span>
      <button
        type="button"
        :disabled="!canSubmit || !passwordValid"
        class="rounded-xl bg-gradient-to-r from-emerald-600 to-teal-700 px-5 py-2.5 text-sm font-bold text-white shadow-md transition hover:scale-[1.02] active:scale-95 disabled:cursor-not-allowed disabled:opacity-50"
        @click="submit"
      >
        创建房间
      </button>
    </div>
  </section>
</template>
