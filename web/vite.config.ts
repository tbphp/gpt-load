import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { configDefaults, defineConfig } from 'vitest/config'

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
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    manifest: true,
  },
  test: {
    environment: 'happy-dom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    exclude: [...configDefaults.exclude, 'e2e/**/*.spec.ts'],
    css: {
      include: [/(base|tokens)\.css/],
    },
  },
})
