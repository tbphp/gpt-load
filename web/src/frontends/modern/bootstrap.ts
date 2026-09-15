import { VueQueryPlugin } from '@tanstack/vue-query'
import { createApp } from 'vue'

import { apiClientKey } from '@shared/http/client-context'
import App from './App.vue'
import { createSessionApiClient } from './app/api-client'
import { createPreferences, preferencesKey } from './app/preferences'
import { createModernQueryClient } from './app/query'
import { authSessionKey, createAuthSession, type AuthSession } from './features/auth/auth-session'
import { createModernI18n } from './i18n'
import { createModernRouter } from './router'
import './styles/base.css'

function getStorage(type: 'localStorage' | 'sessionStorage'): Storage | undefined {
  try {
    return window[type]
  } catch {
    // 存储不可用时，会话继续在内存中维护。
    return undefined
  }
}

export async function bootstrap(): Promise<void> {
  const i18n = createModernI18n()
  document.documentElement.lang = i18n.global.locale.value
  const preferences = createPreferences(i18n.global.locale.value, (locale) => {
    i18n.global.locale.value = locale
  })
  const queryClient = createModernQueryClient()
  const client = createSessionApiClient({
    fetch: window.fetch.bind(window),
    getSession: () => session,
    getLocale: () => i18n.global.locale.value,
  })
  const session: AuthSession = createAuthSession({
    client,
    queryClient,
    localStorage: getStorage('localStorage'),
    sessionStorage: getStorage('sessionStorage'),
  })
  const router = createModernRouter(session)
  const app = createApp(App)
    .provide(preferencesKey, preferences)
    .provide(apiClientKey, client)
    .provide(authSessionKey, session)
    .use(i18n)
    .use(VueQueryPlugin, { queryClient })
    .use(router)
  app.onUnmount(preferences.dispose)
  await router.isReady()
  app.mount('#app')
}
