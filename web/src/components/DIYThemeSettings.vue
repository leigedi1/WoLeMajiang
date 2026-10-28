<script setup lang="ts">
/**
 * DIY 主题设置界面。
 *
 * 交互:
 *   1) 密码门(由 App.vue 的齿轮按钮触发) → 本界面
 *   2) 以经典主题为模板列出 34 种牌, 已自定义的牌显示缩略图与「已改」标记
 *   3) 点某张牌 → 二级窗口: 上传图片 + 选填充方式 + 实时预览 + 保存/恢复默认
 *   4) 右上角「修改密码」: 输入一次当前密码 + 新密码
 *
 * 所有写操作都要带密码(服务端每次校验), 密码只保存在本组件内, 不落 localStorage。
 */
import { computed, onMounted, ref, type CSSProperties } from 'vue'
import { useDIYStore } from '../stores/diy'
import { useThemeStore } from '../stores/theme'
import { diyAuth, diyDeleteTile, diySaveTile, diySetPassword } from '../api/client'
import { getClassicTileSvgPath, getTileMeta } from '../utils/tiles'
import MahjongTile from './MahjongTile.vue'

const props = defineProps<{
  /** 进入设置页时已经通过校验的密码 */
  password: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'password-changed', next: string): void
}>()

const diy = useDIYStore()
const themeStore = useThemeStore()

/** 34 种牌, 按 万/条/筒/风/箭 分组 */
const groups: Array<{ label: string; codes: string[] }> = [
  { label: '万子', codes: ['wan1', 'wan2', 'wan3', 'wan4', 'wan5', 'wan6', 'wan7', 'wan8', 'wan9'] },
  { label: '条子', codes: ['tiao1', 'tiao2', 'tiao3', 'tiao4', 'tiao5', 'tiao6', 'tiao7', 'tiao8', 'tiao9'] },
  { label: '筒子', codes: ['tong1', 'tong2', 'tong3', 'tong4', 'tong5', 'tong6', 'tong7', 'tong8', 'tong9'] },
  { label: '风牌', codes: ['dong', 'nan', 'xi', 'bei'] },
  { label: '箭牌', codes: ['zhong', 'fa', 'bai'] },
]

const allCodes = groups.flatMap((g) => g.codes)

/** 填充方式的中文名 */
const FIT_LABELS: Record<string, { name: string; hint: string }> = {
  cover: { name: '填充', hint: '铺满整张牌, 超出部分裁掉(推荐)' },
  contain: { name: '完整', hint: '整张图片完整显示, 四周可能留白' },
  fill: { name: '拉伸', hint: '强行铺满, 图片比例会变形' },
  'scale-down': { name: '缩小', hint: '只在图片过大时等比缩小' },
  none: { name: '原始', hint: '按原尺寸居中, 不缩放' },
}

/** 把服务端返回的填充方式收敛成合法的 object-fit 值。 */
function toObjectFit(fit: string): CSSProperties['objectFit'] {
  switch (fit) {
    case 'contain':
    case 'fill':
    case 'none':
    case 'scale-down':
      return fit
    case 'cover':
    default:
      return 'cover'
  }
}

/* ------------------------------------------------------------------ 编辑态 */

const editing = ref<string | null>(null)
const editDataUrl = ref('')
const editFit = ref('cover')
const editBusy = ref(false)
const editError = ref('')
const previewTheme = ref<'diy' | 'classic'>('diy')
const fileInput = ref<HTMLInputElement | null>(null)

/** 当前编辑牌的自定义样式(用于预览时先看旧图) */
const editingStyle = computed(() => (editing.value ? diy.styleOf(editing.value) : undefined))

/** 预览用的临时覆盖: 让 MahjongTile 立刻显示新图 */
const previewUrl = computed(() => editDataUrl.value || editingStyle.value?.url || '')

function openEditor(code: string): void {
  editing.value = code
  editError.value = ''
  editDataUrl.value = ''
  editFit.value = diy.styleOf(code)?.fit ?? 'cover'
  previewTheme.value = 'diy'
}

function closeEditor(): void {
  editing.value = null
  editDataUrl.value = ''
  editError.value = ''
}

/** 选文件: 校验类型与体积后读成 data URL */
async function onPickFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  editError.value = ''
  if (!/^image\/(png|jpeg|webp|gif)$/.test(file.type)) {
    editError.value = '只支持 PNG / JPEG / WebP / GIF 图片'
    return
  }
  if (file.size > diy.maxBytes) {
    editError.value = `图片不能超过 ${Math.round(diy.maxBytes / 1024)} KB`
    return
  }
  try {
    editDataUrl.value = await readAsDataURL(file)
  } catch {
    editError.value = '读取图片失败, 请重试'
  }
}

function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

async function saveTile(): Promise<void> {
  const code = editing.value
  if (!code || editBusy.value) return
  if (!editDataUrl.value && !editingStyle.value) {
    editError.value = '请先选择一张图片'
    return
  }
  editBusy.value = true
  editError.value = ''
  try {
    // 只改填充方式时复用已有图片, 不必重新上传。
    const dataUrl = editDataUrl.value
    if (!dataUrl) {
      // 仅调整填充方式: 用当前图片重新提交一次(服务端按新 fit 覆盖记录)。
      const resp = await fetch(editingStyle.value!.url)
      const blob = await resp.blob()
      const reused = await readAsDataURL(new File([blob], 'tile', { type: blob.type }))
      const saved = await diySaveTile({
        password: props.password,
        code,
        fit: editFit.value,
        dataUrl: reused,
      })
      diy.applyTile(saved)
    } else {
      const saved = await diySaveTile({
        password: props.password,
        code,
        fit: editFit.value,
        dataUrl,
      })
      diy.applyTile(saved)
    }
    closeEditor()
  } catch (err) {
    editError.value = err instanceof Error ? err.message : '保存失败'
  } finally {
    editBusy.value = false
  }
}

async function resetTile(): Promise<void> {
  const code = editing.value
  if (!code || editBusy.value) return
  editBusy.value = true
  editError.value = ''
  try {
    await diyDeleteTile(props.password, code)
    diy.dropTile(code)
    closeEditor()
  } catch (err) {
    editError.value = err instanceof Error ? err.message : '恢复失败'
  } finally {
    editBusy.value = false
  }
}

/* ------------------------------------------------------------------ 改密码 */

const showPasswordForm = ref(false)
const pwCurrent = ref('')
const pwNext = ref('')
const pwConfirm = ref('')
const pwBusy = ref(false)
const pwError = ref('')
const pwDone = ref('')

function openPasswordForm(): void {
  showPasswordForm.value = true
  pwCurrent.value = ''
  pwNext.value = ''
  pwConfirm.value = ''
  pwError.value = ''
  pwDone.value = ''
}

async function submitPassword(): Promise<void> {
  if (pwBusy.value) return
  pwError.value = ''
  pwDone.value = ''
  if (pwNext.value.length < 4) {
    pwError.value = '新密码至少 4 个字符'
    return
  }
  if (pwNext.value !== pwConfirm.value) {
    pwError.value = '两次输入的新密码不一致'
    return
  }
  pwBusy.value = true
  try {
    await diySetPassword(pwCurrent.value, pwNext.value)
    pwDone.value = '密码已更新'
    emit('password-changed', pwNext.value)
    pwCurrent.value = ''
    pwNext.value = ''
    pwConfirm.value = ''
    setTimeout(() => {
      showPasswordForm.value = false
    }, 900)
  } catch (err) {
    pwError.value = err instanceof Error ? err.message : '修改失败'
  } finally {
    pwBusy.value = false
  }
}

/* ------------------------------------------------------------------ 其他 */

/** 用当前已保存的 DIY 主题预览效果(不改个人偏好) */
function previewOnTable(): void {
  themeStore.setPlayerPreference('diy')
  emit('close')
}

onMounted(() => {
  void diy.refresh()
  // 顺便把服务端已有的密码状态同步进来(判断是否显示「首次设置密码」)
  void diyAuth(props.password).catch(() => {
    /* 已在进入时校验过, 这里失败不影响编辑 */
  })
})
</script>

<template>
  <div class="min-h-screen bg-stone-100 text-stone-800">
    <div class="mx-auto max-w-5xl px-4 py-5">
      <!-- 顶部栏 -->
      <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h1 class="text-xl font-semibold tracking-tight">DIY 牌面设置</h1>
          <p class="mt-0.5 text-xs text-stone-500">
            以「经典图案」为模板逐张替换牌面。保存后所有选择 DIY 主题的玩家都会看到同一套牌面。
          </p>
        </div>
        <div class="flex items-center gap-2">
          <span class="rounded-full bg-stone-200 px-2.5 py-1 text-xs font-medium text-stone-600">
            已自定义 {{ diy.count }} / {{ allCodes.length }}
          </span>
          <button
            type="button"
            class="rounded-lg border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:border-stone-400"
            @click="openPasswordForm"
          >
            🔑 修改密码
          </button>
          <button
            type="button"
            class="rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-700"
            @click="previewOnTable"
          >
            在牌桌预览
          </button>
          <button
            type="button"
            class="rounded-lg border border-stone-300 bg-white px-3 py-1.5 text-xs text-stone-600 hover:border-stone-400"
            @click="emit('close')"
          >
            关闭
          </button>
        </div>
      </div>

      <p v-if="diy.error" class="mb-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-600">{{ diy.error }}</p>

      <!-- 牌面分组 -->
      <div v-for="group in groups" :key="group.label" class="mb-4 rounded-xl border border-stone-200 bg-white p-3 shadow-sm">
        <div class="mb-2 text-xs font-semibold text-stone-500">{{ group.label }}</div>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="code in group.codes"
            :key="code"
            type="button"
            class="group relative rounded-lg border border-stone-200 bg-stone-50 p-1.5 transition hover:border-emerald-400 hover:bg-emerald-50/40"
            :title="`${getTileMeta(code).name} · 点击自定义`"
            @click="openEditor(code)"
          >
            <div class="relative">
              <!-- 已自定义的牌直接显示自定义图, 否则显示经典图 -->
              <img
                v-if="diy.styleOf(code)"
                :src="diy.styleOf(code)!.url"
                :alt="getTileMeta(code).name"
                class="h-14 w-10 rounded-[3px] border border-stone-300 bg-white"
                :style="{ objectFit: toObjectFit(diy.styleOf(code)!.fit) }"
              />
              <img
                v-else
                :src="getClassicTileSvgPath(code)"
                :alt="getTileMeta(code).name"
                class="h-14 w-10 rounded-[3px] border border-stone-300 bg-white object-contain"
              />
              <span
                v-if="diy.styleOf(code)"
                class="absolute -right-1 -top-1 rounded-full bg-emerald-600 px-1 text-[9px] font-bold text-white"
              >已改</span>
            </div>
            <div class="mt-1 text-center text-[10px] text-stone-500">{{ getTileMeta(code).shortName }}</div>
          </button>
        </div>
      </div>

      <p class="mt-2 text-center text-[11px] text-stone-400">
        建议上传接近 3:4 的竖图；不同填充方式会明显改变观感，保存前可先在右侧预览。
      </p>
    </div>

    <!-- 二级窗口: 编辑单张牌 -->
    <Teleport to="body">
      <div
        v-if="editing"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3"
        @click.self="closeEditor"
      >
        <div class="w-full max-w-2xl overflow-hidden rounded-2xl bg-white shadow-2xl">
          <div class="flex items-center justify-between border-b border-stone-200 px-4 py-3">
            <div class="text-sm font-semibold">
              自定义「{{ getTileMeta(editing).name }}」
            </div>
            <button type="button" class="rounded px-2 py-1 text-stone-400 hover:bg-stone-100" @click="closeEditor">✕</button>
          </div>

          <div class="grid gap-4 p-4 sm:grid-cols-[1fr_14rem]">
            <!-- 左: 上传 + 填充方式 -->
            <div>
              <div class="mb-1 text-xs font-medium text-stone-600">1. 选择图片</div>
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  class="rounded-lg bg-stone-800 px-3 py-2 text-xs font-medium text-white hover:bg-stone-900"
                  @click="fileInput?.click()"
                >
                  📁 上传图片
                </button>
                <span class="truncate text-[11px] text-stone-500">
                  {{ editDataUrl ? '已选择新图片' : editingStyle ? '当前使用已保存的图片' : '尚未选择图片' }}
                </span>
                <input
                  ref="fileInput"
                  type="file"
                  accept="image/png,image/jpeg,image/webp,image/gif"
                  class="hidden"
                  @change="onPickFile"
                />
              </div>
              <p class="mt-1 text-[10px] text-stone-400">
                支持 PNG / JPEG / WebP / GIF，单张不超过 {{ Math.round(diy.maxBytes / 1024) }} KB。
              </p>

              <div class="mt-4 mb-1 text-xs font-medium text-stone-600">2. 填充方式</div>
              <div class="flex flex-wrap gap-1.5">
                <button
                  v-for="fit in diy.fits"
                  :key="fit"
                  type="button"
                  class="rounded-lg border px-2.5 py-1.5 text-xs transition"
                  :class="editFit === fit
                    ? 'border-emerald-500 bg-emerald-50 font-semibold text-emerald-700'
                    : 'border-stone-300 bg-white text-stone-600 hover:border-stone-400'"
                  :title="FIT_LABELS[fit]?.hint"
                  @click="editFit = fit"
                >
                  {{ FIT_LABELS[fit]?.name ?? fit }}
                </button>
              </div>
              <p class="mt-1 text-[10px] text-stone-400">{{ FIT_LABELS[editFit]?.hint }}</p>

              <p v-if="editError" class="mt-3 rounded-lg bg-red-50 px-2.5 py-1.5 text-[11px] text-red-600">{{ editError }}</p>
            </div>

            <!-- 右: 预览 -->
            <div class="rounded-xl bg-stone-100 p-3">
              <div class="mb-2 text-center text-xs font-medium text-stone-600">3. 预览</div>
              <div class="flex items-end justify-center gap-2">
                <!-- 自定义效果(有临时图则用临时图) -->
                <div class="flex flex-col items-center gap-1">
                  <div class="rounded-[4px] border border-stone-300 bg-white shadow-sm" style="width: 4rem; height: 5.5rem">
                    <img
                      v-if="previewUrl"
                      :src="previewUrl"
                      :alt="getTileMeta(editing).name"
                      class="h-full w-full rounded-[3px]"
                      :style="{ objectFit: toObjectFit(editFit) }"
                    />
                    <img
                      v-else
                      :src="getClassicTileSvgPath(editing)"
                      :alt="getTileMeta(editing).name"
                      class="h-full w-full rounded-[3px] object-contain"
                    />
                  </div>
                  <span class="text-[10px] text-stone-500">DIY 效果</span>
                </div>
                <!-- 经典对照 -->
                <div class="flex flex-col items-center gap-1">
                  <img
                    :src="getClassicTileSvgPath(editing)"
                    :alt="getTileMeta(editing).name"
                    class="rounded-[4px] border border-stone-300 bg-white shadow-sm"
                    style="width: 4rem; height: 5.5rem"
                  />
                  <span class="text-[10px] text-stone-500">经典对照</span>
                </div>
              </div>

              <!-- 牌桌上的实际尺寸预览 -->
              <div class="mt-4">
                <div class="mb-1 text-center text-[10px] text-stone-500">牌桌实际大小</div>
                <div class="flex items-end justify-center gap-1">
                  <MahjongTile :code="editing" size="md" theme="diy" />
                  <MahjongTile :code="editing" size="sm" theme="diy" />
                  <MahjongTile :code="editing" size="xs" theme="diy" />
                </div>
                <p class="mt-1 text-center text-[9px] text-stone-400">
                  (若这里没有变化, 说明还没保存)
                </p>
              </div>
            </div>
          </div>

          <div class="flex items-center justify-between gap-2 border-t border-stone-200 bg-stone-50 px-4 py-3">
            <button
              v-if="editingStyle"
              type="button"
              class="rounded-lg border border-rose-200 bg-white px-3 py-1.5 text-xs text-rose-600 hover:bg-rose-50 disabled:opacity-50"
              :disabled="editBusy"
              @click="resetTile"
            >
              恢复默认外观
            </button>
            <span v-else></span>
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="rounded-lg border border-stone-300 bg-white px-3 py-1.5 text-xs text-stone-600 hover:border-stone-400"
                @click="closeEditor"
              >
                取消
              </button>
              <button
                type="button"
                class="rounded-lg bg-emerald-600 px-4 py-1.5 text-xs font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
                :disabled="editBusy || (!editDataUrl && !editingStyle)"
                @click="saveTile"
              >
                {{ editBusy ? '保存中…' : '保存到 DIY 主题' }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 修改密码窗口 -->
    <Teleport to="body">
      <div
        v-if="showPasswordForm"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3"
        @click.self="showPasswordForm = false"
      >
        <div class="w-full max-w-sm overflow-hidden rounded-2xl bg-white shadow-2xl">
          <div class="flex items-center justify-between border-b border-stone-200 px-4 py-3">
            <div class="text-sm font-semibold">修改 DIY 密码</div>
            <button type="button" class="rounded px-2 py-1 text-stone-400 hover:bg-stone-100" @click="showPasswordForm = false">✕</button>
          </div>
          <div class="p-4">
            <label class="mb-1 block text-xs font-medium text-stone-600">当前密码</label>
            <input
              v-model="pwCurrent"
              type="password"
              class="w-full rounded-lg border border-stone-300 px-3 py-2 text-sm focus:border-emerald-500 focus:outline-none"
              placeholder="输入当前密码"
              autocomplete="current-password"
            />
            <label class="mt-3 mb-1 block text-xs font-medium text-stone-600">新密码</label>
            <input
              v-model="pwNext"
              type="password"
              class="w-full rounded-lg border border-stone-300 px-3 py-2 text-sm focus:border-emerald-500 focus:outline-none"
              placeholder="至少 4 个字符"
              autocomplete="new-password"
            />
            <label class="mt-3 mb-1 block text-xs font-medium text-stone-600">确认新密码</label>
            <input
              v-model="pwConfirm"
              type="password"
              class="w-full rounded-lg border border-stone-300 px-3 py-2 text-sm focus:border-emerald-500 focus:outline-none"
              placeholder="再输入一次"
              autocomplete="new-password"
              @keyup.enter="submitPassword"
            />
            <p v-if="pwError" class="mt-2 rounded-lg bg-red-50 px-2.5 py-1.5 text-[11px] text-red-600">{{ pwError }}</p>
            <p v-if="pwDone" class="mt-2 rounded-lg bg-emerald-50 px-2.5 py-1.5 text-[11px] text-emerald-700">{{ pwDone }}</p>
            <button
              type="button"
              class="mt-4 w-full rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
              :disabled="pwBusy"
              @click="submitPassword"
            >
              {{ pwBusy ? '提交中…' : '确认修改' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
