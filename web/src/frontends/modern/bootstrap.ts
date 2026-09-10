import { createApp } from 'vue'

import App from './App.vue'
import { createModernI18n } from './i18n'
import { createModernRouter } from './router'
import './styles/base.css'

export async function bootstrap(): Promise<void> {
  const router = createModernRouter()
  const i18n = createModernI18n()
  document.documentElement.lang = i18n.global.locale.value
  const app = createApp(App).use(i18n).use(router)
  await router.isReady()
  app.mount('#app')
}
