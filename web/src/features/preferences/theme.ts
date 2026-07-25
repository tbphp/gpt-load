import { inject, readonly, ref, type InjectionKey, type Ref } from 'vue'

export type AppTheme = 'system' | 'light' | 'dark'

export interface ThemeController {
  readonly theme: Readonly<Ref<AppTheme>>
  setTheme(theme: AppTheme): void
  dispose(): void
}

export interface ThemeControllerDependencies {
  documentElement: HTMLElement
  storage?: Storage
  matchMedia(query: string): MediaQueryList
}

const themeStorageKey = 'gpt-load.theme'

function isTheme(value: unknown): value is AppTheme {
  return value === 'system' || value === 'light' || value === 'dark'
}

export function createThemeController(deps: ThemeControllerDependencies): ThemeController {
  let initial: AppTheme = 'system'
  try {
    const stored = deps.storage?.getItem(themeStorageKey)
    if (isTheme(stored)) initial = stored
  } catch {
    // Browser preferences remain available in memory when storage is denied.
  }

  const theme = ref<AppTheme>(initial)
  let media: MediaQueryList | undefined
  let mediaListener: ((event: MediaQueryListEvent) => void) | undefined

  function apply(next: AppTheme): void {
    if (next === 'system') {
      deps.documentElement.removeAttribute('data-theme')
    } else {
      deps.documentElement.dataset.theme = next
    }
  }

  try {
    media = deps.matchMedia('(prefers-color-scheme: dark)')
    mediaListener = () => {
      if (theme.value === 'system') apply('system')
    }
    media.addEventListener('change', mediaListener)
  } catch {
    media = undefined
    mediaListener = undefined
  }

  apply(initial)

  return {
    theme: readonly(theme),
    setTheme(next) {
      theme.value = next
      apply(next)
      try {
        deps.storage?.setItem(themeStorageKey, next)
      } catch {
        // Persistence failure does not change the active in-memory preference.
      }
    },
    dispose() {
      if (media && mediaListener) media.removeEventListener('change', mediaListener)
    },
  }
}

export const themeControllerKey: InjectionKey<ThemeController> = Symbol('theme-controller')

export function useTheme(): ThemeController {
  const controller = inject(themeControllerKey)
  if (!controller) throw new Error('THEME_CONTROLLER_NOT_PROVIDED')
  return controller
}
