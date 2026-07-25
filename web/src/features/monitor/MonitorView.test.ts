import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from '@/app/router'
import { createAppI18n } from '@/i18n'

import MonitorView from './MonitorView.vue'

async function mountMonitor(path: string) {
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(MonitorView, {
    global: { plugins: [createAppI18n(undefined, 'en-US').plugin, router] },
  })
  await flushPromises()
  return { router, wrapper }
}

describe('MonitorView', () => {
  it('mounts only the active Logs slot and keeps tab navigation in browser history', async () => {
    const { router, wrapper } = await mountMonitor('/monitor?tab=logs')

    expect(wrapper.get('[data-test="monitor-tab-logs"]').attributes()['aria-selected']).toBe('true')
    expect(wrapper.find('[data-test="monitor-health-slot"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="monitor-logs-slot"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="monitor-inspector-slot"]').exists()).toBe(false)

    await wrapper.get('[data-test="monitor-tab-inspector"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=inspector')

    router.back()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=logs')
    expect(wrapper.get('[data-test="monitor-tab-logs"]').attributes()['aria-selected']).toBe('true')
  })

  it('replaces a legacy tab with the canonical Health query', async () => {
    const { router, wrapper } = await mountMonitor('/monitor?tab=requests')

    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=health')
    expect(wrapper.get('[data-test="monitor-tab-health"]').attributes()['aria-selected']).toBe(
      'true',
    )
    expect(wrapper.find('[data-test="monitor-health-slot"]').exists()).toBe(true)
  })
})
