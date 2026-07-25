import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { AccessKeyDto, GroupSummary } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { createAppRouter } from '@/app/router'
import { mountApp } from '@/test/mount-app'

import AccessKeysView from './AccessKeysView.vue'

const canary = 'sk-gl-ACCESS_KEYS_LIST_CANARY'
const groups: GroupSummary[] = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai-response'],
    models: [{ id: 'gpt-4.1', alias: 'public-gpt' }],
    enabled: true,
    key_count: 1,
  },
]
const keys: AccessKeyDto[] = [
  {
    id: 9,
    name: 'client',
    key: canary,
    status: 'active',
    filters: { groups: [], protocols: [], models: [] },
    rpm_limit: 0,
  },
]

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountView(request: ApiClient['request']) {
  const client = queryClient()
  const mounted = await mountApp(AccessKeysView, {
    api: { request },
    queryClient: client,
    path: '/access-keys',
    locale: 'en-US',
    mounting: { attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

describe('AccessKeysView', () => {
  it('replaces the route placeholder, loads real Groups and AccessKeys, and renders empty filters as all', async () => {
    const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
    expect(router.resolve('/access-keys').matched.at(-1)?.components?.default).toBe(AccessKeysView)

    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys' && options?.method === 'GET') return keys
      if (path === '/api/groups' && options?.method === 'GET') return groups
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)

    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('All Groups')
    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('All protocols')
    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('All models')
    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('Unlimited')
    expect(wrapper.text()).not.toContain(canary)
    wrapper.unmount()
  })

  it('masks by default, reveals and copies only locally, then removes gcTime-zero plaintext on unmount', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys' && options?.method === 'GET') return keys
      if (path === '/api/groups' && options?.method === 'GET') return groups
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { queryClient: client, router, wrapper } = await mountView(request)

    expect(wrapper.text()).not.toContain(canary)
    await wrapper.get('[data-test="access-key-reveal-9"]').trigger('click')
    expect(wrapper.text()).toContain(canary)
    await wrapper.get('[data-test="access-key-copy-9"] button').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(canary)

    expect(JSON.stringify(router.currentRoute.value)).not.toContain(canary)
    expect(JSON.stringify(controlQueryKeys)).not.toContain(canary)
    expect(sessionStorage.getItem('gpt-load.access-key')).toBeNull()
    expect(localStorage.getItem('gpt-load.access-key')).toBeNull()
    expect(client.getMutationCache().getAll()).toHaveLength(0)

    wrapper.unmount()
    await flushPromises()
    expect(client.getQueryData(controlQueryKeys.accessKeys.list())).toBeUndefined()
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain(canary)
    expect(document.body.textContent).not.toContain(canary)
  })

  it('retains masked stale list data and never renders generic error details', async () => {
    let listCalls = 0
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys' && options?.method === 'GET') {
        listCalls += 1
        if (listCalls === 1) return keys
        throw new Error('sk-gl-GENERIC_ERROR_CANARY')
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountView(request)

    await client.invalidateQueries({ queryKey: controlQueryKeys.accessKeys.list() })
    await flushPromises()
    expect(wrapper.get('[data-test="access-key-row-9"]')).toBeDefined()
    expect(wrapper.text()).toContain('may be stale')
    expect(wrapper.html()).not.toContain('sk-gl-GENERIC_ERROR_CANARY')
    expect(wrapper.html()).not.toContain(canary)
    wrapper.unmount()
  })
})
