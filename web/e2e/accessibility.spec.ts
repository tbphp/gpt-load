import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'

import { expect, test, type Page, type TestInfo } from '@playwright/test'

import { installVisualApi, visualAuthKey } from './visual-api'
import type { VisualScenario } from './visual-fixtures'

interface AccessibilityResult {
  title: string
  project: string
  status: 'PASS' | 'FAIL'
  duration_ms: number
  errors: string[]
}

const results: AccessibilityResult[] = []

function sourceSHA(): string {
  const supplied = process.env.GPT_LOAD_E2E_SOURCE_SHA
  if (supplied) return supplied
  return execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: resolve(process.cwd(), '..'),
    encoding: 'utf8',
  }).trim()
}

function artifactRoot(): string {
  return resolve(process.cwd(), process.env.GPT_LOAD_E2E_ARTIFACT_DIR ?? 'test-results/unscoped')
}

async function preparePage(
  page: Page,
  options: {
    path: string
    scenario: VisualScenario
    width: number
    height: number
    locale: 'en-US' | 'zh-CN'
    theme: 'light' | 'dark'
  },
): Promise<void> {
  await page.setViewportSize({ width: options.width, height: options.height })
  await page.emulateMedia({ colorScheme: options.theme, reducedMotion: 'reduce' })
  await page.addInitScript(
    ({ authKey, locale, theme }) => {
      window.sessionStorage.setItem('gpt-load.auth-key', authKey)
      window.localStorage.setItem('gpt-load.locale', locale)
      window.localStorage.setItem('gpt-load.theme', theme)
    },
    { authKey: visualAuthKey, locale: options.locale, theme: options.theme },
  )
  await installVisualApi(page, options.scenario)
  await page.goto(options.path)
  await expect(page.locator('main h1')).toHaveCount(1)
}

function errors(testInfo: TestInfo): string[] {
  return testInfo.errors.map((error) => error.message?.split('\n')[0] ?? 'unknown error')
}

test.setTimeout(60_000)

test.afterEach(async ({}, testInfo) => {
  results.push({
    title: testInfo.title,
    project: testInfo.project.name,
    status: testInfo.status === testInfo.expectedStatus ? 'PASS' : 'FAIL',
    duration_ms: testInfo.duration,
    errors: errors(testInfo),
  })
})

test.afterAll(async () => {
  const payload = {
    schema_version: 1,
    status:
      results.length > 0 && results.every((result) => result.status === 'PASS') ? 'PASS' : 'FAIL',
    source_sha: sourceSHA(),
    source_dirty: process.env.GPT_LOAD_E2E_SOURCE_SHA ? false : undefined,
    project: results[0]?.project ?? 'unknown',
    scope: [
      'skip-link',
      'single-h1',
      'route-focus-and-announcement',
      'tab-order-and-visible-focus',
      '44x44-pointer-targets',
      'dialog-focus-trap-restore-and-escape',
      'scroll-region-keyboard-access',
      'status-text-and-icon',
      'field-label-description-and-error',
      'copy-live-feedback',
      'reduced-motion',
    ],
    results,
  }
  const root = artifactRoot()
  await mkdir(root, { recursive: true })
  await writeFile(
    resolve(root, 'accessibility-evidence.json'),
    `${JSON.stringify(payload, null, 2)}\n`,
    'utf8',
  )
  const markdown = [
    '# Automated accessibility evidence',
    '',
    `- Status: ${payload.status}`,
    `- Source SHA: \`${payload.source_sha}\``,
    `- Project: \`${payload.project}\``,
    '',
    '| Scenario | Result | Duration |',
    '|---|---:|---:|',
    ...results.map(
      (result) =>
        `| ${result.title.replaceAll('|', '\\|')} | ${result.status} | ${result.duration_ms} ms |`,
    ),
    '',
  ].join('\n')
  await writeFile(resolve(root, 'accessibility-evidence.md'), markdown, 'utf8')
})

const routeCases: Array<{
  id: string
  path: string
  scenario: VisualScenario
  width: number
  height: number
  locale: 'en-US' | 'zh-CN'
  theme: 'light' | 'dark'
}> = [
  {
    id: 'mobile-zh-dark',
    path: '/',
    scenario: 'home-normal',
    width: 375,
    height: 812,
    locale: 'zh-CN',
    theme: 'dark',
  },
  {
    id: 'tablet-en-light',
    path: '/access-keys',
    scenario: 'access-keys-long',
    width: 768,
    height: 900,
    locale: 'en-US',
    theme: 'light',
  },
  {
    id: 'compact-zh-light',
    path: '/settings',
    scenario: 'settings-validation',
    width: 1024,
    height: 900,
    locale: 'zh-CN',
    theme: 'light',
  },
  {
    id: 'desktop-en-dark',
    path: '/monitor?tab=logs',
    scenario: 'logs-signal-path',
    width: 1440,
    height: 900,
    locale: 'en-US',
    theme: 'dark',
  },
]

for (const item of routeCases) {
  test(`single heading and route focus · ${item.id}`, async ({ page }) => {
    await preparePage(page, item)
    const heading = page.locator('main h1')
    await expect(heading).toBeFocused()
    await expect(page.locator('[data-test="route-announcer"]')).toHaveText(
      (await heading.textContent())?.trim() ?? '',
    )
  })
}

test('skip link, tab order, visible focus, route announcement, status, copy, and motion', async ({
  page,
  context,
  browserName,
}) => {
  if (browserName === 'chromium') {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  }
  await preparePage(page, {
    path: '/',
    scenario: 'home-normal',
    width: 1440,
    height: 900,
    locale: 'en-US',
    theme: 'light',
  })

  await expect(page.locator('main h1')).toBeFocused()
  const skipLink = page.getByRole('link', { name: 'Skip to main content' })
  const firstFocusableClass = await page.evaluate(() => {
    const selector =
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), ' +
      'textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    return document.querySelector<HTMLElement>(selector)?.className ?? ''
  })
  expect(firstFocusableClass).toContain('skip-link')
  await skipLink.focus()
  await expect(skipLink).toBeFocused()
  await expect.poll(async () => (await skipLink.boundingBox())?.y ?? -1).toBeGreaterThanOrEqual(0)
  await expect(skipLink).toHaveCSS('outline-width', '2px')
  await page.keyboard.press('Enter')
  await expect(page.locator('#main-content')).toBeFocused()

  const pointerTargets = [
    page.locator('a.brand'),
    page.getByRole('link', { name: 'Import upstream keys', exact: true }),
    page.getByRole('button', { name: 'Preferences' }),
    page.getByRole('button', { name: 'Sign out' }),
  ]
  for (const target of pointerTargets) {
    const box = await target.boundingBox()
    expect(box, (await target.getAttribute('aria-label')) ?? 'pointer target').not.toBeNull()
    expect(box!.width).toBeGreaterThanOrEqual(44)
    expect(box!.height).toBeGreaterThanOrEqual(44)
  }

  const homeStatus = page.locator('[data-test="home-lede"]')
  await expect(homeStatus).toHaveClass(/home-lede--normal/)
  await expect(homeStatus.locator('svg').first()).toHaveAttribute('aria-hidden', 'true')

  const navigation = page.getByRole('navigation', { name: 'Primary navigation' })
  await navigation.getByRole('link', { name: 'Settings' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeFocused()
  const copy = page.locator('[data-test="copy-data-dir"]')
  await expect(copy).toBeVisible()
  await copy.click()
  await expect(page.locator('.copy-control__feedback[role="status"]')).toHaveText(
    browserName === 'chromium' ? 'Copied' : /^(Copied|Copy failed)$/,
  )

  await navigation.getByRole('link', { name: 'Monitor' }).click()
  await expect(page).toHaveURL('/monitor?tab=health')
  const monitorHeading = page.getByRole('heading', { level: 1, name: 'Monitor' })
  await expect(monitorHeading).toBeFocused()
  await expect(page.locator('[data-test="route-announcer"]')).toHaveText('Monitor')
  const motion = await navigation.getByRole('link', { name: 'Monitor' }).evaluate((element) => ({
    animation: getComputedStyle(element).animationDuration,
    transition: getComputedStyle(element).transitionDuration,
  }))
  const durationMilliseconds = (value: string) =>
    value.endsWith('ms') ? Number.parseFloat(value) : Number.parseFloat(value) * 1_000
  expect(durationMilliseconds(motion.animation)).toBeLessThanOrEqual(0.01)
  expect(
    motion.transition.split(',').every((value) => durationMilliseconds(value.trim()) <= 0.01),
  ).toBe(true)
})

test('dialog traps focus, exposes field descriptions, closes on Escape, and restores focus', async ({
  page,
}) => {
  await preparePage(page, {
    path: '/access-keys',
    scenario: 'access-keys-long',
    width: 1440,
    height: 900,
    locale: 'en-US',
    theme: 'light',
  })
  const create = page.getByRole('button', { name: 'Create AccessKey' })
  await create.click()

  const drawer = page.getByRole('dialog', { name: 'Create AccessKey' })
  await expect(drawer).toBeVisible()
  await expect(drawer.getByLabel('Name', { exact: true })).toBeFocused()
  const rpm = drawer.getByLabel('Requests per minute')
  await expect(rpm).toHaveAccessibleDescription(/non-negative whole number.*0 means unlimited/i)

  const close = drawer.getByRole('button', { name: 'Close AccessKey editor' })
  const cancel = drawer.getByRole('button', { name: 'Cancel' })
  for (const target of [close, cancel]) {
    const box = await target.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.width).toBeGreaterThanOrEqual(44)
    expect(box!.height).toBeGreaterThanOrEqual(44)
  }

  await cancel.focus()
  await page.keyboard.press('Tab')
  await expect(drawer.locator(':focus')).toHaveCount(1)
  expect(await drawer.evaluate((element) => element.contains(document.activeElement))).toBe(true)

  await page.keyboard.press('Escape')
  await expect(drawer).toBeHidden()
  await expect(create).toBeFocused()
})

test('scroll regions support keyboard access and validation links focus described errors', async ({
  page,
}) => {
  await preparePage(page, {
    path: '/access-keys',
    scenario: 'access-keys-long',
    width: 768,
    height: 900,
    locale: 'en-US',
    theme: 'dark',
  })
  const scrollRegion = page.locator('[data-table-scroll]')
  await expect(scrollRegion).toHaveAttribute('tabindex', '0')
  await expect(scrollRegion).toHaveAccessibleName(/AccessKey/i)
  await expect(scrollRegion).toHaveAccessibleDescription(/scroll/i)
  await scrollRegion.focus()
  const before = await scrollRegion.evaluate((element) => element.scrollLeft)
  for (let index = 0; index < 5; index += 1) await page.keyboard.press('ArrowRight')
  await expect
    .poll(() => scrollRegion.evaluate((element) => element.scrollLeft))
    .toBeGreaterThan(before)

  await preparePage(page, {
    path: '/settings',
    scenario: 'settings-validation',
    width: 1024,
    height: 900,
    locale: 'en-US',
    theme: 'light',
  })
  await page.locator('[data-test="override-request_timeout"]').check()
  const timeout = page.locator('[data-test="value-request_timeout"]')
  await timeout.fill('0')
  await expect(timeout).toHaveAttribute('aria-invalid', 'true')
  await expect(timeout).toHaveAccessibleDescription(/positive safe integer/i)
  const summary = page.locator('[data-test="settings-validation-summary"]')
  await expect(summary).toHaveAttribute('role', 'alert')
  await summary.locator('[data-test="settings-error-link-request_timeout"]').click()
  await expect(timeout).toBeFocused()
})
