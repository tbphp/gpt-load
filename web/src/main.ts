import { VueQueryPlugin } from '@tanstack/vue-query'
import { createApp } from 'vue'
import type { Router } from 'vue-router'

import App from './App.vue'
import { createApiClient } from './api/client'
import { apiClientKey } from './api/client-context'
import type { AuthSessionPayload } from './api/types'
import { createAppQueryClient } from './app/query'
import { createAppRouter } from './app/router'
import { handleGlobalUnauthorized } from './app/unauthorized'
import { authSessionKey, createAuthSession, type AuthSession } from './features/auth/auth-session'
import { createImportRecoveryService, importRecoveryKey } from './features/import/import-recovery'
import {
  createDirtyNavigationController,
  dirtyNavigationKey,
} from './features/import/use-dirty-navigation'
import { createBrowserThemeController, themeControllerKey } from './features/preferences/theme'
import { createAppI18n } from './i18n'
import { appI18nKey } from './i18n/context'
import './styles/tokens.css'
import './styles/base.css'

const queryClient = createAppQueryClient()
const getBrowserStorage = (name: 'localStorage' | 'sessionStorage') => {
  try {
    return window[name]
  } catch {
    return undefined
  }
}
const appI18n = createAppI18n(getBrowserStorage('localStorage'), navigator.language)
const importRecovery = createImportRecoveryService({
  storage: getBrowserStorage('sessionStorage'),
  now: Date.now,
  setTimer: window.setTimeout.bind(window),
  clearTimer: window.clearTimeout.bind(window),
})
const dirtyNavigation = createDirtyNavigationController()
importRecovery.sweep()
const themeController = createBrowserThemeController(
  window,
  document.documentElement,
  getBrowserStorage('localStorage'),
)
window.addEventListener(
  'pagehide',
  () => {
    themeController.dispose()
    importRecovery.dispose()
  },
  { once: true },
)

let authSession: AuthSession | undefined = undefined
let router: Router | undefined = undefined

const apiClient = createApiClient({
  fetch: window.fetch.bind(window),
  getAuthKey: () => authSession?.getAuthKey() ?? '',
  getLocale: () => appI18n.getLocale(),
  onUnauthorized: () => {
    const redirect =
      router?.currentRoute.value.meta.requiresAuth === true
        ? router.currentRoute.value.fullPath
        : '/'
    if (authSession && router) {
      void handleGlobalUnauthorized({
        recovery: importRecovery,
        dirtyNavigation,
        session: authSession,
        router,
        redirect,
      })
    }
  },
})

authSession = createAuthSession({
  storage: getBrowserStorage('sessionStorage'),
  queryClient,
  validate: (key, globalUnauthorized, signal) =>
    apiClient.request<AuthSessionPayload>('/api/auth/session', {
      authKey: key,
      handleUnauthorized: globalUnauthorized,
      signal,
    }),
})
router = createAppRouter(authSession)

createApp(App)
  .provide(authSessionKey, authSession)
  .provide(importRecoveryKey, importRecovery)
  .provide(dirtyNavigationKey, dirtyNavigation)
  .provide(apiClientKey, apiClient)
  .provide(appI18nKey, appI18n)
  .provide(themeControllerKey, themeController)
  .use(appI18n.plugin)
  .use(VueQueryPlugin, { queryClient })
  .use(router)
  .mount('#app')
