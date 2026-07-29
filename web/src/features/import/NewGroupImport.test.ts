import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { flushPromises, mount } from '@vue/test-utils'
import { computed } from 'vue'
import { createMemoryHistory, matchedRouteKey } from 'vue-router'

import type { ApiClient } from '@/api/client'
import { apiClientKey } from '@/api/client-context'
import { ApiError, NetworkError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import type { GroupCreateResult, GroupKeyImportResult } from '@/app/resources/groups'
import { createAppRouter } from '@/app/router'
import { createUnsavedChangesController, unsavedChangesKey } from '@/app/unsaved-changes'
import type { ImportRecoveryService } from '@/features/import/import-recovery'
import type { ImportDraft } from '@/features/import/model-draft'
import { importRecoveryKey } from '@/features/import/import-recovery'
import {
  createImportOperationOwner,
  importOperationOwnerKey,
} from '@/features/import/import-operation-owner'
import { createTestAppI18n as createAppI18n } from '@/test/i18n'
import { waitForRoute } from '@/test/router'

import NewGroupImport from './NewGroupImport.vue'

function groupCreateResult(groupID: number): GroupCreateResult {
  return {
    group_id: groupID,
    group_name: 'Primary',
    keys_added: 1,
    keys_duplicated: 0,
    models: [{ id: 'gpt-4o', alias: '' }],
  }
}

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

async function mountImport(
  request: ApiClient['request'],
  initialDraft?: ImportDraft,
  attachTo?: Element,
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push('/import')
  await router.isReady()
  const activeRouteRecord = computed(() => router.currentRoute.value.matched.at(-1))
  const importRecovery = recovery()
  const operationOwner = createImportOperationOwner()
  const wrapper = mount(NewGroupImport, {
    props: { initialDraft },
    attachTo,
    global: {
      plugins: [
        router,
        createAppI18n(undefined, 'en-US').plugin,
        [VueQueryPlugin, { queryClient }],
      ],
      provide: {
        [apiClientKey as symbol]: { request },
        [importRecoveryKey as symbol]: importRecovery,
        [importOperationOwnerKey as symbol]: operationOwner,
        [unsavedChangesKey as symbol]: createUnsavedChangesController(),
        [matchedRouteKey as symbol]: activeRouteRecord,
      },
    },
  })
  return { importRecovery, operationOwner, queryClient, router, wrapper }
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
  it('focuses the new heading after every explicit step transition', async () => {
    const request = vi.fn().mockResolvedValue({ models: ['gpt-4o'] }) as ApiClient['request']
    const { wrapper } = await mountImport(request, undefined, document.body)
    await enterConnection(wrapper)

    await wrapper.get('[data-test="discover"]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('[data-test="import-step-2-heading"]').element)

    await wrapper.get('[data-test="review"]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('[data-test="import-step-3-heading"]').element)

    const reviewBack = wrapper.findAll('button').find((button) => button.text() === 'Back')
    await reviewBack?.trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('[data-test="import-step-2-heading"]').element)

    const modelsBack = wrapper.findAll('button').find((button) => button.text() === 'Back')
    await modelsBack?.trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('[data-test="import-step-1-heading"]').element)
    wrapper.unmount()
  })

  it('disables conflict Edit while append is pending', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(
        new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'conflict', {
          groups: [{ id: 7, name: 'Existing' }],
        }),
      )
      .mockImplementationOnce(() => new Promise(() => {})) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper)
    await discoverAndReview(wrapper)
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="conflict-append-7"]').trigger('click')

    expect(wrapper.get('[data-test="conflict-edit"]').attributes()).toHaveProperty('disabled')
  })
  it('disables Review Back while create is pending', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockImplementationOnce(() => new Promise(() => {})) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper)
    await discoverAndReview(wrapper)
    await wrapper.get('[data-test="create"]').trigger('click')

    const back = wrapper.findAll('button').find((button) => button.text() === 'Back')
    expect(back?.attributes()).toHaveProperty('disabled')
  })
  it('ignores ordinary discovery rejection after input invalidation', async () => {
    let fail!: (reason: Error) => void
    const request = vi.fn(
      () => new Promise<never>((_resolve, reject) => (fail = reject)),
    ) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper)
    await wrapper.get('[data-test="discover"]').trigger('click')
    await wrapper.get('[data-test="upstream-url"]').setValue('https://changed.example.com')
    fail(new Error('late ordinary failure'))
    await flushPromises()

    expect(wrapper.find('[data-test="manual-path"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Unable to discover')
  })
  it('ignores ordinary discovery resolve after input invalidation', async () => {
    let settle!: (value: { models: string[] }) => void
    const request = vi.fn(
      () => new Promise<{ models: string[] }>((resolve) => (settle = resolve)),
    ) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper)
    await wrapper.get('[data-test="discover"]').trigger('click')
    await wrapper.get('[data-test="upstream-url"]').setValue('https://changed.example.com')
    settle({ models: ['late-model'] })
    await flushPromises()

    expect(wrapper.find('[data-test="manual-path"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('late-model')
    expect(wrapper.text()).not.toContain('Unable to discover')
  })
  it('renders only Group protocols', async () => {
    const { wrapper } = await mountImport(vi.fn() as ApiClient['request'])

    expect(wrapper.find('[data-test="protocol-openai-response"]').exists()).toBe(false)
  })
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

  it('keeps the default HeaderRules config sparse during discovery', async () => {
    const request = vi.fn().mockResolvedValue({ models: [] }) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper, 'raw-key')
    await wrapper.get('[data-test="discover"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/models/discover', {
      method: 'POST',
      json: {
        upstream_url: 'https://api.example.com',
        protocols: ['openai'],
        keys: 'raw-key',
        config: {},
      },
      signal: expect.any(AbortSignal),
    })
  })

  it.each(['X-Test', 'x-test'])(
    'blocks discovery when HeaderRules contain duplicate names ending in %s',
    async (duplicateName) => {
      const request = vi.fn().mockResolvedValue({ models: [] }) as ApiClient['request']
      const { wrapper } = await mountImport(request)
      await enterConnection(wrapper, 'raw-key')
      await wrapper.get('[data-test="add-header-rule"]').trigger('click')
      await wrapper.get('[data-test="header-name"]').setValue('X-Test')
      await wrapper.get('[data-test="add-header-rule"]').trigger('click')
      await wrapper.findAll('[data-test="header-name"]')[1]!.setValue(duplicateName)

      expect(wrapper.get('[data-test="discover"]').attributes()).toHaveProperty('disabled')
    },
  )

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
    const navigation = waitForRoute(router, '/groups/9')
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()
    await navigation

    expect(request).toHaveBeenLastCalledWith('/api/groups', {
      method: 'POST',
      headers: {
        'Idempotency-Key': expect.stringMatching(
          /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
        ),
      },
      json: {
        name: 'Primary',
        upstream_url: 'https://api.example.com',
        protocols: ['openai'],
        models: [{ id: 'manual-model', alias: 'local' }],
        config: {},
        keys: 'raw-authoritative-key',
        confirm_same_upstream_url: false,
      },
      signal: expect.any(AbortSignal),
    })
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.list(),
      exact: true,
    })
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.health(),
      exact: true,
    })
    expect(router.currentRoute.value.fullPath).toBe('/groups/9')
    expect(importRecovery.clear).toHaveBeenCalled()
  })

  it('falls back to fixed generic feedback for malformed conflict data', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(
        new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'must not render', { groups: [] }),
      ) as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper)
    await discoverAndReview(wrapper)
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Unable to create')
    expect(wrapper.find('.conflict').exists()).toBe(false)
  })

  it('uses edited payload and a new identity after an explicit create rejection', async () => {
    const requestMock = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(new ApiError(400, 'VALIDATION_FAILED', 'invalid'))
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockResolvedValueOnce(groupCreateResult(19))
    const request = requestMock as ApiClient['request']
    const { wrapper } = await mountImport(request)
    await enterConnection(wrapper)
    await discoverAndReview(wrapper)
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()

    const reviewBack = wrapper.findAll('button').find((button) => button.text() === 'Back')
    await reviewBack?.trigger('click')
    const modelsBack = wrapper.findAll('button').find((button) => button.text() === 'Back')
    await modelsBack?.trigger('click')
    await wrapper.get('[data-test="group-name"]').setValue('Corrected')
    await wrapper.get('[data-test="discover"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="review"]').trigger('click')
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()

    const createCalls = requestMock.mock.calls.filter(([path]) => path === '/api/groups')
    expect(createCalls).toHaveLength(2)
    expect(createCalls[1]?.[1]?.json).toMatchObject({ name: 'Corrected' })
    expect(createCalls[1]?.[1]?.headers?.['Idempotency-Key']).not.toBe(
      createCalls[0]?.[1]?.headers?.['Idempotency-Key'],
    )
  })

  it.each([
    { groups: [{ id: Number.MAX_SAFE_INTEGER + 1, name: 'Unsafe' }] },
    { groups: [{ id: 7, name: '   ' }] },
  ])(
    'falls back to generic create feedback without navigation for unsafe or blank conflict entries',
    async (data) => {
      const request = vi
        .fn()
        .mockResolvedValueOnce({ models: ['gpt-4o'] })
        .mockRejectedValueOnce(
          new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'must not render', data),
        ) as ApiClient['request']
      const { router, wrapper } = await mountImport(request)
      const push = vi.spyOn(router, 'push')
      await enterConnection(wrapper)
      await discoverAndReview(wrapper)
      await wrapper.get('[data-test="create"]').trigger('click')
      await flushPromises()

      expect(wrapper.text()).toContain('Unable to create')
      expect(wrapper.find('.conflict').exists()).toBe(false)
      expect(push).not.toHaveBeenCalled()
    },
  )
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
    const navigation = waitForRoute(router, '/groups/7')
    await wrapper.get('[data-test="conflict-append-7"]').trigger('click')
    await flushPromises()
    await navigation

    expect(request).toHaveBeenLastCalledWith('/api/groups/7/keys/import', {
      method: 'POST',
      headers: {
        'Idempotency-Key': expect.stringMatching(
          /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
        ),
      },
      json: { keys: 'raw-conflict-key' },
      signal: expect.any(AbortSignal),
    })
    expect(requestMock.mock.calls.filter(([path]) => String(path).includes('/models')).length).toBe(
      1,
    )
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.keys(7),
      exact: true,
    })
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.detail(7),
      exact: true,
    })
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.list(),
      exact: true,
    })
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.health(),
      exact: true,
    })
    expect(router.currentRoute.value.fullPath).toBe('/groups/7')
  })

  it('does not navigate or clear recovery when create invalidations finish after unmount', async () => {
    let resolveInvalidations!: () => void
    const invalidations = new Promise<void>((resolve) => (resolveInvalidations = resolve))
    const request = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockResolvedValueOnce(groupCreateResult(42)) as ApiClient['request']
    const { importRecovery, operationOwner, queryClient, router, wrapper } =
      await mountImport(request)
    const push = vi.spyOn(router, 'push')
    vi.spyOn(queryClient, 'invalidateQueries').mockImplementation(() => invalidations)
    await enterConnection(wrapper, 'CREATE_SUCCESS_SECRET_CANARY')
    await discoverAndReview(wrapper)

    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()
    expect(operationOwner.createGroup.operation.value).toBeNull()
    expect(JSON.stringify(operationOwner.createGroup.operation.value)).not.toContain(
      'CREATE_SUCCESS_SECRET_CANARY',
    )
    wrapper.unmount()
    resolveInvalidations()
    await flushPromises()

    expect(push).not.toHaveBeenCalled()
    expect(importRecovery.clear).not.toHaveBeenCalled()
  })

  it.each(['resolve', 'reject'] as const)(
    'ignores late create %s after unmount before invalidations',
    async (outcome) => {
      let settle!: (value: GroupCreateResult) => void
      let fail!: (reason: Error) => void
      const late = new Promise<GroupCreateResult>((resolve, reject) => {
        settle = resolve
        fail = reject
      })
      const request = vi
        .fn()
        .mockResolvedValueOnce({ models: ['gpt-4o'] })
        .mockImplementationOnce(() => late) as ApiClient['request']
      const { importRecovery, queryClient, router, wrapper } = await mountImport(request)
      const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
      const push = vi.spyOn(router, 'push')
      await enterConnection(wrapper)
      await discoverAndReview(wrapper)
      await wrapper.get('[data-test="create"]').trigger('click')
      wrapper.unmount()
      if (outcome === 'resolve') settle(groupCreateResult(42))
      else fail(new Error('late ordinary failure'))
      await flushPromises()

      expect(invalidate).not.toHaveBeenCalled()
      expect(push).not.toHaveBeenCalled()
      expect(importRecovery.clear).not.toHaveBeenCalled()
      expect(document.body.textContent).not.toContain('Unable to create')
    },
  )

  it('does not navigate or clear recovery when append invalidations finish after unmount', async () => {
    let resolveInvalidations!: () => void
    const invalidations = new Promise<void>((resolve) => (resolveInvalidations = resolve))
    const request = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(
        new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'conflict', {
          groups: [{ id: 7, name: 'Existing' }],
        }),
      )
      .mockResolvedValueOnce({
        group_id: 7,
        keys_added: 1,
        keys_duplicated: 0,
      }) as ApiClient['request']
    const { importRecovery, operationOwner, queryClient, router, wrapper } =
      await mountImport(request)
    const push = vi.spyOn(router, 'push')
    vi.spyOn(queryClient, 'invalidateQueries').mockImplementation(() => invalidations)
    await enterConnection(wrapper, 'APPEND_SUCCESS_SECRET_CANARY')
    await discoverAndReview(wrapper)
    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="conflict-append-7"]').trigger('click')
    await flushPromises()
    expect(operationOwner.importKeys.operation.value).toBeNull()
    expect(JSON.stringify(operationOwner.importKeys.operation.value)).not.toContain(
      'APPEND_SUCCESS_SECRET_CANARY',
    )
    wrapper.unmount()
    resolveInvalidations()
    await flushPromises()

    expect(push).not.toHaveBeenCalled()
    expect(importRecovery.clear).not.toHaveBeenCalled()
  })

  it.each(['resolve', 'reject'] as const)(
    'ignores late append %s after unmount before invalidations',
    async (outcome) => {
      let settle!: (value: GroupKeyImportResult) => void
      let fail!: (reason: Error) => void
      const late = new Promise<GroupKeyImportResult>((resolve, reject) => {
        settle = resolve
        fail = reject
      })
      const request = vi
        .fn()
        .mockResolvedValueOnce({ models: ['gpt-4o'] })
        .mockRejectedValueOnce(
          new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'conflict', {
            groups: [{ id: 7, name: 'Existing' }],
          }),
        )
        .mockImplementationOnce(() => late) as ApiClient['request']
      const { importRecovery, queryClient, router, wrapper } = await mountImport(request)
      const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
      const push = vi.spyOn(router, 'push')
      await enterConnection(wrapper)
      await discoverAndReview(wrapper)
      await wrapper.get('[data-test="create"]').trigger('click')
      await flushPromises()
      await wrapper.get('[data-test="conflict-append-7"]').trigger('click')
      wrapper.unmount()
      if (outcome === 'resolve') {
        settle({ group_id: 7, keys_added: 1, keys_duplicated: 0 })
      } else fail(new Error('late ordinary failure'))
      await flushPromises()

      expect(invalidate).not.toHaveBeenCalled()
      expect(push).not.toHaveBeenCalled()
      expect(importRecovery.clear).not.toHaveBeenCalled()
      expect(document.body.textContent).not.toContain('Unable to create')
    },
  )

  it('resubmits a structured conflict only after explicit separate-Group confirmation', async () => {
    const conflict = new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'conflict', {
      groups: [{ id: 7, name: 'Existing' }],
    })
    const requestMock = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(conflict)
      .mockResolvedValueOnce(groupCreateResult(10))
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

  it('checks an indeterminate create with the exact same UUID and frozen payload', async () => {
    const requestMock = vi
      .fn()
      .mockResolvedValueOnce({ models: ['gpt-4o'] })
      .mockRejectedValueOnce(new NetworkError())
      .mockResolvedValueOnce(groupCreateResult(23))
    const { router, wrapper } = await mountImport(requestMock as ApiClient['request'])
    await enterConnection(wrapper, 'stable-raw-key')
    await discoverAndReview(wrapper)

    await wrapper.get('[data-test="create"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="import-operation-notice"]').text()).toContain(
      'could not be confirmed',
    )

    const first = requestMock.mock.calls[1]?.[1]
    const navigation = waitForRoute(router, '/groups/23')
    await wrapper.get('[data-test="import-operation-retry"]').trigger('click')
    await flushPromises()
    await navigation
    const second = requestMock.mock.calls[2]?.[1]

    expect(first?.headers?.['Idempotency-Key']).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    )
    expect(second?.headers?.['Idempotency-Key']).toBe(first?.headers?.['Idempotency-Key'])
    expect(second?.json).toEqual(first?.json)
    expect(router.currentRoute.value.fullPath).toBe('/groups/23')
  })
})
