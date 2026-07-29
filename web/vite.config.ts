/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: true }), react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    // ECharts (~530 kB) already lands in its own chunk, loaded only on the
    // chart/costs routes via the lazy use-echarts hook — it is intentionally
    // split, not shipped on first paint. Pin it to a stable vendor chunk and
    // lift the advisory limit above its real size so the build stays quiet
    // without hiding genuinely oversized app chunks.
    chunkSizeWarningLimit: 600,
    rollupOptions: {
      output: {
        manualChunks: (id: string) => (id.includes('node_modules/echarts') ? 'echarts' : undefined),
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    // Each test file spins up a full jsdom environment; running as many in
    // parallel as there are cores thrashes CPU on loaded machines/CI and
    // starves React renders into timeouts. Cap parallelism and give slow
    // renders headroom so the suite is deterministic under load.
    maxWorkers: '50%',
    testTimeout: 15000,
  },
})
