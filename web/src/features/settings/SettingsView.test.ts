import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { SettingsDto } from '@/api/control/settings'
import { controlQueryKeys } from '@/app/query-keys'
import { createAppRouter } from '@/app/router'
import { createThemeController, themeControllerKey } from '@/features/preferences/theme'
import { mountApp } from '@/test/mount-app'

import SettingsView from './SettingsView.vue'

const headerCanary = 'HEADER_RULE_CACHE_CANARY_8f31'
const base: SettingsDto = {
  revision: 10,
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Global': headerCanary }, remove: [] },
    request_log_retention_days: 7,
  },
  overrides: [],
}
const updated: SettingsDto = {
  ...base,
  revision: 11,
  values: { ...base.values, request_timeout: 900 },
  overrides: ['request_timeout'],
}
const systemInfo = {
  version: '2.0.0',
  deployment: {
    instance_mode: 'single',
    database: 'sqlite',
    distribution: 'single_binary',
  },
  data_dir: '/data',
  auth_key: { source: 'environment', path: null },
  encryption: { enabled: true, source: 'environment', path: null },
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountView() {
  const client = queryClient()
  client.setQueryData(controlQueryKeys.groups.list(), [{ id: 7 }])
  client.setQueryData(controlQueryKeys.groups.detail(7), { id: 7, effective_config: {} })
  client.setQueryData(controlQueryKeys.groups.detail(8), { id: 8, effective_config: {} })
  client.setQueryData(controlQueryKeys.groups.keys(7), [{ id: 1 }])
  client.setQueryData(controlQueryKeys.health(), { observed_at: 'now' })
  client.setQueryData(controlQueryKeys.accessKeys.list(), [{ id: 9 }])

  const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
    if (path === '/api/settings' && options?.method === undefined) return base
    if (path === '/api/settings' && options?.method === 'PUT') return updated
    if (path === '/api/system/info') return systemInfo
    throw new Error(`unexpected request ${path}`)
  })
  const theme = createThemeController({
    documentElement: document.documentElement,
    storage: localStorage,
    matchMedia: window.matchMedia.bind(window),
  })
  const mounted = await mountApp(SettingsView, {
    api: { request: requestMock as ApiClient['request'] },
    queryClient: client,
    locale: 'en-US',
    path: '/settings',
    mounting: {
      attachTo: document.body,
      global: { provide: { [themeControllerKey as symbol]: theme } },
    },
  })
  await flushPromises()
  return { ...mounted, queryClient: client, requestMock, theme }
}

describe('SettingsView', () => {
  it('replaces the route placeholder and uses a gcTime-zero Settings query', async () => {
    const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
    expect(router.resolve('/settings').matched.at(-1)?.components?.default).toBe(SettingsView)

    const { queryClient: client, theme, wrapper } = await mountView()
    expect(wrapper.get('h1').text()).toBe('Settings')
    expect(
      client.getQueryCache().find({ queryKey: controlQueryKeys.settings() })?.options.gcTime,
    ).toBe(0)
    expect(wrapper.html()).not.toContain(headerCanary)
    wrapper.unmount()
    await flushPromises()
    expect(client.getQueryData(controlQueryKeys.settings())).toBeUndefined()
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain(headerCanary)
    theme.dispose()
  })

  it('rebases Settings and invalidates only the loaded Group-detail prefix after success', async () => {
    const { queryClient: client, requestMock, theme, wrapper } = await mountView()
    const setQueryData = vi.spyOn(client, 'setQueryData')
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-request_timeout"]').setValue('900')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      json: { settings: { request_timeout: 900 } },
      signal: expect.any(AbortSignal),
    })
    expect(setQueryData).toHaveBeenCalledWith(controlQueryKeys.settings(), updated)
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.details() },
    ])
    expect(client.getQueryData(controlQueryKeys.groups.list())).toEqual([{ id: 7 }])
    expect(client.getQueryData(controlQueryKeys.groups.keys(7))).toEqual([{ id: 1 }])
    expect(client.getQueryData(controlQueryKeys.health())).toEqual({ observed_at: 'now' })
    expect(client.getQueryData(controlQueryKeys.accessKeys.list())).toEqual([{ id: 9 }])
    expect(client.getQueryData(controlQueryKeys.systemInfo())).toEqual(systemInfo)
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    expect(JSON.stringify(wrapper.emitted())).not.toContain(headerCanary)
    expect(JSON.stringify(wrapper.vm.$route)).not.toContain(headerCanary)
    expect(JSON.stringify(controlQueryKeys)).not.toContain(headerCanary)
    expect(JSON.stringify(localStorage)).not.toContain(headerCanary)
    wrapper.unmount()
    theme.dispose()
  })
})
