import { expect, test as base } from '@playwright/test'

const deterministicInitScript = String.raw`
(() => {
  const fixedNow = Date.parse('2026-07-28T00:00:00.000Z')
  const NativeDate = Date
  class DeterministicDate extends NativeDate {
    constructor(...args) {
      super(...(args.length === 0 ? [fixedNow] : args))
    }
    static now() {
      return fixedNow
    }
  }
  Object.defineProperty(globalThis, 'Date', { configurable: true, value: DeterministicDate })

  const nextSequence = (key) => {
    let current = 0
    try {
      current = Number.parseInt(sessionStorage.getItem(key) || '0', 10) || 0
      sessionStorage.setItem(key, String(current + 1))
    } catch {
      current += 1
    }
    return current + 1
  }
  Object.defineProperty(globalThis.crypto, 'randomUUID', {
    configurable: true,
    value: () => {
      const suffix = String(nextSequence('gpt-load.e2e.uuid')).padStart(12, '0')
      return '00000000-0000-4000-8000-' + suffix
    },
  })

  const nativeFetch = globalThis.fetch.bind(globalThis)
  globalThis.fetch = (input, init = {}) => {
    const headers = new Headers(
      init.headers || (input instanceof Request ? input.headers : undefined),
    )
    if (!headers.has('X-Request-ID')) {
      const suffix = String(nextSequence('gpt-load.e2e.request')).padStart(12, '0')
      headers.set('X-Request-ID', '00000000-0000-4000-8000-' + suffix)
    }
    return nativeFetch(input, { ...init, headers })
  }

  try {
    if (!localStorage.getItem('gpt-load.locale')) localStorage.setItem('gpt-load.locale', 'en-US')
    if (!localStorage.getItem('gpt-load.theme')) localStorage.setItem('gpt-load.theme', 'light')
  } catch {}

})()
`

export const test = base.extend<{ deterministicHarness: void }>({
  deterministicHarness: [
    async ({ page }, use) => {
      await page.route(/\.css(?:\?|$)/, async (route) => {
        const response = await route.fetch()
        const source = await response.text()
        await route.fulfill({
          response,
          body:
            `${source}\n` +
            '*,*::before,*::after{animation-duration:0s!important;animation-delay:0s!important;' +
            'transition-duration:0s!important;transition-delay:0s!important;' +
            'caret-color:transparent!important}',
          headers: {
            ...response.headers(),
            'content-type': 'text/css; charset=utf-8',
          },
        })
      })
      await page.addInitScript({ content: deterministicInitScript })
      await use()
    },
    { auto: true },
  ],
})

export { expect }
