import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient } from '@/api/client'
import { apiClientKey } from '@/api/client-context'
import { ApiError, NetworkError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { createAppRouter } from '@/app/router'
import {
  createDirtyNavigationController,
  dirtyNavigationKey,
} from '@/features/import/use-dirty-navigation'
import type { ImportRecoveryService } from '@/features/import/import-recovery'
import type { ImportDraft } from '@/features/import/model-draft'
import { importRecoveryKey } from '@/features/import/import-recovery'
import { createAppI18n } from '@/i18n'

import NewGroupImport from './NewGroupImport.vue'

function recovery(): ImportRecoveryService {
  return {
    register: () => () => {},
    captureForUnauthorized: () => 'no-active-draft',
    consume: () => null,
    clear: vi.fn(),
    sweep: () => {},
    dispose: () => {},
  }
}

async function mountImport(request: ApiClient['request'], initialDraft?: ImportDraft) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push('/import')
  await router.isReady()
  const importRecovery = recovery()
  const wrapper = mount(NewGroupImport, {
    props: { initialDraft },
    global: {
      plugins: [
        router,
        createAppI18n(undefined, 'en-US').plugin,
        [VueQueryPlugin, { queryClient }],
      ],
      provide: {
        [apiClientKey as symbol]: { request },
        [importRecoveryKey as symbol]: importRecovery,
        [dirtyNavigationKey as symbol]: createDirtyNavigationController(),
      },
    },
  })
  return { importRecovery, queryClient, router, wrapper }
}

async function enterConnection(wrapper: ReturnType<typeof mount>, keys = 'raw-key\nraw-key') {
  await wrapper.get('[data-test="preset"]').setValue('custom')
  await wrapper.get('[data-test="group-name"]').setValue('Primary')
  await wrapper.get('[data-test="upstream-url"]').setValue('https://api.example.com')
  await wrapper.get('[data-test="protocol-openai"]').setValue(true)
  await wrapper.get('[data-test="keys"]').setValue(keys)
}

async function discoverAndReview(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('[data-test="discover"]').trigger('click')
  await flushPromises()
  await wrapper.get('[data-test="review"]').trigger('click')
}

describe('NewGroupImport', () => {
  it('immediately exposes manual model entry for a recovered step-2 draft with no models', async () => {
    const request = vi.fn() as ApiClient['request']
    const recovered: ImportDraft = {
      mode: 'new',
      step: 2,
      preset_id: 'custom',
      name: 'Recovered',
      upstream_url: 'https://api.example.com',
      protocols: ['openai'],
      keys: 'recovered-key',
      header_rules: { set: {}, remove: [] },
      models: [],
    }

    const { wrapper } = await mountImport(request, recovered)

    expect(wrapper.get('[data-test="manual-model-id"]')).toBeDefined()
    expect(wrapper.find('[data-test="manual-path"]').exists()).toBe(false)
    expect(request).not.toHaveBeenCalled()
  })

  it('applies presets, keeps Custom editable, sends raw discovery input, and shows non-blocking key hints', async () => {
    const request = vi.fn().mockResolvedValue({ models: ['gpt-4o'] }) as ApiClient['request']
    const { wrapper } = await mountImport(request)

    await wrapper.get('[data-test="preset"]').setValue('anthropic')
    expect((wrapper.get('[data-test="upstream-url"]').element as HTMLInputElement).value).toBe(
      'https://api.anthropic.com',
    )
    expect(
      (wrapper.get('[data-test="protocol-anthropic"]').element as HTMLInputElement).checked,
    ).toBe(true)
    await enterConnection(wrapper, 'raw-key\nraw-key\nsk-gl-warning')
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.get('[data-test="header-name"]').setValue('X-Test')
    await wrapper.get('[data-test="header-value"]').setValue('secret')

    expect(wrapper.text()).toContain('duplicate')
    expect(wrapper.text()).toContain('AccessKey')
    expect(wrapper.get('[data-test="discover"]').attributes()).not.toHaveProperty('disabled')
    await wrapper.get('[data-test="discover"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/models/discover', {
      method: 'POST',
      json: {
        upstream_url: 'https://api.example.com',
        protocols: ['openai'],
        keys: 'raw-key\nraw-key\nsk-gl-warning',
        config: { header_rules: { set: { 'X-Test': 'secret' }, remove: [] } },
      },
      signal: expect.any(AbortSignal),
    })
    expect(wrapper.text()).toContain('gpt-4o')
  })

  it('keeps secret canaries out of routes, query keys, mutation cache, and rendered text', async () => {
    const request = vi.fn().mockRejectedValue(new NetworkError()) as ApiClient['request']
    const { queryClient, router, wrapper } = await mountImport(request)
    await enterConnection(wrapper, 'UPSTREAM_KEY_CANARY_91e7')
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.get('[data-test="header-name"]').setValue('X-Canary')
    await wrapper.get('[data-test="header-value"]').setValue('HEADER_CANARY_57aa')
    await wrapper.get('[data-test="discover"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).not.toContain('UPSTREAM_KEY_CANARY_91e7')
    expect(router.currentRoute.value.fullPath).not.toContain('HEADER_CANARY_57aa')
    expect(
      JSON.stringify(
        queryClient
          .getQueryCache()
          .getAll()
          .map((query) => query.queryKey),
      ),
    ).not.toContain('CANARY')
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
    expect(wrapper.text()).not.toContain('UPSTREAM_KEY_CANARY_91e7')
    expect(wrapper.text()).not.toContain('HEADER_CANARY_57aa')
  })

  it('blocks discovery when HeaderRules contain case-insensitive duplicate names', async () => {
    const request = vi.fn().mockResolvedValue({ models: [] }) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper, 'raw-key')
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.get('[data-test="header-name"]').setValue('X-Test')
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.findAll('[data-test="header-name"]')[1]!.setValue('x-test')

    expect(wrapper.get('[data-test="discover"]').attributes()).toHaveProperty('disabled')
  })

  it('aborts an in-flight discovery and requires rediscovery when connection input changes', async () => {
    const request = vi.fn(() => new Promise(() => {})) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper, 'raw-key')
    await wrapper.get('[data-test="discover"]').trigger('click')
    const signal = (vi.mocked(request).mock.calls[0]?.[1] as { signal: AbortSignal }).signal

    await wrapper.get('[data-test="upstream-url"]').setValue('https://changed.example.com')

    expect(signal.aborted).toBe(true)
    expect(wrapper.find('[data-test="review"]').exists()).toBe(false)
  })

  it('keeps a manual-model path after discovery failure and creates with exact invalidations', async () => {
    const request = vi
      .fn()
      .mockRejectedValueOnce(new NetworkError())
      .mockResolvedValueOnce({
        group_id: 9,
        group_name: 'Primary',
        keys_added: 1,
        keys_duplicated: 0,
        models: [{ id: 'manual-model', alias: 'local' }],
      }) as ApiClient['request']
    const { importRecovery, queryClient, router, wrapper } = await mountImport(request)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await enterConnection(wrapper, 'raw-authoritative-key')
    await wrapper.get('[data-test="discover"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="manual-path"]').exists()).toBe(true)
    await wrapper.get('[data-test="manual-path"]').trigger('click')
    await wrapper.get('[data-test="manual-model-id"]').setValue('manual-model')
    await wrapper.get('[data-test="manual-model-alias"]').setValue('local')
    await wrapper.get('[data-test="add-manual-model"]').trigger('click')
    await wrapper.get('[data-test="review"]').trigger('click')
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenLastCalledWith('/api/groups', {
      method: 'POST',
      json: {
        name: 'Primary',
        upstream_url: 'https://api.example.com',
        protocols: ['openai'],
        models: [{ id: 'manual-model', alias: 'local' }],
        config: { header_rules: { set: {}, remove: [] } },
        keys: 'raw-authoritative-key',
        confirm_same_upstream_url: false,
      },
      signal: expect.any(AbortSignal),
    })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: controlQueryKeys.groups.list() })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: controlQueryKeys.health() })
    expect(router.currentRoute.value.fullPath).toBe('/groups/9')
    expect(importRecovery.clear).toHaveBeenCalled()
  })

  it('guards conflict data and appends raw keys without group update or model endpoints', async () => {
    const requestMock = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(
        new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'conflict', {
          groups: [{ id: 7, name: 'Existing' }],
        }),
      )
      .mockResolvedValueOnce({ group_id: 7, keys_added: 1, keys_duplicated: 0 })
    const request = requestMock as ApiClient['request']
    const { queryClient, router, wrapper } = await mountImport(request)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await enterConnection(wrapper, 'raw-conflict-key')
    await discoverAndReview(wrapper)
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Existing')
    expect(wrapper.find('[data-test="conflict-confirm-separate"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="conflict-edit"]').exists()).toBe(true)
    await wrapper.get('[data-test="conflict-append-7"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenLastCalledWith('/api/groups/7/keys/import', {
      method: 'POST',
      json: { keys: 'raw-conflict-key' },
      signal: expect.any(AbortSignal),
    })
    expect(requestMock.mock.calls.filter(([path]) => String(path).includes('/models')).length).toBe(
      1,
    )
    expect(invalidate).toHaveBeenCalledWith({ queryKey: controlQueryKeys.groups.keys(7) })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: controlQueryKeys.groups.detail(7) })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: controlQueryKeys.groups.list() })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: controlQueryKeys.health() })
    expect(router.currentRoute.value.fullPath).toBe('/groups/7')
  })

  it('resubmits a structured conflict only after explicit separate-Group confirmation', async () => {
    const conflict = new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'conflict', {
      groups: [{ id: 7, name: 'Existing' }],
    })
    const requestMock = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(conflict)
      .mockResolvedValueOnce({ group_id: 10 })
    const request = requestMock as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper)
    await discoverAndReview(wrapper)
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="conflict-confirm-separate"]').trigger('click')
    await flushPromises()
    expect(requestMock.mock.calls[2]?.[1]).toMatchObject({
      json: { confirm_same_upstream_url: true },
    })
  })
})
