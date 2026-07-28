import { expect, test as base } from '@playwright/test'

import { deterministicUUIDPrefix } from './deterministic-ids'
import { visualClock } from './visual-fixtures'

function deterministicInitScript(uuidPrefix: string): string {
  return String.raw`
(() => {
  const uuidPrefix = ${JSON.stringify(uuidPrefix)}
  const fixedNow = Date.parse(${JSON.stringify(visualClock)})
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
      return uuidPrefix + suffix
    },
  })

  const nativeFetch = globalThis.fetch.bind(globalThis)
  globalThis.fetch = (input, init = {}) => {
    const headers = new Headers(
      init.headers || (input instanceof Request ? input.headers : undefined),
    )
    if (!headers.has('X-Request-ID')) {
      const suffix = String(nextSequence('gpt-load.e2e.request')).padStart(12, '0')
      headers.set('X-Request-ID', uuidPrefix + suffix)
    }
    return nativeFetch(input, { ...init, headers })
  }

  try {
    if (!localStorage.getItem('gpt-load.locale')) localStorage.setItem('gpt-load.locale', 'en-US')
    if (!localStorage.getItem('gpt-load.theme')) localStorage.setItem('gpt-load.theme', 'light')
  } catch {}

})()
`
}

export const test = base.extend<{ deterministicHarness: void }>({
  deterministicHarness: [
    async ({ page }, use, testInfo) => {
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
      await page.addInitScript({
        content: deterministicInitScript(
          deterministicUUIDPrefix({
            parallelIndex: testInfo.parallelIndex,
            repeatEachIndex: testInfo.repeatEachIndex,
            retry: testInfo.retry,
            testId: testInfo.testId,
          }),
        ),
      })
      await use()
    },
    { auto: true },
  ],
})

export { expect }
