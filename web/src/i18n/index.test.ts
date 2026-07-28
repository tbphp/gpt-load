import { testMessages } from '@/test/i18n'

import { createAppI18n } from './index'

function dictionaryKeys(value: Record<string, unknown>, prefix = ''): string[] {
  return Object.entries(value).flatMap(([key, child]) => {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof child === 'object' && child !== null) {
      return dictionaryKeys(child as Record<string, unknown>, path)
    }
    return [path]
  })
}

describe('createAppI18n', () => {
  it('prefers a supported saved locale', async () => {
    window.localStorage.setItem('gpt-load.locale', 'en-US')

    const appI18n = await createAppI18n(window.localStorage, 'ja-JP')

    expect(appI18n.getLocale()).toBe('en-US')
    expect(document.documentElement.lang).toBe('en-US')
  })

  it.each([
    ['zh-Hant-TW', 'zh-CN'],
    ['EN-gb', 'en-US'],
    ['ja', 'ja-JP'],
  ] as const)('normalizes navigator locale %s to %s', async (browserLanguage, expected) => {
    const appI18n = await createAppI18n(window.localStorage, browserLanguage)
    expect(appI18n.getLocale()).toBe(expected)
  })

  it('falls back to zh-CN', async () => {
    window.localStorage.setItem('gpt-load.locale', 'en-us')
    const appI18n = await createAppI18n(window.localStorage, 'fr-FR')
    expect(appI18n.getLocale()).toBe('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
  })

  it('persists locale and updates document language after loading active namespaces', async () => {
    const appI18n = await createAppI18n(window.localStorage, 'zh-CN')
    await appI18n.loadNamespaces(['monitor'])

    await appI18n.setLocale('ja-JP')

    expect(appI18n.getLocale()).toBe('ja-JP')
    expect(window.localStorage.getItem('gpt-load.locale')).toBe('ja-JP')
    expect(document.documentElement.lang).toBe('ja-JP')
    expect(appI18n.plugin.global.te('monitor.title', 'ja-JP')).toBe(true)
    expect(appI18n.plugin.global.te('settings.title', 'ja-JP')).toBe(false)
  })

  it('keeps the in-memory and document locale when storage fails', async () => {
    const failingStorage = {
      getItem() {
        throw new Error('storage unavailable')
      },
      setItem() {
        throw new Error('storage unavailable')
      },
    } as unknown as Storage
    const appI18n = await createAppI18n(failingStorage, 'en-GB')

    await expect(appI18n.setLocale('ja-JP')).resolves.toBeUndefined()
    expect(appI18n.getLocale()).toBe('ja-JP')
    expect(document.documentElement.lang).toBe('ja-JP')
  })

  it('uses memory when the default localStorage getter throws', async () => {
    const localStorageGetter = vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw new DOMException('storage unavailable', 'SecurityError')
    })
    try {
      const appI18n = await createAppI18n(undefined, 'ja-JP')
      expect(appI18n.getLocale()).toBe('ja-JP')
      await expect(appI18n.setLocale('en-US')).resolves.toBeUndefined()
      expect(appI18n.getLocale()).toBe('en-US')
      expect(document.documentElement.lang).toBe('en-US')
    } finally {
      localStorageGetter.mockRestore()
    }
  })

  it('loads only core/Home/Login initially and feature messages by namespace', async () => {
    const appI18n = await createAppI18n(window.localStorage, 'en-US')
    const initial = appI18n.plugin.global.getLocaleMessage('en-US')

    expect(Object.keys(initial)).toEqual(['common', 'auth', 'shell', 'notFound', 'home'])
    expect(initial).not.toHaveProperty('monitor')

    await appI18n.loadNamespaces(['monitor'])

    expect(appI18n.plugin.global.getLocaleMessage('en-US')).toHaveProperty('monitor')
    expect(appI18n.plugin.global.getLocaleMessage('en-US')).not.toHaveProperty('settings')
  })

  it('keeps every split locale dictionary structurally complete', () => {
    const zhCN = testMessages['zh-CN'] as Record<string, unknown>
    const enUS = testMessages['en-US'] as Record<string, unknown>
    const jaJP = testMessages['ja-JP'] as Record<string, unknown>
    const expectedKeys = dictionaryKeys(zhCN)

    expect(dictionaryKeys(enUS)).toEqual(expectedKeys)
    expect(dictionaryKeys(jaJP)).toEqual(expectedKeys)
    expect(dictionaryKeys(zhCN.modelPrices as Record<string, unknown>).length).toBeGreaterThan(20)
    expect(dictionaryKeys(zhCN.monitor as Record<string, unknown>).length).toBeGreaterThan(100)
  })

  it('keeps security and pricing policy copy in all three locales', () => {
    const locales = [testMessages['zh-CN'], testMessages['en-US'], testMessages['ja-JP']]
    for (const locale of locales) {
      expect(locale.import.headerRules.storageNotice).not.toHaveLength(0)
      expect(locale.modelPrices.builtin.longContext.label).not.toHaveLength(0)
      expect(locale.modelPrices.builtin.longContext.summary).not.toHaveLength(0)
    }
  })
})
