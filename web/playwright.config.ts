import { defineConfig } from '@playwright/test'

const baseURL = process.env.GPT_LOAD_E2E_BASE_URL
if (!baseURL) {
  throw new Error('Use pnpm run test:e2e so the isolated E2E harness can provide a base URL')
}

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.spec.ts',
  outputDir: process.env.GPT_LOAD_E2E_ARTIFACT_DIR ?? 'test-results/unscoped',
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  globalTeardown: './e2e/global-teardown.ts',
  use: {
    baseURL,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    locale: 'en-US',
    timezoneId: 'UTC',
    colorScheme: 'light',
    contextOptions: { reducedMotion: 'reduce' },
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    { name: 'webkit', use: { browserName: 'webkit' } },
  ],
})
