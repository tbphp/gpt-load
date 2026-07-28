import { createI18n } from 'vue-i18n'

export const supportedLocales = ['zh-CN', 'en-US', 'ja-JP'] as const
export type AppLocale = (typeof supportedLocales)[number]
export type MessageNamespace =
  'import' | 'group' | 'access-keys' | 'monitor' | 'model-prices' | 'settings'

type MessageTree = { [key: string]: string | MessageTree }
type MessageLoader = () => Promise<{ default: MessageTree }>

const localeStorageKey = 'gpt-load.locale'
const namespaces: MessageNamespace[] = [
  'import',
  'group',
  'access-keys',
  'monitor',
  'model-prices',
  'settings',
]
const coreLoaders: Record<AppLocale, MessageLoader> = {
  'zh-CN': () => import('./locales/zh-CN/core'),
  'en-US': () => import('./locales/en-US/core'),
  'ja-JP': () => import('./locales/ja-JP/core'),
}
const namespaceLoaders: Record<MessageNamespace, Record<AppLocale, MessageLoader>> = {
  import: {
    'zh-CN': () => import('./locales/zh-CN/import'),
    'en-US': () => import('./locales/en-US/import'),
    'ja-JP': () => import('./locales/ja-JP/import'),
  },
  group: {
    'zh-CN': () => import('./locales/zh-CN/group'),
    'en-US': () => import('./locales/en-US/group'),
    'ja-JP': () => import('./locales/ja-JP/group'),
  },
  'access-keys': {
    'zh-CN': () => import('./locales/zh-CN/access-keys'),
    'en-US': () => import('./locales/en-US/access-keys'),
    'ja-JP': () => import('./locales/ja-JP/access-keys'),
  },
  monitor: {
    'zh-CN': () => import('./locales/zh-CN/monitor'),
    'en-US': () => import('./locales/en-US/monitor'),
    'ja-JP': () => import('./locales/ja-JP/monitor'),
  },
  'model-prices': {
    'zh-CN': () => import('./locales/zh-CN/model-prices'),
    'en-US': () => import('./locales/en-US/model-prices'),
    'ja-JP': () => import('./locales/ja-JP/model-prices'),
  },
  settings: {
    'zh-CN': () => import('./locales/zh-CN/settings'),
    'en-US': () => import('./locales/en-US/settings'),
    'ja-JP': () => import('./locales/ja-JP/settings'),
  },
}

function createI18nPlugin(
  initialLocale: AppLocale,
  messages: Partial<Record<AppLocale, MessageTree>>,
) {
  const loadedMessages = Object.fromEntries(
    Object.entries(messages).filter((entry): entry is [AppLocale, MessageTree] =>
      Boolean(entry[1]),
    ),
  )
  return createI18n({
    legacy: false as const,
    locale: initialLocale,
    fallbackLocale: initialLocale,
    messages: loadedMessages,
  })
}

export interface AppI18n {
  plugin: ReturnType<typeof createI18nPlugin>
  getLocale(): AppLocale
  setLocale(locale: AppLocale): Promise<void>
  loadNamespaces(requested: readonly MessageNamespace[]): Promise<void>
  loadAll(): Promise<void>
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

function resolveStorage(storage?: Storage): Storage | undefined {
  if (storage !== undefined) return storage
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

function initialLocale(storage: Storage | undefined, browserLanguage: string): AppLocale {
  let savedLocale: string | null = null
  try {
    savedLocale = storage?.getItem(localeStorageKey) ?? null
  } catch {
    // Storage may be unavailable; the in-memory locale remains authoritative.
  }
  return isSupportedLocale(savedLocale) ? savedLocale : normalizeLocale(browserLanguage)
}

function createController(
  locale: AppLocale,
  messages: Partial<Record<AppLocale, MessageTree>>,
  loaded: Set<string>,
  storage?: Storage,
): AppI18n {
  const plugin = createI18nPlugin(locale, messages)
  const pending = new Map<string, Promise<void>>()
  let activeNamespaces: readonly MessageNamespace[] = []

  async function ensure(
    targetLocale: AppLocale,
    namespace: 'core' | MessageNamespace,
  ): Promise<void> {
    const identity = `${targetLocale}:${namespace}`
    if (loaded.has(identity)) return
    const existing = pending.get(identity)
    if (existing) return existing
    const request = (async () => {
      const loader =
        namespace === 'core' ? coreLoaders[targetLocale] : namespaceLoaders[namespace][targetLocale]
      const module = await loader()
      plugin.global.mergeLocaleMessage(targetLocale, module.default)
      loaded.add(identity)
    })().finally(() => pending.delete(identity))
    pending.set(identity, request)
    return request
  }

  document.documentElement.lang = locale
  return {
    plugin,
    getLocale() {
      return plugin.global.locale.value as AppLocale
    },
    async setLocale(nextLocale) {
      await Promise.all([
        ensure(nextLocale, 'core'),
        ...activeNamespaces.map((namespace) => ensure(nextLocale, namespace)),
      ])
      plugin.global.locale.value = nextLocale
      plugin.global.fallbackLocale.value = nextLocale
      document.documentElement.lang = nextLocale
      try {
        storage?.setItem(localeStorageKey, nextLocale)
      } catch {
        // Persistence failures must not desynchronize the active locale.
      }
    },
    async loadNamespaces(requested) {
      activeNamespaces = [...new Set(requested)]
      await Promise.all(
        activeNamespaces.map((namespace) =>
          ensure(plugin.global.locale.value as AppLocale, namespace),
        ),
      )
    },
    async loadAll() {
      activeNamespaces = namespaces
      await Promise.all(
        namespaces.map((namespace) => ensure(plugin.global.locale.value as AppLocale, namespace)),
      )
    },
  }
}

export async function createAppI18n(
  storage?: Storage,
  browserLanguage: string = navigator.language,
): Promise<AppI18n> {
  const resolvedStorage = resolveStorage(storage)
  const locale = initialLocale(resolvedStorage, browserLanguage)
  const core = (await coreLoaders[locale]()).default
  return createController(locale, { [locale]: core }, new Set([`${locale}:core`]), resolvedStorage)
}

export function createAppI18nForTesting(
  messages: Record<AppLocale, MessageTree>,
  locale: AppLocale,
  storage?: Storage,
): AppI18n {
  const loaded = new Set<string>()
  for (const supportedLocale of supportedLocales) {
    loaded.add(`${supportedLocale}:core`)
    for (const namespace of namespaces) loaded.add(`${supportedLocale}:${namespace}`)
  }
  return createController(locale, messages, loaded, storage)
}
