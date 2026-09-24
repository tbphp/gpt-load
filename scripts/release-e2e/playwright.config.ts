import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: '.',
  timeout: 30_000,
  expect: { timeout: 12_000 },
  retries: 0,
  workers: 1,
  reporter: [
    ['line'],
    [
      'html',
      { outputFolder: process.env.RELEASE_E2E_REPORT ?? 'playwright-report', open: 'never' },
    ],
  ],
  outputDir: process.env.RELEASE_E2E_OUTPUT ?? 'test-results',
  use: {
    baseURL: process.env.RELEASE_TEST_BASE_URL,
    browserName: 'chromium',
    locale: 'zh-CN',
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
})
