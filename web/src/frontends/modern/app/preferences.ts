import { computed, inject, readonly, ref, type InjectionKey } from 'vue'

import type { AppLocale } from '@shared/preferences/locale'

export const themes = ['system', 'light', 'dark'] as const
export type Theme = (typeof themes)[number]

function readPreference(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

export function createPreferences(
  initialLocale: AppLocale,
  applyLocale: (locale: AppLocale) => void,
) {
  const storedTheme = readPreference('gpt-load.theme')
  const theme = ref<Theme>(
    themes.includes(storedTheme as Theme) ? (storedTheme as Theme) : 'system',
  )
  const systemScheme = window.matchMedia('(prefers-color-scheme: dark)')
  const systemDark = ref(systemScheme.matches)
  const resolvedTheme = computed<'light' | 'dark'>(() =>
    theme.value === 'system' ? (systemDark.value ? 'dark' : 'light') : theme.value,
  )
  function updateSystemTheme(event: MediaQueryListEvent): void {
    systemDark.value = event.matches
  }
  systemScheme.addEventListener('change', updateSystemTheme)
  const locale = ref(initialLocale)
  const sidebarCollapsed = ref(readPreference('gpt-load.modern.sidebar-collapsed') === 'true')
  const persistenceFailed = ref(false)

  function persist(key: string, value: string): void {
    try {
      window.localStorage.setItem(key, value)
      if (window.localStorage.getItem(key) !== value) persistenceFailed.value = true
    } catch {
      // 存储被禁用时，本次访问仍可使用所选界面偏好。
      persistenceFailed.value = true
    }
  }

  function applyTheme(value: Theme): void {
    if (value === 'system') document.documentElement.removeAttribute('data-theme')
    else document.documentElement.dataset.theme = value
  }

  applyTheme(theme.value)

  return {
    theme: readonly(theme),
    resolvedTheme,
    locale: readonly(locale),
    sidebarCollapsed: readonly(sidebarCollapsed),
    persistenceFailed: readonly(persistenceFailed),
    dispose() {
      systemScheme.removeEventListener('change', updateSystemTheme)
    },
    setTheme(value: Theme) {
      theme.value = value
      applyTheme(value)
      persist('gpt-load.theme', value)
    },
    setLocale(value: AppLocale) {
      locale.value = value
      applyLocale(value)
      document.documentElement.lang = value
      persist('gpt-load.locale', value)
    },
    toggleSidebar() {
      sidebarCollapsed.value = !sidebarCollapsed.value
      persist('gpt-load.modern.sidebar-collapsed', String(sidebarCollapsed.value))
    },
  }
}

const preferencesKey: InjectionKey<ReturnType<typeof createPreferences>> =
  Symbol('modern-preferences')
export { preferencesKey }

export function usePreferences() {
  const preferences = inject(preferencesKey)
  if (!preferences) throw new Error('MODERN_PREFERENCES_NOT_PROVIDED')
  return preferences
}
