import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import GroupDetailView from './GroupDetailView.vue'

const detail = {
  id: 7,
  name: 'Primary',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai' as const],
  models: [{ id: 'gpt-4o', alias: '' }],
  enabled: true,
  validation_model: null,
  weight_manual: null,
  config: { header_rules: { set: { 'X-Canary': 'HEADER_RULE_CANARY_9c41' }, remove: [] } },
  effective_config: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Canary': 'HEADER_RULE_CANARY_9c41' }, remove: [] },
  },
  key_count: 0,
}

const detail8 = {
  ...detail,
  id: 8,
  name: 'Secondary',
  upstream_url: 'https://secondary.example.com/v1',
  config: { header_rules: { set: {}, remove: [] } },
  effective_config: {
    ...detail.effective_config,
    header_rules: { set: {}, remove: [] },
  },
}

const group7Keys = [
  {
    id: 11,
    group_id: 7,
    mask: 'sk-group-7-…a1b2',
    status: 'active' as const,
    effective_status: 'available' as const,
    weight_manual: null,
    weight_auto: 72,
    blacklisted: false,
    cooldown_until: null,
    failure_count: 0,
  },
]

const group8Keys = [
  {
    id: 21,
    group_id: 8,
    mask: 'sk-group-8-…c3d4',
    status: 'active' as const,
    effective_status: 'available' as const,
    weight_manual: null,
    weight_auto: 61,
    blacklisted: false,
    cooldown_until: null,
    failure_count: 0,
  },
]

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

describe('GroupDetailView', () => {
  it.each(['/groups/0', '/groups/-1', '/groups/7.5', '/groups/not-a-number'])(
    'shows a local invalid identity state and issues zero API calls for %s',
    async (path) => {
      const request = vi.fn() as ApiClient['request']
      const mounted = await mountApp(GroupDetailView, {
        api: { request },
        queryClient: queryClient(),
        path,
        locale: 'en-US',
      })
      await flushPromises()

      expect(mounted.wrapper.get('[data-test="invalid-group-id"]').attributes('role')).toBe('alert')
      expect(request).not.toHaveBeenCalled()
      mounted.wrapper.unmount()
    },
  )

  it('loads detail and keys independently, renders a secret-safe stable header, and drops detail cache on unmount', async () => {
    const requestMock = vi.fn(async (path: string, _options?: ApiRequestOptions) => {
      void _options
      if (path === '/api/groups/7') return detail
      if (path === '/api/groups/7/keys') return []
      throw new Error(`unexpected request: ${path}`)
    })
    const client = queryClient()
    const { router, wrapper } = await mountApp(GroupDetailView, {
      api: { request: requestMock as ApiClient['request'] },
      queryClient: client,
      path: '/groups/7?tab=keys',
      locale: 'en-US',
    })
    await flushPromises()

    expect(requestMock.mock.calls.map(([path]) => path)).toEqual([
      '/api/groups/7',
      '/api/groups/7/keys',
    ])
    expect(requestMock.mock.calls[0]?.[1]).toMatchObject({ method: 'GET' })
    expect(requestMock.mock.calls[1]?.[1]).toMatchObject({ method: 'GET' })
    expect(wrapper.get('h1').text()).toBe('Primary')
    expect(wrapper.text()).toContain('api.example.com')
    expect(wrapper.text()).toContain('OpenAI')
    expect(wrapper.text()).toContain('Enabled')
    expect(wrapper.get('[data-test="group-import-link"]').attributes('href')).toBe(
      '/import?mode=existing&group_id=7',
    )
    const activeTab = wrapper.get('[data-test="group-tab-keys"]')
    const panel = wrapper.get('[role="tabpanel"]')
    expect(activeTab.attributes('aria-controls')).toBe(panel.attributes('id'))
    expect(panel.attributes('aria-labelledby')).toBe(activeTab.attributes('id'))
    expect(panel.text()).toContain('No upstream keys')

    const rendered = wrapper.html()
    const routeState = JSON.stringify(router.currentRoute.value)
    const queryKeys = JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    )
    expect(rendered).not.toContain('HEADER_RULE_CANARY_9c41')
    expect(routeState).not.toContain('HEADER_RULE_CANARY_9c41')
    expect(queryKeys).not.toContain('HEADER_RULE_CANARY_9c41')
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    expect(client.getQueryData(controlQueryKeys.groups.detail(7))).toEqual(detail)

    wrapper.unmount()
    await vi.waitFor(() => {
      expect(client.getQueryData(controlQueryKeys.groups.detail(7))).toBeUndefined()
    })
  })

  it('renders the route-backed Models editor instead of the placeholder', async () => {
    const request = vi.fn(async (path: string) => {
      if (path === '/api/groups/7') return detail
      throw new Error(`unexpected request: ${path}`)
    }) as ApiClient['request']
    const mounted = await mountApp(GroupDetailView, {
      api: { request },
      queryClient: queryClient(),
      path: '/groups/7?tab=models',
      locale: 'en-US',
    })
    await flushPromises()

    expect(mounted.wrapper.get('[data-test="models-discover"]').text()).toContain('Rediscover')
    expect(mounted.wrapper.text()).not.toContain('delivered in the next T23 task')
    expect(request).toHaveBeenCalledTimes(1)
    mounted.wrapper.unmount()
  })

  it('does not render the Settings placeholder while a Models-route detail is still loading', async () => {
    const request = vi.fn(() => new Promise(() => undefined)) as ApiClient['request']
    const mounted = await mountApp(GroupDetailView, {
      api: { request },
      queryClient: queryClient(),
      path: '/groups/7?tab=models',
      locale: 'en-US',
    })

    expect(mounted.wrapper.text()).toContain('Loading Group details')
    expect(mounted.wrapper.text()).not.toContain('Group settings')
    mounted.wrapper.unmount()
  })

  it('renders the route-backed Settings editor instead of the placeholder', async () => {
    const request = vi.fn(async (path: string) => {
      if (path === '/api/groups/7') return detail
      throw new Error(`unexpected request: ${path}`)
    }) as ApiClient['request']
    const mounted = await mountApp(GroupDetailView, {
      api: { request },
      queryClient: queryClient(),
      path: '/groups/7?tab=settings',
      locale: 'en-US',
    })
    await flushPromises()

    expect(mounted.wrapper.get('[data-test="group-settings-save"]').text()).toContain('Save')
    expect(mounted.wrapper.text()).not.toContain('delivered in the following T23 task')
    expect(mounted.wrapper.text()).not.toContain('HEADER_RULE_CANARY_9c41')
    mounted.wrapper.unmount()
  })

  it('remounts the Keys tab when route Group ID changes so old rows and local state cannot target the new Group', async () => {
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7') return detail
      if (path === '/api/groups/8') return detail8
      if (path === '/api/groups/7/keys' && options?.method === 'GET') return group7Keys
      if (path === '/api/groups/8/keys' && options?.method === 'GET') return group8Keys
      if (path === '/api/groups/8/keys/21' && options?.method === 'PUT') {
        return { ...group8Keys[0], weight_manual: 50 }
      }
      if (path === '/api/groups/8/keys/21' && options?.method === 'DELETE') return undefined
      throw new Error(`unexpected request: ${path}`)
    })
    const client = queryClient()
    const { router, wrapper } = await mountApp(GroupDetailView, {
      api: { request: requestMock as ApiClient['request'] },
      queryClient: client,
      path: '/groups/7?tab=keys',
      locale: 'en-US',
    })
    await flushPromises()

    expect(wrapper.text()).toContain('sk-group-7-…a1b2')
    await wrapper.get('[data-test="key-weight-11"]').setValue('42')
    await wrapper.get('[data-test="key-delete-11"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()

    await router.push('/groups/8?tab=keys')
    await flushPromises()

    expect(requestMock.mock.calls.map(([path]) => path)).toContain('/api/groups/8/keys')
    expect(wrapper.text()).not.toContain('sk-group-7-…a1b2')
    expect(wrapper.text()).toContain('sk-group-8-…c3d4')
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect((wrapper.get('[data-test="key-weight-21"]').element as HTMLSelectElement).value).toBe(
      'auto',
    )

    await wrapper.get('[data-test="key-weight-21"]').setValue('50')
    await wrapper.get('[data-test="key-save-21"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/8/keys/21', {
      method: 'PUT',
      json: { weight_manual: 50 },
      signal: expect.any(AbortSignal),
    })

    await wrapper.get('[data-test="key-delete-21"]').trigger('click')
    await flushPromises()
    const confirmDelete = document.querySelector<HTMLButtonElement>(
      '[data-test="key-delete-confirm-21"]',
    )
    expect(confirmDelete).not.toBeNull()
    confirmDelete?.click()
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/8/keys/21', {
      method: 'DELETE',
      signal: expect.any(AbortSignal),
    })
    const mutationPaths = requestMock.mock.calls
      .filter(([, options]) => options?.method === 'PUT' || options?.method === 'DELETE')
      .map(([path]) => path)
    expect(mutationPaths).toEqual(['/api/groups/8/keys/21', '/api/groups/8/keys/21'])
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })
})
