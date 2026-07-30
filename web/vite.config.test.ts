// @vitest-environment node

import { fileURLToPath, URL } from 'node:url'

import { devServerFileSystemAllow, pageRouteManifestPath, webRootPath } from './vite.config'

describe('Vite filesystem boundary', () => {
  it('is fixed to the web root and the one shared manifest file', () => {
    expect(webRootPath).toBe(fileURLToPath(new URL('.', import.meta.url)))
    expect(pageRouteManifestPath).toBe(
      fileURLToPath(new URL('../internal/webui/page_routes.json', import.meta.url)),
    )
    expect(devServerFileSystemAllow).toEqual([webRootPath, pageRouteManifestPath])
  })
})
