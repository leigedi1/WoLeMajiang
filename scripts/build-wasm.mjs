#!/usr/bin/env node
/**
 * 构建单机引擎 WebAssembly 并放置到前端静态目录。
 *
 * 产物:
 *   web/public/engine/engine.wasm      Go 权威引擎(game + ai + local)的 wasm 编译
 *   web/public/engine/wasm_exec.js     Go 官方运行时胶水代码(取自 GOROOT)
 *
 * 说明:
 *   - 单机模式不依赖后端, 规则判定与联机完全相同(同一份 Go 代码);
 *   - 产物不入库(体积大且随源码变化), 需要在开发/构建前执行一次;
 *   - Go 工具链位置可用环境变量 GO_BIN 指定。
 */
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, statSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const serverDir = join(root, 'server')
const outDir = join(root, 'web', 'public', 'engine')

const candidates = [
  process.env.GO_BIN,
  'go',
  'C:\\Program Files\\Go\\bin\\go.exe',
  'D:\\SOFT\\go\\bin\\go.exe',
].filter(Boolean)

function findGo() {
  for (const bin of candidates) {
    try {
      execFileSync(bin, ['version'], { stdio: 'pipe' })
      return bin
    } catch {
      /* 继续尝试下一个 */
    }
  }
  throw new Error('找不到 Go 工具链, 请安装 Go 或用 GO_BIN 指定 go 可执行文件路径')
}

const go = findGo()
const env = { ...process.env, GOOS: 'js', GOARCH: 'wasm', CGO_ENABLED: '0' }

console.log(`[build-wasm] Go: ${go}`)
console.log('[build-wasm] 编译 ./cmd/wasm → web/public/engine/engine.wasm')

mkdirSync(outDir, { recursive: true })
execFileSync(go, ['build', '-trimpath', '-ldflags=-s -w', '-o', join(outDir, 'engine.wasm'), './cmd/wasm'], {
  cwd: serverDir,
  env,
  stdio: 'inherit',
})

const goroot = execFileSync(go, ['env', 'GOROOT'], { encoding: 'utf8' }).trim()
const glueCandidates = [
  join(goroot, 'lib', 'wasm', 'wasm_exec.js'),
  join(goroot, 'misc', 'wasm', 'wasm_exec.js'),
]
const glue = glueCandidates.find((p) => existsSync(p))
if (!glue) {
  throw new Error(`在 ${goroot} 下找不到 wasm_exec.js (lib/wasm 或 misc/wasm)`)
}
copyFileSync(glue, join(outDir, 'wasm_exec.js'))

const wasmSize = statSync(join(outDir, 'engine.wasm')).size
console.log(`[build-wasm] 完成: engine.wasm ${(wasmSize / 1024 / 1024).toFixed(2)} MB, wasm_exec.js 已就位`)
