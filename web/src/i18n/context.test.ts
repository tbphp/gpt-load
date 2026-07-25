import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import { appI18nKey, useAppI18n } from './context'
import { createAppI18n, type AppI18n } from './index'

let injected: AppI18n | undefined
const AppI18nConsumer = defineComponent({
  setup() {
    injected = useAppI18n()
    return {}
  },
  template: '<div />',
})

describe('AppI18n injection', () => {
  it('fails fast when AppI18n is not provided', () => {
    expect(() => mount(AppI18nConsumer)).toThrow('APP_I18N_NOT_PROVIDED')
  })

  it('returns the provided AppI18n controller', () => {
    const appI18n = createAppI18n(undefined, 'en-US')

    mount(AppI18nConsumer, {
      global: { provide: { [appI18nKey as symbol]: appI18n } },
    })

    expect(injected).toBe(appI18n)
  })
})
