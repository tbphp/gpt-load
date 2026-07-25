import { inject, type InjectionKey } from 'vue'

import type { AppI18n } from './index'

export const appI18nKey: InjectionKey<AppI18n> = Symbol('app-i18n')

export function useAppI18n(): AppI18n {
  const appI18n = inject(appI18nKey)
  if (!appI18n) throw new Error('APP_I18N_NOT_PROVIDED')
  return appI18n
}
