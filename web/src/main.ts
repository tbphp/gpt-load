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
import { clearEphemeralState } from './app/ephemeral-state'
import { authSessionKey, createAuthSession, type AuthSession } from './features/auth/auth-session'
import { createImportRecoveryService, importRecoveryKey } from './features/import/import-recovery'
import { createUnsavedChangesController, unsavedChangesKey } from './app/unsaved-changes'
import { createBrowserThemeController, themeControllerKey } from './features/preferences/theme'
import { createAppI18n } from './i18n'
import { appI18nKey } from './i18n/context'
import './styles/tokens.css'
import './styles/base.css'

async function bootstrap(): Promise<void> {
  const queryClient = createAppQueryClient()
  const getBrowserStorage = (name: 'localStorage' | 'sessionStorage') => {
    try {
      return window[name]
    } catch {
      return undefined
    }
  }
  const appI18n = await createAppI18n(
    getBrowserStorage('localStorage'),
    navigator.languages,
    navigator.language,
  )
  const importRecovery = createImportRecoveryService({
    storage: getBrowserStorage('sessionStorage'),
    now: Date.now,
    setTimer: window.setTimeout.bind(window),
    clearTimer: window.clearTimeout.bind(window),
  })
  const unsavedChanges = createUnsavedChangesController()
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
      clearEphemeralState()
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
          unsavedChanges,
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
    onClear: clearEphemeralState,
    validate: (key, globalUnauthorized, signal) =>
      apiClient.request<AuthSessionPayload>('/api/auth/session', {
        authKey: key,
        handleUnauthorized: globalUnauthorized,
        signal,
      }),
  })
  router = createAppRouter(authSession, undefined, appI18n)

  createApp(App)
    .provide(authSessionKey, authSession)
    .provide(importRecoveryKey, importRecovery)
    .provide(unsavedChangesKey, unsavedChanges)
    .provide(apiClientKey, apiClient)
    .provide(appI18nKey, appI18n)
    .provide(themeControllerKey, themeController)
    .use(appI18n.plugin)
    .use(VueQueryPlugin, { queryClient })
    .use(router)
    .mount('#app')
}

void bootstrap()
