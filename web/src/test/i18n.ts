import accessKeysEN from '@/i18n/locales/en-US/access-keys'
import coreEN from '@/i18n/locales/en-US/core'
import groupEN from '@/i18n/locales/en-US/group'
import importEN from '@/i18n/locales/en-US/import'
import modelPricesEN from '@/i18n/locales/en-US/model-prices'
import monitorEN from '@/i18n/locales/en-US/monitor'
import settingsEN from '@/i18n/locales/en-US/settings'
import accessKeysJA from '@/i18n/locales/ja-JP/access-keys'
import coreJA from '@/i18n/locales/ja-JP/core'
import groupJA from '@/i18n/locales/ja-JP/group'
import importJA from '@/i18n/locales/ja-JP/import'
import modelPricesJA from '@/i18n/locales/ja-JP/model-prices'
import monitorJA from '@/i18n/locales/ja-JP/monitor'
import settingsJA from '@/i18n/locales/ja-JP/settings'
import accessKeysZH from '@/i18n/locales/zh-CN/access-keys'
import coreZH from '@/i18n/locales/zh-CN/core'
import groupZH from '@/i18n/locales/zh-CN/group'
import importZH from '@/i18n/locales/zh-CN/import'
import modelPricesZH from '@/i18n/locales/zh-CN/model-prices'
import monitorZH from '@/i18n/locales/zh-CN/monitor'
import settingsZH from '@/i18n/locales/zh-CN/settings'
import { createAppI18nForTesting, type AppLocale } from '@/i18n'

const messages = {
  'zh-CN': {
    ...coreZH,
    ...importZH,
    ...groupZH,
    ...accessKeysZH,
    ...monitorZH,
    ...modelPricesZH,
    ...settingsZH,
  },
  'en-US': {
    ...coreEN,
    ...importEN,
    ...groupEN,
    ...accessKeysEN,
    ...monitorEN,
    ...modelPricesEN,
    ...settingsEN,
  },
  'ja-JP': {
    ...coreJA,
    ...importJA,
    ...groupJA,
    ...accessKeysJA,
    ...monitorJA,
    ...modelPricesJA,
    ...settingsJA,
  },
}

export function createTestAppI18n(storage?: Storage, locale: AppLocale = 'zh-CN') {
  return createAppI18nForTesting(messages, locale, storage)
}

export const testMessages = messages
