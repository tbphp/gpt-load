import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from '@/app/router'
import { createTestAppI18n as createAppI18n } from '@/test/i18n'

import GroupTabs from './GroupTabs.vue'

async function mountTabs(path: string) {
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(GroupTabs, {
    global: { plugins: [createAppI18n(undefined, 'en-US').plugin, router] },
  })
  await flushPromises()
  return { router, wrapper }
}

describe('GroupTabs', () => {
  it('preserves the problem filter only on the Keys tab', async () => {
    const { router, wrapper } = await mountTabs('/groups/7?tab=keys&key_state=problem')

    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=keys&key_state=problem')
    await wrapper.get('[data-test="group-tab-models"]').trigger('mousedown', {
      button: 0,
      ctrlKey: false,
    })
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=models')

    await wrapper.get('[data-test="group-tab-keys"]').trigger('mousedown', {
      button: 0,
      ctrlKey: false,
    })
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=keys')
  })

  it('does not add history when the active tab is selected again', async () => {
    const { router, wrapper } = await mountTabs('/groups/7?tab=keys&key_state=problem')
    const push = vi.spyOn(router, 'push')

    await wrapper.get('[data-test="group-tab-keys"]').trigger('mousedown', {
      button: 0,
      ctrlKey: false,
    })
    await flushPromises()

    expect(push).not.toHaveBeenCalled()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=keys&key_state=problem')
  })

  it('normalizes unknown tabs and uses query history for click, back, and forward state', async () => {
    const { router, wrapper } = await mountTabs('/groups/7?tab=unknown&unsafe=discarded')

    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=keys')
    expect(wrapper.get('[data-test="group-tab-keys"]').attributes()['aria-selected']).toBe('true')

    await wrapper.get('[data-test="group-tab-models"]').trigger('mousedown', {
      button: 0,
      ctrlKey: false,
    })
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=models')

    await wrapper.get('[data-test="group-tab-settings"]').trigger('mousedown', {
      button: 0,
      ctrlKey: false,
    })
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=settings')

    router.back()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=models')
    expect(wrapper.get('[data-test="group-tab-models"]').attributes()['aria-selected']).toBe('true')

    router.forward()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=settings')
  })
})
