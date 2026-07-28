import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'
import { defineComponent, h, toRef, type PropType } from 'vue'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import type { RuntimeSettingKey, SettingsDto, TimeoutSettingKey } from '@/api/control/settings'
import { ApiError, NetworkError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import type { SettingsResource } from '@/app/resources/settings'
import { mountApp } from '@/test/mount-app'
import { apiWithResponseMetadata } from '@/test/api-response'

import type { SettingsDraft } from './settings-patch'
import { useSettingsController, type SettingsPageController } from './use-settings-controller'

const fixedNow = new Date('2026-07-28T14:00:00.000Z')
const tokens = {
  base: `sha256-${'a'.repeat(64)}`,
  saved: `sha256-${'b'.repeat(64)}`,
  unrelated: `sha256-${'c'.repeat(64)}`,
  latest: `sha256-${'d'.repeat(64)}`,
  localized: `sha256-${'e'.repeat(64)}`,
} as const

function settings(
  requestTimeout = 600,
  retentionDays = 7,
  overrides: RuntimeSettingKey[] = [],
): SettingsDto {
  return {
    values: {
      connect_timeout: 15,
      first_byte_timeout: 120,
      request_timeout: requestTimeout,
      stream_idle_timeout: 300,
      header_rules: { set: {}, remove: [] },
      inject_usage_options: false,
      request_log_retention_days: retentionDays,
    },
    overrides,
  }
}

function resource(settingsValue: SettingsDto, settingsETag = tokens.base): SettingsResource {
  return { settings: settingsValue, settings_etag: settingsETag }
}

function cloneDraft(draft: SettingsDraft): SettingsDraft {
  return {
    values: {
      ...draft.values,
      header_rules: {
        set: { ...draft.values.header_rules.set },
        remove: [...draft.values.header_rules.remove],
      },
    },
    overrides: new Set(draft.overrides),
  }
}

function changeNumber(
  controller: SettingsPageController,
  key: TimeoutSettingKey | 'request_log_retention_days',
  value: number,
): void {
  const current = controller.draft.value
  if (!current) throw new Error('missing Settings draft')
  const draft = cloneDraft(current)
  draft.values[key] = value
  draft.overrides.add(key)
  controller.updateDraft({ key, draft })
}

async function mountController(
  request: ApiClient['request'],
  options: {
    initial?: SettingsResource
    queryClient?: QueryClient
    tokenFor?: (path: ApiPath, requestOptions: ApiRequestOptions | undefined) => string
  } = {},
) {
  const initial = options.initial ?? resource(settings())
  const queryClient =
    options.queryClient ??
    new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
  queryClient.setQueryData(controlQueryKeys.settings('en-US'), initial)
  let controller: SettingsPageController | undefined
  const Harness = defineComponent({
    props: {
      resource: {
        type: Object as PropType<SettingsResource>,
        required: true,
      },
    },
    setup(props) {
      controller = useSettingsController(toRef(props, 'resource'), {
        now: () => new Date(fixedNow),
      })
      return () => h('div')
    },
  })
  const mounted = await mountApp(Harness, {
    api: apiWithResponseMetadata(
      request,
      (path, requestOptions) =>
        options.tokenFor?.(path, requestOptions) ??
        (requestOptions?.method === 'PUT' ? tokens.saved : tokens.base),
    ),
    queryClient,
    locale: 'en-US',
    mounting: { props: { resource: initial } },
  })
  if (!controller) throw new Error('Settings controller was not created')
  return { ...mounted, controller, queryClient }
}

describe('useSettingsController', () => {
  it('saves both sections in one PUT and rebases the exact localized cache once', async () => {
    const saved = settings(900, 30, ['request_timeout', 'request_log_retention_days'])
    const request = vi.fn(async () => saved) as ApiClient['request']
    const { controller, queryClient, wrapper } = await mountController(request)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    changeNumber(controller, 'request_timeout', 900)
    changeNumber(controller, 'request_log_retention_days', 30)
    await controller.saveAll()

    expect(request).toHaveBeenCalledOnce()
    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      headers: { 'If-Match': `"${tokens.base}"` },
      json: {
        settings: {
          request_timeout: 900,
          request_log_retention_days: 30,
        },
      },
      signal: expect.any(AbortSignal),
    })
    expect(controller.base.value).toEqual(resource(saved, tokens.saved))
    expect(controller.patch.value).toEqual({})
    expect(controller.dirty.value).toBe(false)
    expect(controller.savedAt.value).toEqual(fixedNow)
    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toEqual(
      resource(saved, tokens.saved),
    )
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.details() },
    ])
    wrapper.unmount()
  })

  it('performs one all-scope three-way merge for a 412 response', async () => {
    const latest = settings(1_200, 7, ['request_timeout'])
    const request = vi.fn(async () => {
      throw new ApiError(412, 'SETTINGS_VERSION_CONFLICT', 'conflict', {
        settings: latest,
        settings_etag: tokens.latest,
      })
    }) as ApiClient['request']
    const { controller, queryClient, wrapper } = await mountController(request)

    changeNumber(controller, 'request_timeout', 900)
    changeNumber(controller, 'request_log_retention_days', 30)
    await controller.saveAll()

    expect(controller.base.value).toEqual(resource(latest, tokens.latest))
    expect(controller.draft.value?.values.request_timeout).toBe(900)
    expect(controller.draft.value?.values.request_log_retention_days).toBe(30)
    expect(controller.conflicts.value.map(({ key }) => key)).toEqual(['request_timeout'])
    expect(controller.savedAt.value).toBeNull()
    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toEqual(
      resource(latest, tokens.latest),
    )
    wrapper.unmount()
  })

  it('forces an exact refetch when the cached ETag is unrelated to the operation', async () => {
    const saved = settings(900, 7, ['request_timeout'])
    const request = vi.fn(async () => saved) as ApiClient['request']
    const { controller, queryClient, wrapper } = await mountController(request)
    const refetch = vi.spyOn(queryClient, 'refetchQueries').mockResolvedValue()
    queryClient.setQueryData(
      controlQueryKeys.settings('en-US'),
      resource(settings(1_500), tokens.unrelated),
    )

    changeNumber(controller, 'request_timeout', 900)
    await controller.saveAll()

    expect(refetch).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings('en-US'),
      exact: true,
    })
    expect(controller.savedAt.value).toBeNull()
    wrapper.unmount()
  })

  it('reconciles a network-unknown PUT with GET without resending the mutation', async () => {
    const applied = settings(900, 30, ['request_timeout', 'request_log_retention_days'])
    const requestMock = vi
      .fn()
      .mockRejectedValueOnce(new NetworkError())
      .mockResolvedValueOnce(applied)
    const request = requestMock as ApiClient['request']
    const { controller, queryClient, wrapper } = await mountController(request, {
      tokenFor: (_path, requestOptions) =>
        requestOptions?.method === 'PUT' ? tokens.saved : tokens.latest,
    })
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    changeNumber(controller, 'request_timeout', 900)
    changeNumber(controller, 'request_log_retention_days', 30)
    await controller.saveAll()

    expect(
      requestMock.mock.calls.map(
        (call) => (call[1] as ApiRequestOptions | undefined)?.method ?? 'GET',
      ),
    ).toEqual(['PUT', 'GET'])
    expect(controller.indeterminate.value).toBe(false)
    expect(controller.reconciling.value).toBe(false)
    expect(controller.dirty.value).toBe(false)
    expect(controller.savedAt.value).toEqual(fixedNow)
    expect(invalidate).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('discards every field and preserves dirty fields across a localized ETag rebase', async () => {
    const request = vi.fn() as ApiClient['request']
    const initial = resource(settings())
    const { controller, wrapper } = await mountController(request, { initial })

    changeNumber(controller, 'request_timeout', 900)
    changeNumber(controller, 'request_log_retention_days', 30)
    await wrapper.setProps({ resource: resource(settings(), tokens.localized) })
    await flushPromises()

    expect(controller.base.value?.settings_etag).toBe(tokens.localized)
    expect(controller.patch.value).toEqual({
      request_timeout: 900,
      request_log_retention_days: 30,
    })
    expect(controller.conflicts.value).toEqual([])

    controller.discard()

    expect(controller.patch.value).toEqual({})
    expect(controller.dirty.value).toBe(false)
    expect(controller.draft.value?.values.request_timeout).toBe(600)
    expect(controller.draft.value?.values.request_log_retention_days).toBe(7)
    wrapper.unmount()
  })
})
