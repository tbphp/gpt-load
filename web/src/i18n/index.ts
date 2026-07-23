import { createI18n } from 'vue-i18n'

import enUS from './locales/en-US'
import jaJP from './locales/ja-JP'
import zhCN from './locales/zh-CN'

export const supportedLocales = ['zh-CN', 'en-US', 'ja-JP'] as const
export type AppLocale = (typeof supportedLocales)[number]

const localeStorageKey = 'gpt-load.locale'

function createI18nPlugin(initialLocale: AppLocale) {
  return createI18n({
    legacy: false,
    locale: initialLocale,
    fallbackLocale: 'zh-CN',
    messages: {
      'zh-CN': zhCN,
      'en-US': enUS,
      'ja-JP': jaJP,
    },
  })
}

export interface AppI18n {
  plugin: ReturnType<typeof createI18nPlugin>
  getLocale(): AppLocale
  setLocale(locale: AppLocale): void
}

export function normalizeLocale(value?: string | null): AppLocale {
  const normalized = value?.trim().toLowerCase()
  if (normalized?.startsWith('ja')) return 'ja-JP'
  if (normalized?.startsWith('en')) return 'en-US'
  if (normalized?.startsWith('zh')) return 'zh-CN'
  return 'zh-CN'
}

function isSupportedLocale(value: string | null): value is AppLocale {
  return supportedLocales.includes(value as AppLocale)
}

export function createAppI18n(
  storage?: Storage,
  browserLanguage: string = navigator.language,
): AppI18n {
  let resolvedStorage = storage
  if (resolvedStorage === undefined) {
    try {
      resolvedStorage = window.localStorage
    } catch {
      // Access to the default storage itself may be denied.
    }
  }

  let savedLocale: string | null = null
  try {
    savedLocale = resolvedStorage?.getItem(localeStorageKey) ?? null
  } catch {
    // Storage may be unavailable; the in-memory locale remains authoritative.
  }

  const initialLocale = isSupportedLocale(savedLocale)
    ? savedLocale
    : normalizeLocale(browserLanguage)
  const plugin = createI18nPlugin(initialLocale)

  document.documentElement.lang = initialLocale

  return {
    plugin,
    getLocale() {
      return plugin.global.locale.value
    },
    setLocale(locale) {
      plugin.global.locale.value = locale
      document.documentElement.lang = locale
      try {
        resolvedStorage?.setItem(localeStorageKey, locale)
      } catch {
        // Persistence failures must not desynchronize the active locale.
      }
    },
  }
}
