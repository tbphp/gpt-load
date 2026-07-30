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
    fallbackLocale: 'en-US',
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

function localeFamily(value: string): AppLocale | undefined {
  const normalized = value.trim().toLowerCase()
  if (normalized === 'zh' || normalized.startsWith('zh-')) return 'zh-CN'
  if (normalized === 'en' || normalized.startsWith('en-')) return 'en-US'
  if (normalized === 'ja' || normalized.startsWith('ja-')) return 'ja-JP'
  return undefined
}

function resolveLocaleCandidate(value: string): AppLocale | undefined {
  return isSupportedLocale(value) ? value : localeFamily(value)
}

function initialLocale(
  storage: Storage | undefined,
  browserLanguages: readonly string[],
  browserLanguage: string,
): AppLocale {
  let savedLocale: string | null = null
  try {
    savedLocale = storage?.getItem(localeStorageKey) ?? null
  } catch {
    // Storage may be unavailable; the in-memory locale remains authoritative.
  }
  if (isSupportedLocale(savedLocale)) return savedLocale

  for (const candidate of browserLanguages) {
    const resolved = resolveLocaleCandidate(candidate)
    if (resolved) return resolved
  }

  return resolveLocaleCandidate(browserLanguage) ?? 'en-US'
}

function persistLocale(storage: Storage | undefined, locale: AppLocale): void {
  try {
    storage?.setItem(localeStorageKey, locale)
  } catch {
    // Persistence failures do not change the active in-memory preference.
  }
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
  let requestedLocale = locale

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

  async function ensureWithFallback(
    targetLocale: AppLocale,
    namespace: 'core' | MessageNamespace,
  ): Promise<void> {
    const requests = [ensure(targetLocale, namespace)]
    if (targetLocale !== 'en-US') {
      requests.push(ensure('en-US', namespace))
    }
    await Promise.all(requests)
  }

  document.documentElement.lang = locale
  return {
    plugin,
    getLocale() {
      return plugin.global.locale.value as AppLocale
    },
    async setLocale(nextLocale) {
      requestedLocale = nextLocale
      persistLocale(storage, nextLocale)
      await Promise.all([
        ensureWithFallback(nextLocale, 'core'),
        ...activeNamespaces.map((namespace) => ensureWithFallback(nextLocale, namespace)),
      ])
      if (requestedLocale !== nextLocale) return
      plugin.global.locale.value = nextLocale
      document.documentElement.lang = nextLocale
    },
    async loadNamespaces(requested) {
      activeNamespaces = [...new Set(requested)]
      await Promise.all(
        activeNamespaces.map((namespace) =>
          ensureWithFallback(plugin.global.locale.value as AppLocale, namespace),
        ),
      )
    },
    async loadAll() {
      activeNamespaces = namespaces
      await Promise.all(
        namespaces.map((namespace) =>
          ensureWithFallback(plugin.global.locale.value as AppLocale, namespace),
        ),
      )
    },
  }
}

export async function createAppI18n(
  storage?: Storage,
  browserLanguages: readonly string[] = navigator.languages,
  browserLanguage: string = navigator.language,
): Promise<AppI18n> {
  const resolvedStorage = resolveStorage(storage)
  const locale = initialLocale(resolvedStorage, browserLanguages, browserLanguage)
  persistLocale(resolvedStorage, locale)

  const localesToLoad = locale === 'en-US' ? (['en-US'] as const) : ([locale, 'en-US'] as const)
  const entries = await Promise.all(
    localesToLoad.map(
      async (candidate) => [candidate, (await coreLoaders[candidate]()).default] as const,
    ),
  )
  const messages = Object.fromEntries(entries) as Partial<Record<AppLocale, MessageTree>>
  const loaded = new Set(entries.map(([candidate]) => `${candidate}:core`))
  return createController(locale, messages, loaded, resolvedStorage)
}
