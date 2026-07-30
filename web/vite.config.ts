import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export const webRootPath = fileURLToPath(new URL('.', import.meta.url))
export const pageRouteManifestPath = fileURLToPath(
  new URL('../internal/webui/page_routes.json', import.meta.url),
)
export const devServerFileSystemAllow = [webRootPath, pageRouteManifestPath]

export default defineConfig({
  root: webRootPath,
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    fs: {
      allow: devServerFileSystemAllow,
    },
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:3001',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    manifest: true,
  },
})
