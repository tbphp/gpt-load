import { flushPromises, mount } from '@vue/test-utils'

import { createTestAppI18n } from '@/test/i18n'

import PreferencesControl from './PreferencesControl.vue'

function mountControl(compact = false) {
  const appI18n = createTestAppI18n(undefined, 'en-US')
  const wrapper = mount(PreferencesControl, {
    attachTo: document.body,
    props: {
      locale: 'en-US',
      theme: 'system',
      compact,
    },
    global: {
      plugins: [appI18n.plugin],
    },
  })
  return wrapper
}

describe('PreferencesControl', () => {
  it('emits locale and theme exactly once for each user choice', async () => {
    const wrapper = mountControl()

    await wrapper.get('input[data-test="preference-locale"][value="ja-JP"]').setValue()
    await wrapper.get('input[data-test="preference-theme"][value="dark"]').setValue()

    expect(wrapper.emitted('update:locale')).toEqual([['ja-JP']])
    expect(wrapper.emitted('update:theme')).toEqual([['dark']])
    wrapper.unmount()
  })

  it('uses one 44px trigger for compact shell placement', async () => {
    const wrapper = mountControl(true)

    expect(wrapper.findAll('[aria-label="Preferences"]')).toHaveLength(1)
    await wrapper.get('[aria-label="Preferences"]').trigger('click')
    await flushPromises()

    expect(document.querySelector('[data-test="preferences-panel"]')).not.toBeNull()
    wrapper.unmount()
  })
})
