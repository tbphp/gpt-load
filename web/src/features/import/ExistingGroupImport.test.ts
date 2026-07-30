import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import { ApiError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { createImportOperationOwner } from '@/features/import/import-operation-owner'
import type { ImportRecoveryService } from '@/features/import/import-recovery'
import type { ExistingGroupImportDraft } from '@/features/import/model-draft'
import { mountApp } from '@/test/mount-app'
import { FakeApi } from '@/test/fake-api'

import ExistingGroupImport from './ExistingGroupImport.vue'

const groups = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai-chat-completions' as const],
    models: [{ id: 'gpt-4o', alias: '' }],
    enabled: true,
    key_count: 2,
  },
  {
    id: 8,
    name: 'Backup',
    upstream_url: 'https://backup.example.com',
    protocols: ['anthropic' as const],
    models: [{ id: 'claude-sonnet', alias: '' }],
    enabled: true,
    key_count: 1,
  },
]

function recovery(): ImportRecoveryService {
  return {
    register: vi.fn(() => () => {}),
    captureForUnauthorized: () => 'no-active-draft',
    consume: () => null,
    clear: vi.fn(),
    sweep: () => {},
    dispose: () => {},
  }
}

function queryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

async function mountExisting(
  path: string,
  request: ApiClient['request'],
  initialDraft?: ExistingGroupImportDraft,
  attachTo?: Element,
) {
  const client = queryClient()
  const importRecovery = recovery()
  const operationOwner = createImportOperationOwner()
  const mounted = await mountApp(ExistingGroupImport, {
    api: { request },
    queryClient: client,
    path,
    locale: 'en-US',
    recovery: importRecovery,
    operationOwner,
    mounting: { props: { initialDraft }, attachTo },
  })
  await flushPromises()
  return { ...mounted, importRecovery, operationOwner, queryClient: client }
}

describe('ExistingGroupImport', () => {
  it('focuses the review heading and then the confirmed result heading', async () => {
    const request = vi.fn(async (path: string) => {
      if (path === '/api/groups') return groups
      if (path === '/api/groups/7/keys/import') {
        return { group_id: 7, keys_added: 1, keys_duplicated: 0 }
      }
      throw new Error(`unexpected request: ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountExisting(
      '/import?mode=existing&group_id=7',
      request,
      undefined,
      document.body,
    )

    await wrapper.get('[data-test="keys"]').setValue('raw-key')
    await wrapper.get('[data-test="existing-review"]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(
      wrapper.get('[data-test="existing-review-heading"]').element,
    )

    await wrapper.get('[data-test="existing-submit"]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(
      wrapper.get('[data-test="existing-result-heading"]').element,
    )
    wrapper.unmount()
  })

  it('preselects a positive route Group and imports exact raw keys without discovery or Group mutation', async () => {
    const requestMock = vi.fn(async (path: string, _options?: ApiRequestOptions) => {
      void _options
      if (path === '/api/groups') return groups
      if (path === '/api/groups/7/keys/import') {
        return { group_id: 7, keys_added: 3, keys_duplicated: 2 }
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const request = requestMock as ApiClient['request']
    const { importRecovery, operationOwner, queryClient, router, wrapper } = await mountExisting(
      '/import?mode=existing&group_id=7',
      request,
    )
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    expect((wrapper.get('[data-test="existing-group"]').element as HTMLSelectElement).value).toBe(
      '7',
    )
    await wrapper.get('[data-test="keys"]').setValue(' one\n\none\nsk-gl-warning ')

    expect(wrapper.text()).toContain('1 empty lines')
    expect(wrapper.text()).toContain('1 duplicate lines')
    expect(wrapper.text()).toContain('AccessKeys')
    expect(wrapper.get('[data-test="existing-review"]').attributes()).not.toHaveProperty('disabled')
    await wrapper.get('[data-test="existing-review"]').trigger('click')

    expect(wrapper.text()).not.toContain(' one')
    expect(wrapper.text()).toContain('3 non-empty keys')
    await wrapper.get('[data-test="existing-submit"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/7/keys/import', {
      method: 'POST',
      headers: {
        'Idempotency-Key': expect.stringMatching(
          /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
        ),
      },
      json: { keys: ' one\n\none\nsk-gl-warning ' },
      signal: expect.any(AbortSignal),
    })
    const paths = requestMock.mock.calls.map(([path]) => path)
    expect(paths).not.toContain('/api/models/discover')
    expect(paths).not.toContain('/api/groups/7/models/discover')
    expect(paths).not.toContain('/api/groups/7/models')
    expect(paths).not.toContain('/api/groups/7')
    expect(paths).not.toContain('/api/settings')
    expect(requestMock.mock.calls.every(([, options]) => options?.method !== 'PUT')).toBe(true)

    expect(wrapper.get('[data-test="existing-result"]').text()).toContain('3')
    expect(wrapper.get('[data-test="existing-result"]').text()).toContain('2')
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.keys(7), exact: true },
      { queryKey: controlQueryKeys.groups.detail(7), exact: true },
      { queryKey: controlQueryKeys.groups.list(), exact: true },
      { queryKey: controlQueryKeys.health(), exact: true },
    ])
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: controlQueryKeys.all })
    expect(importRecovery.clear).toHaveBeenCalledOnce()
    expect(operationOwner.importKeys.operation.value).toBeNull()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing&group_id=7')
  })

  it('blocks only more than 1,000 non-empty lines', async () => {
    const request = vi.fn(async () => groups) as ApiClient['request']
    const { wrapper } = await mountExisting('/import?mode=existing&group_id=7', request)

    await wrapper
      .get('[data-test="keys"]')
      .setValue(Array.from({ length: 1_001 }, (_, index) => `key-${index}`).join('\n'))

    expect(wrapper.get('[data-test="existing-review"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.text()).toContain('At most 1,000')
  })

  it('treats a digit-only positive Group identity as preselected', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve(groups)
    const { wrapper } = await mountExisting(
      '/import?mode=existing&group_id=007',
      api.request.bind(api),
    )

    expect((wrapper.get('[data-test="existing-group"]').element as HTMLSelectElement).value).toBe(
      '7',
    )
    expect(api.requests.map(({ path }) => path)).toEqual(['/api/groups'])
  })

  it.each([
    '/import?mode=existing&group_id=0',
    '/import?mode=existing&group_id=-1',
    '/import?mode=existing&group_id=7.5',
    '/import?mode=existing&group_id=abc',
    '/import?mode=existing&group_id=',
  ])('does not issue a Group-detail request for invalid route identity %s', async (path) => {
    const requestMock = vi.fn(async (requestPath: string) => {
      if (requestPath === '/api/groups') return groups
      throw new Error(`unexpected request: ${requestPath}`)
    })
    const { wrapper } = await mountExisting(path, requestMock as ApiClient['request'])

    expect((wrapper.get('[data-test="existing-group"]').element as HTMLSelectElement).value).toBe(
      '',
    )
    expect(requestMock.mock.calls.map(([requestPath]) => requestPath)).toEqual(['/api/groups'])
  })

  it('writes selection to route history so back and forward restore it', async () => {
    const request = vi.fn(async () => groups) as ApiClient['request']
    const { router, wrapper } = await mountExisting('/import?mode=existing&group_id=7', request)
    const selector = wrapper.get('[data-test="existing-group"]')

    await selector.setValue('8')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing&group_id=8')

    router.back()
    await flushPromises()
    expect((selector.element as HTMLSelectElement).value).toBe('7')

    router.forward()
    await flushPromises()
    expect((selector.element as HTMLSelectElement).value).toBe('8')
  })

  it('keeps the original operation target authoritative when the route changes', async () => {
    let settle!: (value: { group_id: number; keys_added: number; keys_duplicated: number }) => void
    const late = new Promise<{ group_id: number; keys_added: number; keys_duplicated: number }>(
      (resolve) => {
        settle = resolve
      },
    )
    const requestMock = vi.fn((path: string) => {
      if (path === '/api/groups') return Promise.resolve(groups)
      if (path === '/api/groups/8/keys/import') return late
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    const { importRecovery, queryClient, router, wrapper } = await mountExisting(
      '/import?mode=existing&group_id=7',
      requestMock as ApiClient['request'],
    )
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await wrapper.get('[data-test="existing-group"]').setValue('8')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing&group_id=8')
    await wrapper.get('[data-test="keys"]').setValue('late-target-key')
    await wrapper.get('[data-test="existing-review"]').trigger('click')
    await wrapper.get('[data-test="existing-submit"]').trigger('click')
    vi.stubGlobal(
      'confirm',
      vi.fn(() => true),
    )

    router.back()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing&group_id=7')

    settle({ group_id: 8, keys_added: 1, keys_duplicated: 0 })
    await flushPromises()

    expect(wrapper.find('[data-test="existing-result"]').exists()).toBe(true)
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.keys(8),
      exact: true,
    })
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.detail(8),
      exact: true,
    })
    expect(importRecovery.clear).toHaveBeenCalledOnce()
    vi.unstubAllGlobals()
    wrapper.unmount()
  })

  it('restores an existing draft and keeps raw keys out of route, caches, rendered text, and generic errors', async () => {
    const canary = 'UPSTREAM_KEY_CANARY_EXISTING_3c8f'
    const requestMock = vi.fn(async (path: string) => {
      if (path === '/api/groups') return groups
      throw new ApiError(500, 'INTERNAL_SERVER_ERROR', canary)
    })
    const { queryClient, router, wrapper } = await mountExisting(
      '/import?mode=existing&group_id=7',
      requestMock as ApiClient['request'],
      { mode: 'existing', group_id: 7, keys: canary },
    )

    expect((wrapper.get('[data-test="keys"]').element as HTMLTextAreaElement).value).toBe(canary)
    await wrapper.get('[data-test="existing-review"]').trigger('click')
    await wrapper.get('[data-test="existing-submit"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).not.toContain(canary)
    expect(
      JSON.stringify(
        queryClient
          .getQueryCache()
          .getAll()
          .map((query) => query.queryKey),
      ),
    ).not.toContain(canary)
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
    expect(wrapper.text()).not.toContain(canary)
    expect(wrapper.get('[data-test="import-operation-notice"]').text()).toContain(
      'could not be confirmed',
    )
    expect(wrapper.text()).not.toContain('Unable to import keys')
  })

  it('renders only the compacted resource identity and offers no resubmit action', async () => {
    const requestMock = vi.fn(async (path: string) => {
      if (path === '/api/groups') return groups
      if (path === '/api/groups/7/keys/import') {
        throw new ApiError(410, 'IDEMPOTENCY_RESULT_EXPIRED', 'expired', {
          operation_id: 'server-operation-secret',
          operation_kind: 'group_key_import',
          resource_identity: 'group:7:key-import:11',
          completed_at: '2026-07-20T00:00:00Z',
        })
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const { wrapper } = await mountExisting(
      '/import?mode=existing&group_id=7',
      requestMock as ApiClient['request'],
    )
    await wrapper.get('[data-test="keys"]').setValue('raw-key')
    await wrapper.get('[data-test="existing-review"]').trigger('click')
    await wrapper.get('[data-test="existing-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="import-operation-resource"]').text()).toBe(
      'group:7:key-import:11',
    )
    expect(wrapper.find('[data-test="import-operation-retry"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('server-operation-secret')
  })
})
