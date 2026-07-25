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
})
