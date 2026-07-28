import { expect, test } from './fixtures'

const credentialCanary = process.env.GPT_LOAD_E2E_AUTH_KEY
if (!credentialCanary) throw new Error('E2E harness environment is incomplete')

test('Reka overlays work under production CSP', async ({ page }, testInfo) => {
  expect(testInfo.project.use).toMatchObject({
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  })
  const errors: string[] = []
  const unauthorizedResponses: string[] = []
  page.on('console', (message) => message.type() === 'error' && errors.push(message.text()))
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('response', (response) => {
    if (response.status() === 401) unauthorizedResponses.push(response.url())
  })
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
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: 'Close navigation' })).toBeHidden()
  await expect(navigationTrigger).toBeFocused()
  await navigationTrigger.click()
  await page.getByRole('button', { name: 'Close navigation' }).click()
  await expect(navigationTrigger).toBeFocused()
  await page.goto('/access-keys')
  const revealButton = page.getByRole('button', { name: 'Reveal key' }).first()
  const revealBounds = await revealButton.boundingBox()
  expect(revealBounds).not.toBeNull()
  expect(revealBounds!.width).toBeGreaterThanOrEqual(44)
  expect(revealBounds!.height).toBeGreaterThanOrEqual(44)
  const preferencesTrigger = page.getByRole('button', { name: 'Preferences' })
  await preferencesTrigger.click()
  await expect(page.locator('[data-test="preferences-panel"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(preferencesTrigger).toBeFocused()
  await preferencesTrigger.click()
  await page.getByLabel('English', { exact: true }).check()
  await page.getByLabel('Dark', { exact: true }).check()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  const cspViolations = await page.evaluate(
    () => (window as Window & { __cspViolations?: string[] }).__cspViolations ?? [],
  )
  expect(cspViolations).toEqual([])
  expect(unauthorizedResponses).toEqual([])
  expect(errors).toEqual([])

  const recordings = testInfo.attachments.filter(({ name }) => /trace|screenshot|video/i.test(name))
  expect(recordings).toEqual([])
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
