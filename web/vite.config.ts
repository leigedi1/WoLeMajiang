import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// 后端地址可用环境变量 MAJIANG_API 覆盖(本机 8080 常被其他软件占用)。
const apiTarget = process.env.MAJIANG_API || 'http://127.0.0.1:8080'

// 部署前缀: 根路径部署留空(默认 '/', 现线上 https://mj.wole110.xyz/ 即如此),
// 只有将来放回子路径(如 /mj/)时才用 MAJIANG_BASE=/mj/ 构建。
// 该值会被写成 import.meta.env.BASE_URL, web/src/api/client.ts 的 basePath() 据此拼 REST/WS 地址。
// 注意: 必须构建进产物, 不能用运行时拼接 —— 否则 /mj/ 下会请求到站点根部。
const deployBase = process.env.MAJIANG_BASE || '/'

// 开发服务器将 /api 与 /ws 代理到 Go 后端, 前端同源访问无需 CORS 配置。
export default defineConfig({
  base: deployBase,
  plugins: [vue(), tailwindcss()],
  server: {
    port: 5173,
    // Windows 上 pnpm/npm 偶尔会在项目根留下 .package.json.*.tmpdir 临时目录,
    // vite 的 watcher 一旦 watch 到它就会 EBUSY 崩溃(实测多次), 这里直接忽略。
    watch: {
      ignored: [
        '**/.package.json.*.tmpdir/**',
        '**/*.tmpdir/**',
        '**/.git/**',
        '**/dist/**',
        '**/*.tsbuildinfo',
      ],
    },
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: false,
      },
      '/ws': {
        target: apiTarget,
        ws: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
  },
} as ReturnType<typeof defineConfig>)
