import { createApp } from 'vue'

import App from './App.vue'
import { createPreferences, preferencesKey } from './app/preferences'
import favicon from './assets/brand/icon.svg'
import { createModernI18n } from './i18n'
import { createModernRouter } from './router'
import './styles/base.css'

export async function bootstrap(): Promise<void> {
  const router = createModernRouter()
  const i18n = createModernI18n()
  document.documentElement.lang = i18n.global.locale.value
  const preferences = createPreferences(i18n.global.locale.value, (locale) => {
    i18n.global.locale.value = locale
  })
  const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (icon) icon.href = favicon
  const app = createApp(App).provide(preferencesKey, preferences).use(i18n).use(router)
  await router.isReady()
  app.mount('#app')
}
