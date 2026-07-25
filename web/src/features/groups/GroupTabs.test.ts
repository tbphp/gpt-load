import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from '@/app/router'
import { createAppI18n } from '@/i18n'

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
  it('normalizes unknown tabs and uses query history for click, back, and forward state', async () => {
    const { router, wrapper } = await mountTabs('/groups/7?tab=unknown&unsafe=discarded')

    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=keys')
    expect(wrapper.get('[data-test="group-tab-keys"]').attributes()['aria-selected']).toBe('true')

    await wrapper.get('[data-test="group-tab-models"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/groups/7?tab=models')

    await wrapper.get('[data-test="group-tab-settings"]').trigger('click')
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
