import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export const webRootPath = fileURLToPath(new URL('.', import.meta.url))
export const pageRouteManifestPath = fileURLToPath(
  new URL('../internal/webui/page_routes.json', import.meta.url),
)
export const devServerFileSystemAllow = [webRootPath, pageRouteManifestPath]

const proxyTarget = process.env.VITE_DEV_PROXY_TARGET || 'http://127.0.0.1:3001'

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
    proxy: Object.fromEntries(
      ['/api', '/health', '/v1', '/v1beta'].map((path) => [
        path,
        { target: proxyTarget, changeOrigin: true },
      ]),
    ),
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    manifest: true,
  },
})
