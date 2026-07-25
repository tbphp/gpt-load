import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import type { RouteInspectReasonCode, RouteInspectResponseDto } from '@/api/control/route-inspect'
import type { AccessKeyDto } from '@/api/control/types'
import { ApiError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import AppSelect from '@/components/ui/AppSelect.vue'
import { mountApp } from '@/test/mount-app'

import InspectorTab from './InspectorTab.vue'

const activeAccessKey: AccessKeyDto = {
  id: 12,
  name: 'Client production',
  key: 'sk-gl-raw-access-key-canary',
  status: 'active',
  filters: { groups: [], protocols: [], models: [] },
  rpm_limit: 0,
}

const disabledAccessKey: AccessKeyDto = {
  id: 13,
  name: 'Client disabled',
  key: 'sk-gl-disabled-raw-canary',
  status: 'disabled',
  filters: { groups: [], protocols: [], models: [] },
  rpm_limit: 0,
}

function inspectionFixture(
  overrides: Partial<RouteInspectResponseDto> = {},
): RouteInspectResponseDto {
  return {
    observed_at: '2026-07-25T10:00:00Z',
    snapshot_revision: 42,
    protocol: 'openai',
    external_model: 'gpt-client',
    access_key: {
      id: 12,
      name: 'Client production',
      status: 'active',
    },
    routable: true,
    reason_code: null,
    groups: [],
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, reject, resolve }
}

class InspectorApi implements ApiClient {
  readonly requests: Array<{ path: ApiPath; options?: ApiRequestOptions }> = []
  private inspectionIndex = 0

  constructor(
    private readonly inspections: Array<
      RouteInspectResponseDto | Promise<RouteInspectResponseDto>
    > = [inspectionFixture()],
    private readonly accessKeys: AccessKeyDto[] | Promise<AccessKeyDto[]> = [
      activeAccessKey,
      disabledAccessKey,
    ],
  ) {}

  request<T>(path: ApiPath, options?: ApiRequestOptions): Promise<T> {
    this.requests.push({ path, options })
    if (path === '/api/access-keys') {
      return Promise.resolve(this.accessKeys as T)
    }
    if (path === '/api/route/inspect') {
      const response = this.inspections[this.inspectionIndex] ?? this.inspections.at(-1)
      this.inspectionIndex += 1
      return Promise.resolve(response as T)
    }
    throw new Error(`Unexpected request: ${path}`)
  }
}

function createQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountInspector(
  api: ApiClient,
  path = '/monitor?tab=inspector',
  locale: 'zh-CN' | 'en-US' | 'ja-JP' = 'en-US',
) {
  const queryClient = createQueryClient()
  const mounted = await mountApp(InspectorTab, {
    api,
    queryClient,
    path,
    locale,
    mounting: { attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient }
}

async function selectValue(
  wrapper: Awaited<ReturnType<typeof mountInspector>>['wrapper'],
  index: number,
  value: string,
): Promise<void> {
  wrapper.findAllComponents(AppSelect)[index]?.vm.$emit('update:modelValue', value)
  await flushPromises()
}

function inspectionRequests(api: InspectorApi) {
  return api.requests.filter(({ path }) => path === '/api/route/inspect')
}

describe('InspectorTab', () => {
  it('prefills a deep link without inspecting and keeps a disabled AccessKey selectable', async () => {
    const api = new InspectorApi()
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=anthropic&external_model=claude-exact&access_key_id=13',
    )

    const selects = wrapper.findAllComponents(AppSelect)
    expect(selects[0]?.props('modelValue')).toBe('anthropic')
    expect(selects[1]?.props('modelValue')).toBe('13')
    expect(wrapper.get<HTMLInputElement>('[data-test="inspector-model"]').element.value).toBe(
      'claude-exact',
    )
    expect(selects[1]?.props('options')).toContainEqual({
      value: '13',
      label: 'Client disabled · #13 · Disabled',
    })
    expect(inspectionRequests(api)).toEqual([])
  })

  it.each([
    ['missing protocol', '', 'gpt-client', '12'],
    ['missing model', 'openai', '', '12'],
    ['whitespace-wrapped model', 'openai', ' gpt-client ', '12'],
    ['invalid protocol', 'legacy', 'gpt-client', '12'],
    ['missing AccessKey', 'openai', 'gpt-client', ''],
  ])('rejects %s locally without sending a request', async (_name, protocol, model, keyID) => {
    const api = new InspectorApi()
    const { router, wrapper } = await mountInspector(api)

    await selectValue(wrapper, 0, protocol)
    await wrapper.get('[data-test="inspector-model"]').setValue(model)
    await selectValue(wrapper, 1, keyID)
    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()

    expect(inspectionRequests(api)).toEqual([])
    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=inspector')
    expect(wrapper.find('[data-test="inspector-validation-error"]').exists()).toBe(true)
  })

  it('posts the exact strict body only on explicit valid submit and allowlists the URL', async () => {
    const api = new InspectorApi([
      inspectionFixture({
        protocol: 'anthropic',
        external_model: 'claude-exact',
        access_key: { id: 13, name: 'Client disabled', status: 'disabled' },
      }),
    ])
    const { router, wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=anthropic&external_model=claude-exact&access_key_id=13&raw_access_key=sk-gl-route-canary',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()

    expect(inspectionRequests(api)).toEqual([
      {
        path: '/api/route/inspect',
        options: {
          method: 'POST',
          json: {
            protocol: 'anthropic',
            external_model: 'claude-exact',
            access_key_id: 13,
          },
          signal: expect.any(AbortSignal),
        },
      },
    ])
    expect(router.currentRoute.value.query).toEqual({
      tab: 'inspector',
      protocol: 'anthropic',
      external_model: 'claude-exact',
      access_key_id: '13',
    })
    expect(JSON.stringify(router.currentRoute.value.query)).not.toContain('sk-gl-route-canary')
    expect(wrapper.get('[data-test="inspector-access-key-status"]').classes()).toContain(
      'status-badge--neutral',
    )
  })

  it('requires reselection when a deep-linked AccessKey no longer exists', async () => {
    const api = new InspectorApi()
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=99',
    )

    expect(wrapper.get('[data-test="inspector-access-key-missing"]').text()).toContain(
      'no longer exists',
    )
    expect(wrapper.get('[data-test="inspector-access-key-missing"]').text()).toContain('#99')
    expect(wrapper.findAllComponents(AppSelect)[1]?.props('options')).toContainEqual({
      value: '99',
      label: 'Missing AccessKey · #99',
    })
    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()
    expect(inspectionRequests(api)).toEqual([])

    await selectValue(wrapper, 1, '12')
    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()
    expect(inspectionRequests(api)).toHaveLength(1)
  })

  it('aborts the previous submit and lets only the newest owner publish a result', async () => {
    const first = deferred<RouteInspectResponseDto>()
    const second = deferred<RouteInspectResponseDto>()
    const api = new InspectorApi([first.promise, second.promise])
    const { router, wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=first-model&access_key_id=12',
    )

    const submit = wrapper.get('[data-test="inspector-submit"]')
    await submit.trigger('click')
    await flushPromises()
    const firstRequest = inspectionRequests(api)[0]
    expect(firstRequest?.options?.signal?.aborted).toBe(false)
    expect(submit.attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-test="inspector-model"]').setValue('second-model')
    await submit.trigger('click')
    await flushPromises()
    expect(firstRequest?.options?.signal?.aborted).toBe(true)

    second.resolve(
      inspectionFixture({
        external_model: 'second-model',
        groups: [
          {
            group_id: 2,
            group_name: 'Current Group',
            upstream_model: 'current-upstream',
            weight_manual: null,
            included: true,
            routable: true,
            reason_code: null,
            keys: [],
          },
        ],
      }),
    )
    await flushPromises()
    expect(wrapper.text()).toContain('Current Group')

    first.resolve(
      inspectionFixture({
        external_model: 'first-model',
        groups: [
          {
            group_id: 1,
            group_name: 'Late Group',
            upstream_model: 'late-upstream',
            weight_manual: null,
            included: true,
            routable: true,
            reason_code: null,
            keys: [],
          },
        ],
      }),
    )
    await flushPromises()

    expect(wrapper.text()).toContain('Current Group')
    expect(wrapper.text()).not.toContain('Late Group')
    expect(router.currentRoute.value.query.external_model).toBe('second-model')
  })

  it('aborts an in-flight request and removes result state on unmount', async () => {
    const pending = deferred<RouteInspectResponseDto>()
    const api = new InspectorApi([pending.promise])
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()
    const request = inspectionRequests(api)[0]
    expect(request?.options?.signal?.aborted).toBe(false)

    wrapper.unmount()
    expect(request?.options?.signal?.aborted).toBe(true)

    pending.resolve(inspectionFixture({ external_model: 'late-unmounted-result-canary' }))
    await flushPromises()
    expect(document.body.textContent).not.toContain('late-unmounted-result-canary')
  })

  it('marks a successful observation as input-changed after the draft is edited', async () => {
    const api = new InspectorApi()
    const { router, wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-test="inspector-input-changed"]').exists()).toBe(false)

    await wrapper.get('[data-test="inspector-model"]').setValue('edited-model')
    await flushPromises()

    expect(wrapper.get('[data-test="inspector-input-changed"]').text()).toContain(
      'inputs have changed',
    )
    expect(wrapper.find('[data-test="inspector-result"]').exists()).toBe(true)
    expect(router.currentRoute.value.query.external_model).toBe('gpt-client')
  })

  it('renders current-runtime facts, raw weights, and upstream Key identities without selection claims', async () => {
    const upstreamMask = 'upstream-mask-never-render'
    const upstreamName = 'upstream-name-never-render'
    const api = new InspectorApi([
      inspectionFixture({
        groups: [
          {
            group_id: 7,
            group_name: 'Primary Group',
            upstream_model: 'provider/model-exact',
            weight_manual: null,
            included: true,
            routable: true,
            reason_code: null,
            keys: [
              {
                key_id: 31,
                available: true,
                reason_code: null,
                weight_manual: null,
                weight_auto: 37,
                effective_weight: 1850,
                cooldown_until: null,
                name: upstreamName,
                mask: upstreamMask,
              },
              {
                key_id: 32,
                available: false,
                reason_code: 'key_cooldown',
                weight_manual: 7,
                weight_auto: 99,
                effective_weight: 0,
                cooldown_until: '2026-07-25T10:02:00Z',
              },
            ],
          },
        ],
      } as Partial<RouteInspectResponseDto>),
    ])
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()

    const result = wrapper.get('[data-test="inspector-result"]')
    expect(result.text()).toContain('Current runtime observation')
    expect(result.text()).toContain('Observed at 2026-07-25T10:00:00Z')
    expect(result.text()).toContain('Revision 42')
    expect(result.text()).toContain('Read-only')
    expect(result.text()).toContain('No upstream request is sent')
    expect(result.text()).toContain('No tokens are consumed')
    expect(result.text()).toContain('Routable')
    expect(result.text()).toContain('Primary Group')
    expect(result.text()).toContain('provider/model-exact')
    expect(result.text()).toContain('Key #31')
    expect(result.text()).toContain('Key #32')
    expect(wrapper.get('[data-test="inspector-access-key-status"]').classes()).toContain(
      'status-badge--success',
    )
    expect(wrapper.get('[data-test="inspector-observed-at"]').attributes('datetime')).toBe(
      '2026-07-25T10:00:00Z',
    )
    expect(wrapper.get('[data-test="inspector-key-32-cooldown"]').attributes('datetime')).toBe(
      '2026-07-25T10:02:00Z',
    )
    expect(wrapper.get('[data-test="inspector-key-31-manual-weight"]').text()).toContain('null')
    expect(wrapper.get('[data-test="inspector-key-31-auto-weight"]').text()).toContain('37')
    expect(wrapper.get('[data-test="inspector-key-31-effective-weight"]').text()).toContain('1850')
    expect(wrapper.get('[data-test="inspector-key-32-manual-weight"]').text()).toContain('7')
    expect(result.text()).not.toContain(upstreamMask)
    expect(result.text()).not.toContain(upstreamName)
    expect(result.text().toLowerCase()).not.toContain('selected')
    expect(result.text().toLowerCase()).not.toContain('predicted')
  })

  it('treats a 200 non-routable response with groups=[] as a complete result', async () => {
    const api = new InspectorApi([
      inspectionFixture({
        routable: false,
        reason_code: 'no_route_target',
        groups: [],
      }),
    ])
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=missing-model&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-test="inspector-result-state"]').text()).toContain('Not routable')
    expect(wrapper.get('[data-test="inspector-result-reason"]').text()).toContain(
      'No route target matches this model and protocol',
    )
    expect(wrapper.get('[data-test="inspector-groups-complete-empty"]').text()).toContain(
      'complete current-runtime explanation',
    )
    expect(wrapper.find('[data-test="inspector-request-error"]').exists()).toBe(false)
  })

  it('maps all 14 reason codes and uses a safe fallback for an unknown code', async () => {
    const reasons: Array<[RouteInspectReasonCode, string]> = [
      ['access_key_disabled', 'AccessKey is disabled'],
      ['protocol_filtered', 'AccessKey filters exclude this protocol'],
      ['model_filtered', 'AccessKey filters exclude this model'],
      ['no_route_target', 'No route target matches this model and protocol'],
      ['group_disabled', 'Group is disabled'],
      ['group_filtered', 'AccessKey filters exclude this Group'],
      ['no_available_group', 'No candidate Group is currently routable'],
      ['no_keys', 'Group has no runtime keys'],
      ['group_weight_zero', 'Group effective weight is zero'],
      ['key_disabled', 'Key is disabled'],
      ['key_blacklisted', 'Key is blacklisted'],
      ['key_cooldown', 'Key is cooling down'],
      ['key_weight_zero', 'Key effective weight is zero'],
      ['no_available_key', 'Group has no currently available Key'],
    ]
    const unknownReason = 'future-reason-secret-canary'
    const api = new InspectorApi([
      inspectionFixture({
        routable: false,
        reason_code: reasons[0]![0],
        groups: [
          ...reasons.slice(1).map(([reason], index) => ({
            group_id: index + 1,
            group_name: `Group ${index + 1}`,
            upstream_model: `upstream-${index + 1}`,
            weight_manual: null,
            included: false,
            routable: false,
            reason_code: reason,
            keys: [],
          })),
          {
            group_id: 99,
            group_name: 'Future Group',
            upstream_model: 'future-upstream',
            weight_manual: null,
            included: false,
            routable: false,
            reason_code: unknownReason,
            keys: [],
          },
        ],
      } as Partial<RouteInspectResponseDto>),
    ])
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()

    for (const [, label] of reasons) expect(wrapper.text()).toContain(label)
    expect(wrapper.text()).toContain('Unknown route reason')
    expect(wrapper.text()).not.toContain(unknownReason)
  })

  it('never exposes raw AccessKeys and caches only gcTime-zero safe options', async () => {
    const api = new InspectorApi()
    const { queryClient, router, wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain(activeAccessKey.key)
    expect(wrapper.text()).not.toContain(disabledAccessKey.key)
    expect(JSON.stringify(router.currentRoute.value.query)).not.toContain(activeAccessKey.key)
    expect(queryClient.getQueryData(controlQueryKeys.accessKeys.options())).toEqual([
      { id: 12, name: 'Client production', status: 'active' },
      { id: 13, name: 'Client disabled', status: 'disabled' },
    ])
    expect(
      queryClient.getQueryCache().find({ queryKey: controlQueryKeys.accessKeys.options() })?.options
        .gcTime,
    ).toBe(0)
    expect(JSON.stringify(queryClient.getQueryCache().getAll())).not.toContain(activeAccessKey.key)
    expect(queryClient.getMutationCache().getAll()).toEqual([])

    wrapper.unmount()
    await flushPromises()
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(queryClient.getQueryData(controlQueryKeys.accessKeys.options())).toBeUndefined()
  })

  it('renders a local safe failure instead of treating ApiError.message as a route reason', async () => {
    const canary = 'raw-api-message-never-render'
    const rejected = deferred<RouteInspectResponseDto>()
    const api = new InspectorApi([rejected.promise])
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-form"]').trigger('submit')
    rejected.reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', canary))
    await flushPromises()

    expect(wrapper.get('[data-test="inspector-request-error"]').text()).toContain(
      'Unable to inspect the current route',
    )
    expect(wrapper.text()).not.toContain(canary)
    expect(wrapper.find('[data-test="inspector-result"]').exists()).toBe(false)
  })

  it('keeps a prior observation but marks it older when a same-input retry fails', async () => {
    const retry = deferred<RouteInspectResponseDto>()
    const api = new InspectorApi([inspectionFixture(), retry.promise])
    const { wrapper } = await mountInspector(
      api,
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )

    await wrapper.get('[data-test="inspector-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="inspector-result-stale"]').exists()).toBe(false)

    await wrapper.get('[data-test="inspector-submit"]').trigger('click')
    retry.reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'same-input-retry-canary'))
    await flushPromises()

    expect(wrapper.find('[data-test="inspector-result"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="inspector-result-stale"]').text()).toContain(
      'older observation',
    )
    expect(wrapper.text()).toContain('Observed at 2026-07-25T10:00:00Z')
    expect(wrapper.text()).not.toContain('same-input-retry-canary')
  })
})
