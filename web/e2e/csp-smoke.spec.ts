import { readdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'

const credentialCanary = 'csp-auth-key'

async function artifactFiles(directory: string): Promise<string[]> {
  try {
    const entries = await readdir(directory, { withFileTypes: true })
    const nested = await Promise.all(
      entries.map((entry) => {
        const path = join(directory, entry.name)
        return entry.isDirectory() ? artifactFiles(path) : [path]
      }),
    )
    return nested.flat()
  } catch {
    return []
  }
}

test('Reka overlays work under production CSP', async ({ page }, testInfo) => {
  expect(testInfo.project.use).toMatchObject({
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  })
  const errors: string[] = []
  page.on('console', (message) => message.type() === 'error' && errors.push(message.text()))
  page.on('pageerror', (error) => errors.push(error.message))
  await page.addInitScript(() => {
    localStorage.setItem('gpt-load.locale', 'en-US')
    const violations: string[] = []
    Object.defineProperty(window, '__cspViolations', { value: violations })
    document.addEventListener('securitypolicyviolation', (event) => {
      violations.push(
        `${event.violatedDirective}:${event.blockedURI}:${event.sourceFile}:${event.lineNumber}:${event.sample}`,
      )
    })
  })
  await page.goto('/login')
  await page.getByLabel('AUTH_KEY', { exact: true }).fill(credentialCanary)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.setViewportSize({ width: 375, height: 812 })
  const navigationTrigger = page.getByRole('button', {
    name: 'Open navigation',
  })
  await navigationTrigger.click()
  await page.getByRole('button', { name: 'Close navigation' }).click()
  await expect(navigationTrigger).toBeFocused()
  await page.getByLabel('Language').click()
  await page.getByRole('option', { name: 'English' }).click()
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  const cspViolations = await page.evaluate(
    () => (window as Window & { __cspViolations?: string[] }).__cspViolations ?? [],
  )
  expect(cspViolations).toEqual([])
  expect(errors).toEqual([])

  const recordings = testInfo.attachments.filter(({ name }) => /trace|screenshot|video/i.test(name))
  expect(recordings).toEqual([])
  for (const path of await artifactFiles(testInfo.outputDir)) {
    expect((await readFile(path)).includes(Buffer.from(credentialCanary))).toBe(false)
  }
})

test('compact navigation remains safe at 1024px in every locale', async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 })
  await page.goto('/login')
  await page.getByLabel('AUTH_KEY', { exact: true }).fill(credentialCanary)
  await page.locator('form button[type="submit"]').click()
  await expect(page).toHaveURL('/')

  const locales = [
    { locale: 'zh-CN', open: '打开导航', close: '关闭导航' },
    { locale: 'en-US', open: 'Open navigation', close: 'Close navigation' },
    { locale: 'ja-JP', open: 'ナビゲーションを開く', close: 'ナビゲーションを閉じる' },
  ] as const

  for (const { locale, open, close } of locales) {
    await page.evaluate((nextLocale) => localStorage.setItem('gpt-load.locale', nextLocale), locale)
    await page.reload()

    const navigationTrigger = page.getByRole('button', { name: open })
    await expect(navigationTrigger).toBeVisible()
    const layout = await page.evaluate(() => ({
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth,
    }))
    expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth)

    const undersizedControls = await page
      .locator('.app-topbar a, .app-topbar button')
      .evaluateAll((controls) =>
        controls
          .map((control) => {
            const bounds = control.getBoundingClientRect()
            return {
              label: control.getAttribute('aria-label') ?? control.textContent,
              ...bounds.toJSON(),
            }
          })
          .filter(({ width, height }) => width > 0 && height > 0)
          .filter(({ width, height }) => width < 44 || height < 44),
      )
    expect(undersizedControls).toEqual([])

    await navigationTrigger.click()
    await expect(page.getByRole('button', { name: close })).toBeVisible()
    await page.getByRole('button', { name: close }).click()
  }
})
