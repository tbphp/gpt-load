import { expect, test } from '@playwright/test'

test('Reka overlays work under production CSP', async ({ page }) => {
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
  await page.getByLabel('AUTH_KEY', { exact: true }).fill('csp-auth-key')
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
})
