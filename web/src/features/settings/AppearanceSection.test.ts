import { mount } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import { apiClientKey } from '@/api/client-context'
import { createThemeController, themeControllerKey } from '@/features/preferences/theme'
import { appI18nKey } from '@/i18n/context'
import { createTestAppI18n as createAppI18n } from '@/test/i18n'

import AppearanceSection from './AppearanceSection.vue'

function mountSection() {
  const request = vi.fn() as ApiClient['request']
  const appI18n = createAppI18n(window.localStorage, 'en-US')
  const theme = createThemeController({
    documentElement: document.documentElement,
    storage: window.localStorage,
    matchMedia: window.matchMedia.bind(window),
  })
  const wrapper = mount(AppearanceSection, {
    global: {
      plugins: [appI18n.plugin],
      provide: {
        [apiClientKey as symbol]: { request },
        [appI18nKey as symbol]: appI18n,
        [themeControllerKey as symbol]: theme,
      },
    },
  })
  return { appI18n, request, theme, wrapper }
}

describe('AppearanceSection', () => {
  it('updates and safely persists all three browser-local locales and themes immediately', async () => {
    const { appI18n, request, theme, wrapper } = mountSection()

    expect(wrapper.text()).toContain('current browser')
    expect(wrapper.get('[data-test="appearance-locale"]').findAll('option')).toHaveLength(3)
    expect(wrapper.get('[data-test="appearance-theme"]').findAll('option')).toHaveLength(3)

    await wrapper.get('[data-test="appearance-locale"]').setValue('ja-JP')
    expect(appI18n.getLocale()).toBe('ja-JP')
    expect(document.documentElement.lang).toBe('ja-JP')
    expect(localStorage.getItem('gpt-load.locale')).toBe('ja-JP')

    await wrapper.get('[data-test="appearance-theme"]').setValue('dark')
    expect(theme.theme.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(localStorage.getItem('gpt-load.theme')).toBe('dark')

    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
    theme.dispose()
  })
})
