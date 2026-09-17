import { createI18n } from 'vue-i18n'

import { getBrowserLocale } from '@shared/preferences/locale'

import enUS from './locales/en-US'
import jaJP from './locales/ja-JP'
import zhCN from './locales/zh-CN'

export function createModernI18n() {
  return createI18n({
    legacy: false,
    locale: getBrowserLocale(),
    fallbackLocale: 'en-US',
    messages: { 'zh-CN': zhCN, 'en-US': enUS, 'ja-JP': jaJP },
  })
}
