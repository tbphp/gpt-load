import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { GroupDetailDto } from '@/api/control/groups'
import { ApiError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import GroupModelsTab from './GroupModelsTab.vue'

const detail: GroupDetailDto = {
  id: 7,
  name: 'Primary',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai'],
  models: [
    { id: 'old', alias: 'public' },
    { id: 'legacy', alias: 'legacy-public' },
  ],
  enabled: true,
  key_count: 2,
  validation_model: null,
  weight_manual: null,
  config: { header_rules: { set: { 'X-Canary': 'HEADER_RULE_SECRET' }, remove: [] } },
  effective_config: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Canary': 'HEADER_RULE_SECRET' }, remove: [] },
  },
}

function queryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountModels(request: ApiClient['request'], group = detail) {
  const client = queryClient()
  const mounted = await mountApp(GroupModelsTab, {
    api: { request },
    queryClient: client,
    path: '/groups/7?tab=models',
    locale: 'en-US',
    mounting: {
      props: { groupId: 7, group },
      attachTo: document.body,
    },
  })
  return { ...mounted, queryClient: client }
}

function clickDocument(selector: string): void {
  const element = document.querySelector<HTMLButtonElement>(selector)
  if (!element) throw new Error(`missing element ${selector}`)
  element.click()
}

describe('GroupModelsTab', () => {
  it('merges candidate-only discovery without losing saved aliases or missing saved models', async () => {
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7/models/discover' && options?.method === 'POST') {
        return { models: ['old', 'new'] }
      }
      throw new Error(`unexpected request: ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountModels(request)

    await wrapper.get('[data-test="models-discover"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/groups/7/models/discover', {
      method: 'POST',
      signal: expect.any(AbortSignal),
    })
    expect((wrapper.get('[data-test="model-alias-0"]').element as HTMLInputElement).value).toBe(
      'public',
    )
    expect(
      (wrapper.get('[data-test="model-selected-1"]').element as HTMLInputElement).checked,
    ).toBe(true)
    expect(wrapper.get('[data-test="model-status-1"]').text()).toBe('Not rediscovered')
    expect(wrapper.get('[data-test="model-status-2"]').text()).toBe('Newly discovered')
    expect(wrapper.text()).toContain('new')
    wrapper.unmount()
  })

  it('appends rediscovered candidates without discarding the current dirty draft', async () => {
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7/models/discover' && options?.method === 'POST') {
        return { models: ['old', 'manual-id', 'new', 'new'] }
      }
      throw new Error(`unexpected request: ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountModels(request)

    await wrapper.get('[data-test="model-alias-0"]').setValue('local-public')
    await wrapper.get('[data-test="model-selected-1"]').setValue(false)
    await wrapper.get('[data-test="manual-model-id"]').setValue('manual-id')
    await wrapper.get('[data-test="manual-model-alias"]').setValue('manual-public')
    await wrapper.get('[data-test="add-manual-model"]').trigger('click')
    await wrapper.get('[data-test="models-discover"]').trigger('click')
    await flushPromises()

    expect((wrapper.get('[data-test="model-alias-0"]').element as HTMLInputElement).value).toBe(
      'local-public',
    )
    expect(
      (wrapper.get('[data-test="model-selected-1"]').element as HTMLInputElement).checked,
    ).toBe(false)
    expect(wrapper.get('[data-test="model-status-0"]').text()).toBe('Rediscovered')
    expect(wrapper.get('[data-test="model-status-1"]').text()).toBe('Not rediscovered')
    expect(wrapper.get('[data-test="model-row-2"]').text()).toContain('manual-id')
    expect((wrapper.get('[data-test="model-alias-2"]').element as HTMLInputElement).value).toBe(
      'manual-public',
    )
    expect(wrapper.get('[data-test="model-status-2"]').text()).toBe('Manual')
    expect(wrapper.get('[data-test="model-row-3"]').text()).toContain('new')
    expect(wrapper.get('[data-test="model-status-3"]').text()).toBe('Newly discovered')
    expect(wrapper.findAll('[data-test^="model-row-"]')).toHaveLength(4)
    wrapper.unmount()
  })

  it('preserves the draft and manual model path after BAD_GATEWAY discovery failure', async () => {
    const request = vi.fn().mockRejectedValue(new ApiError(502, 'BAD_GATEWAY', 'secret upstream'))
    const { wrapper } = await mountModels(request as ApiClient['request'])

    await wrapper.get('[data-test="models-discover"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Discovery failed upstream')
    expect((wrapper.get('[data-test="model-alias-0"]').element as HTMLInputElement).value).toBe(
      'public',
    )
    await wrapper.get('[data-test="manual-model-id"]').setValue('manual-id')
    await wrapper.get('[data-test="manual-model-alias"]').setValue('manual-public')
    await wrapper.get('[data-test="add-manual-model"]').trigger('click')
    expect(wrapper.text()).toContain('manual-id')
    expect(wrapper.html()).not.toContain('secret upstream')
    wrapper.unmount()
  })

  it('renders structured Keys and existing-Group import actions for NO_ACTIVE_UPSTREAM_KEY', async () => {
    const request = vi
      .fn()
      .mockRejectedValue(new ApiError(409, 'NO_ACTIVE_UPSTREAM_KEY', 'no active key'))
    const { wrapper } = await mountModels(request as ApiClient['request'])

    await wrapper.get('[data-test="models-discover"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="models-keys-action"]').attributes('href')).toBe(
      '/groups/7?tab=keys',
    )
    expect(wrapper.get('[data-test="models-import-action"]').attributes('href')).toBe(
      '/import?mode=existing&group_id=7',
    )
    wrapper.unmount()
  })

  it('suppresses a normalized no-op and sends zero PUT requests', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountModels(request)

    expect(wrapper.get('[data-test="models-save"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-test="model-alias-0"]').setValue(' public ')
    expect(wrapper.get('[data-test="models-save"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-test="models-save"]').trigger('click')

    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('sends the exact full normalized list, rebases detail, and invalidates only the Group list', async () => {
    const updated = {
      ...detail,
      models: [
        { id: 'old', alias: 'public' },
        { id: 'new', alias: 'fresh' },
      ],
    }
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7/models/discover' && options?.method === 'POST') {
        return { models: ['old', 'new'] }
      }
      if (path === '/api/groups/7/models' && options?.method === 'PUT') return updated
      throw new Error(`unexpected request: ${path}`)
    })
    const { queryClient: client, wrapper } = await mountModels(requestMock as ApiClient['request'])

    await wrapper.get('[data-test="models-discover"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="model-selected-1"]').setValue(false)
    await wrapper.get('[data-test="model-alias-2"]').setValue(' fresh ')
    expect(wrapper.get('[data-test="models-removal-warning"]').text()).toContain('AccessKey')

    const setQueryData = vi.spyOn(client, 'setQueryData')
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    await wrapper.get('[data-test="models-save"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/7/models', {
      method: 'PUT',
      json: {
        models: [
          { id: 'old', alias: 'public' },
          { id: 'new', alias: 'fresh' },
        ],
      },
      signal: expect.any(AbortSignal),
    })
    expect(setQueryData).toHaveBeenCalledWith(controlQueryKeys.groups.detail(7), updated)
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.list() },
    ])
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: controlQueryKeys.health() })
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    expect(wrapper.get('[data-test="models-save"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('requires an accessible hard confirmation before replacing models with an empty list', async () => {
    const oneModel = { ...detail, models: [{ id: 'only', alias: 'public' }] }
    const updated = { ...oneModel, models: [] }
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7/models' && options?.method === 'PUT') return updated
      throw new Error(`unexpected request: ${path}`)
    })
    const { wrapper } = await mountModels(requestMock as ApiClient['request'], oneModel)

    await wrapper.get('[data-test="model-selected-0"]').setValue(false)
    expect(wrapper.get('[data-test="models-removal-warning"]').text()).toContain('AccessKey')
    await wrapper.get('[data-test="models-save"]').trigger('click')
    await flushPromises()

    expect(requestMock).not.toHaveBeenCalled()
    const dialog = document.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog?.getAttribute('aria-labelledby')).toBeTruthy()

    clickDocument('[data-test="models-empty-confirm"]')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/7/models', {
      method: 'PUT',
      json: { models: [] },
      signal: expect.any(AbortSignal),
    })
    wrapper.unmount()
  })
})
