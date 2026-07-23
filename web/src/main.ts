import { VueQueryPlugin } from '@tanstack/vue-query'
import { createApp } from 'vue'
import type { Router } from 'vue-router'

import App from './App.vue'
import { createApiClient } from './api/client'
import type { AuthSessionPayload } from './api/types'
import { createAppQueryClient } from './app/query'
import { createAppRouter } from './app/router'
import { authSessionKey, createAuthSession, type AuthSession } from './features/auth/auth-session'
import { createAppI18n } from './i18n'
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
    authSession?.clear()
    if (router) {
      void router.replace({
        name: 'login',
        query: { redirect },
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
  .use(appI18n.plugin)
  .use(VueQueryPlugin, { queryClient })
  .use(router)
  .mount('#app')
