/// <reference types="vitest/config" />
import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// The dev server proxies /api to the Go API so the browser talks to one origin,
// exactly like nginx does in the container (web/nginx.conf).
const apiTarget = process.env.SCC_API_URL ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': { target: apiTarget, changeOrigin: false },
    },
  },
  build: {
    sourcemap: false,
    // Vite inlines assets under 4 KB as data: URLs, and nginx's CSP (font-src 'self') blocks
    // data: fonts. Keep fonts as files; everything else keeps the default behaviour.
    assetsInlineLimit: (file) => (file.endsWith('.woff2') ? false : undefined),
  },
  test: {
    environment: 'jsdom',
    include: ['tests/**/*.{test,spec}.ts'],
    globals: false,
    restoreMocks: true,
  },
})
