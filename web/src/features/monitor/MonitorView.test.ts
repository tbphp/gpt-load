import { QueryClient } from '@tanstack/vue-query'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import { createAppRouter } from '@/app/router'
import { createAppI18n } from '@/i18n'
import { mountApp } from '@/test/mount-app'

import MonitorView from './MonitorView.vue'

async function mountMonitor(path: string) {
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(MonitorView, {
    global: {
      plugins: [createAppI18n(undefined, 'en-US').plugin, router],
      stubs: {
        HealthTab: {
          template: '<div data-test="monitor-health-content" />',
        },
        LogsTab: {
          template: '<div data-test="monitor-logs-content" />',
        },
      },
    },
  })
  await flushPromises()
  return { router, wrapper }
}

class MonitorApi implements ApiClient {
  readonly requests: Array<{ path: ApiPath; options?: ApiRequestOptions }> = []

  request<T>(path: ApiPath, options?: ApiRequestOptions): Promise<T> {
    this.requests.push({ path, options })
    if (path.startsWith('/api/logs')) {
      return Promise.resolve({ items: [], next_cursor: null } as T)
    }
    if (path === '/api/groups' || path === '/api/access-keys') {
      return Promise.resolve([] as T)
    }
    throw new Error(`Unexpected request: ${path}`)
  }
}

describe('MonitorView', () => {
  it('mounts only the active Logs slot and keeps tab navigation in browser history', async () => {
    const { router, wrapper } = await mountMonitor('/monitor?tab=logs')

    expect(wrapper.get('[data-test="monitor-tab-logs"]').attributes()['aria-selected']).toBe('true')
    expect(wrapper.find('[data-test="monitor-health-slot"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="monitor-logs-slot"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="monitor-logs-content"]').exists()).toBe(true)
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
    expect(wrapper.find('[data-test="monitor-health-content"]').exists()).toBe(true)
  })

  it('mounts real Logs queries only after a malformed direct URL is canonical', async () => {
    const api = new MonitorApi()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const observedQueryKeys: unknown[] = []
    const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
      observedQueryKeys.push(event.query.queryKey)
    })
    const canaries = {
      from: 'canary-from',
      requestID: 'canary-request-id',
      status: 'canary-status',
    }
    const path =
      `/monitor?tab=logs&from=${canaries.from}` +
      `&status=${canaries.status}&request_id=${canaries.requestID}`

    const { router, wrapper } = await mountApp(MonitorView, {
      api,
      queryClient,
      path,
      mounting: { attachTo: document.body },
    })
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=logs')
    expect(
      api.requests.filter(({ path: requestPath }) => requestPath.startsWith('/api/logs')),
    ).toEqual([
      {
        path: '/api/logs',
        options: { method: 'GET', signal: expect.any(AbortSignal) },
      },
    ])
    const observableState = JSON.stringify({
      cache: queryClient
        .getQueryCache()
        .getAll()
        .map(({ queryKey }) => queryKey),
      observedQueryKeys,
    })
    for (const canary of Object.values(canaries)) {
      expect(document.body.textContent).not.toContain(canary)
      expect(observableState).not.toContain(canary)
    }

    unsubscribe()
    wrapper.unmount()
  })
})
