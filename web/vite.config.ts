import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

// 开发期：Wails 窗口指向 Vite dev server，API 反代到内嵌服务。
// 生产期：前端由内嵌服务同源提供（见 internal/api/startup.go），不存在跨源。
//
// 端口 0 表示随机，见 docs/plans/wails-migration.md §Round 1：
// 端口分配由内核决定，避免与本机已有服务冲突。
const EMBEDDED_PORT = process.env.RFERP_DEV_PORT ?? '0'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: `http://127.0.0.1:${EMBEDDED_PORT}`, changeOrigin: false },
      '/healthz': { target: `http://127.0.0.1:${EMBEDDED_PORT}`, changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})