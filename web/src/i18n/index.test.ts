import enUS from './locales/en-US'
import jaJP from './locales/ja-JP'
import zhCN from './locales/zh-CN'
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
  it('prefers a supported saved locale', () => {
    window.localStorage.setItem('gpt-load.locale', 'en-US')

    const appI18n = createAppI18n(window.localStorage, 'ja-JP')

    expect(appI18n.getLocale()).toBe('en-US')
    expect(document.documentElement.lang).toBe('en-US')
  })

  it.each([
    ['zh-Hant-TW', 'zh-CN'],
    ['EN-gb', 'en-US'],
    ['ja', 'ja-JP'],
  ] as const)('normalizes navigator locale %s to %s', (browserLanguage, expected) => {
    const appI18n = createAppI18n(window.localStorage, browserLanguage)

    expect(appI18n.getLocale()).toBe(expected)
  })

  it('falls back to zh-CN', () => {
    window.localStorage.setItem('gpt-load.locale', 'en-us')

    const appI18n = createAppI18n(window.localStorage, 'fr-FR')

    expect(appI18n.getLocale()).toBe('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
  })

  it('persists locale and updates document language', () => {
    const appI18n = createAppI18n(window.localStorage, 'zh-CN')

    appI18n.setLocale('ja-JP')

    expect(appI18n.getLocale()).toBe('ja-JP')
    expect(window.localStorage.getItem('gpt-load.locale')).toBe('ja-JP')
    expect(document.documentElement.lang).toBe('ja-JP')
  })

  it('keeps the in-memory and document locale when storage fails', () => {
    const failingStorage = {
      getItem() {
        throw new Error('storage unavailable')
      },
      setItem() {
        throw new Error('storage unavailable')
      },
    } as unknown as Storage

    const appI18n = createAppI18n(failingStorage, 'en-GB')

    expect(appI18n.getLocale()).toBe('en-US')
    expect(() => appI18n.setLocale('ja-JP')).not.toThrow()
    expect(appI18n.getLocale()).toBe('ja-JP')
    expect(document.documentElement.lang).toBe('ja-JP')
  })

  it('uses memory when the default localStorage getter throws', () => {
    const localStorageGetter = vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw new DOMException('storage unavailable', 'SecurityError')
    })

    try {
      const appI18n = createAppI18n(undefined, 'ja-JP')

      expect(appI18n.getLocale()).toBe('ja-JP')
      expect(document.documentElement.lang).toBe('ja-JP')
      expect(() => appI18n.setLocale('en-US')).not.toThrow()
      expect(appI18n.getLocale()).toBe('en-US')
      expect(document.documentElement.lang).toBe('en-US')
    } finally {
      localStorageGetter.mockRestore()
    }
  })

  it('keeps all three locale dictionaries structurally complete', () => {
    const expectedKeys = dictionaryKeys(zhCN)

    expect(dictionaryKeys(enUS)).toEqual(expectedKeys)
    expect(dictionaryKeys(jaJP)).toEqual(expectedKeys)
    expect(Object.values(zhCN.auth).every((message) => message.length > 0)).toBe(true)
    expect(Object.values(enUS.auth).every((message) => message.length > 0)).toBe(true)
    expect(Object.values(jaJP.auth).every((message) => message.length > 0)).toBe(true)
  })
})
