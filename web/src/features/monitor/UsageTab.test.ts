import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import type { UsageAggregateDto, UsageReportDto } from '@/api/control/usage'
import type { GroupSummary } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import UsageTab from './UsageTab.vue'

const aggregate: UsageAggregateDto = {
  request_count: 12,
  success_count: 9,
  failure_count: 3,
  uncached_input_tokens: 100,
  cache_read_tokens: 20,
  cache_write_5m_tokens: 3,
  cache_write_1h_tokens: 4,
  output_tokens: 50,
  total_tokens: 177,
  estimated_cost_usd: 0.123456,
  usage_missing_count: 2,
  partial_count: 3,
  unpriced_request_count: 4,
}

function usageReport(overrides: Partial<UsageReportDto> = {}): UsageReportDto {
  return {
    observed_at: '2026-07-27T04:00:01Z',
    range: {
      from: '2026-07-26T05:00:00Z',
      to: '2026-07-27T05:00:00Z',
      granularity: 'hour',
    },
    filters: { group_id: null, model: '' },
    summary: { ...aggregate },
    series: [
      {
        ...aggregate,
        bucket_start: '2026-07-27T02:00:00Z',
        bucket_end: '2026-07-27T03:00:00Z',
      },
    ],
    breakdown: [{ ...aggregate, group_id: 7, model: 'gpt-upstream' }],
    breakdown_truncated: false,
    request_log: {
      enqueued_total: 20,
      persisted_total: 18,
      dropped_not_running_total: 0,
      dropped_queue_full_total: 1,
      dropped_stopping_total: 0,
      dropped_persist_failed_total: 1,
      dropped_shutdown_total: 0,
      dropped_total: 2,
      write_failure_total: 1,
      retention_delete_failure_total: 0,
      queue_depth: 0,
      queue_capacity: 100,
      last_write_failure_at: '2026-07-27T03:00:00Z',
      last_retention_failure_at: null,
    },
    ...overrides,
  }
}

function group(id: number, name: string): GroupSummary {
  return {
    id,
    name,
    enabled: true,
    upstream_url: 'https://example.test',
    protocols: ['openai'],
    key_count: 1,
    models: [{ id: 'gpt-upstream', alias: '' }],
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

class UsageApi implements ApiClient {
  readonly requests: Array<{ path: ApiPath; options?: ApiRequestOptions }> = []
  private usageRequestCount = 0

  constructor(
    private readonly reports: Array<UsageReportDto | Promise<UsageReportDto>> = [usageReport()],
    private readonly groups: GroupSummary[] | Promise<GroupSummary[]> = [group(7, 'Primary')],
  ) {}

  request<T>(path: ApiPath, options?: ApiRequestOptions): Promise<T> {
    this.requests.push({ path, options })
    if (path.startsWith('/api/usage?')) {
      const report = this.reports[this.usageRequestCount] ?? this.reports.at(-1)
      this.usageRequestCount += 1
      return Promise.resolve(report as T)
    }
    if (path === '/api/groups') return Promise.resolve(this.groups as T)
    throw new Error(`Unexpected request: ${path}`)
  }
}

function queryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountUsage(
  api: ApiClient,
  path = '/monitor?tab=usage&range=24h',
  client = queryClient(),
) {
  const mounted = await mountApp(UsageTab, {
    api,
    queryClient: client,
    path,
    locale: 'en-US',
    mounting: { attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

describe('UsageTab', () => {
  it('loads the default 24-hour report and applies canonical URL filters to the request and draft', async () => {
    const api = new UsageApi([usageReport(), usageReport()])
    const first = await mountUsage(api)

    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))[0]).toEqual({
      path: '/api/usage?range=24h',
      options: { method: 'GET', signal: expect.any(AbortSignal) },
    })
    first.wrapper.unmount()

    const filtered = await mountUsage(
      api,
      '/monitor?tab=usage&range=30d&group_id=7&model=gpt-upstream',
    )
    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))[1]?.path).toBe(
      '/api/usage?range=30d&group_id=7&model=gpt-upstream',
    )
    expect(filtered.wrapper.get<HTMLSelectElement>('[data-test="usage-group"]').element.value).toBe(
      '7',
    )
    expect(filtered.wrapper.get<HTMLInputElement>('[data-test="usage-model"]').element.value).toBe(
      'gpt-upstream',
    )
  })

  it('updates range immediately while Group/model remain an explicit Apply/Reset draft', async () => {
    const api = new UsageApi([usageReport(), usageReport(), usageReport(), usageReport()])
    const { router, wrapper } = await mountUsage(
      api,
      '/monitor?tab=usage&range=24h&group_id=7&model=gpt-upstream',
    )

    await wrapper.get('[data-test="usage-range"]').setValue('30d')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe(
      '/monitor?tab=usage&range=30d&group_id=7&model=gpt-upstream',
    )

    await wrapper.get('[data-test="usage-group"]').setValue('')
    await wrapper.get('[data-test="usage-model"]').setValue('claude-upstream')
    expect(router.currentRoute.value.fullPath).toContain('model=gpt-upstream')
    await wrapper.get('[data-test="usage-filter-form"]').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe(
      '/monitor?tab=usage&range=30d&model=claude-upstream',
    )

    await wrapper.get('[data-test="usage-reset"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=usage&range=30d')
  })

  it('preserves an unapplied Group/model draft when only range changes', async () => {
    const api = new UsageApi([usageReport(), usageReport()])
    const { router, wrapper } = await mountUsage(
      api,
      '/monitor?tab=usage&range=24h&group_id=7&model=gpt-upstream',
    )

    await wrapper.get('[data-test="usage-group"]').setValue('')
    await wrapper.get('[data-test="usage-model"]').setValue('draft-upstream')
    await wrapper.get('[data-test="usage-range"]').setValue('30d')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe(
      '/monitor?tab=usage&range=30d&group_id=7&model=gpt-upstream',
    )
    expect(wrapper.get<HTMLSelectElement>('[data-test="usage-group"]').element.value).toBe('')
    expect(wrapper.get<HTMLInputElement>('[data-test="usage-model"]').element.value).toBe(
      'draft-upstream',
    )
  })

  it('degrades a failed Group option query to a usable positive-ID input', async () => {
    const api = new UsageApi([usageReport()], Promise.reject(new Error('groups-secret')))
    const { wrapper } = await mountUsage(api)

    expect(wrapper.get('[data-test="usage-group-options-failed"]').text()).toContain(
      'Group names are unavailable',
    )
    const input = wrapper.get<HTMLInputElement>('[data-test="usage-group"]')
    expect(input.element.tagName).toBe('INPUT')
    expect(input.attributes('inputmode')).toBe('numeric')
    expect(wrapper.text()).not.toContain('groups-secret')
  })

  it('uses backend total_tokens and presents separate quality counts with overlap and process scope', async () => {
    const { wrapper } = await mountUsage(new UsageApi())

    expect(wrapper.get('[data-test="usage-kpi-total-tokens"]').text()).toContain('177')
    expect(wrapper.get('[data-test="usage-quality-missing"]').text()).toContain('2')
    expect(wrapper.get('[data-test="usage-quality-partial"]').text()).toContain('3')
    expect(wrapper.get('[data-test="usage-quality-unpriced"]').text()).toContain('4')
    expect(wrapper.get('[data-test="usage-quality-overlap"]').text()).toContain('may overlap')
    expect(wrapper.get('[data-test="usage-scope"]').text()).toContain('current process')
    expect(wrapper.get('[data-test="usage-quality-dropped"]').text()).toContain('2')
    expect(wrapper.get('[data-test="usage-quality-write-failures"]').text()).toContain('1')
  })

  it('explains excluded tokens and links unpriced requests to model prices without calling unknown cost free', async () => {
    const report = usageReport({
      summary: { ...aggregate, estimated_cost_usd: 0, unpriced_request_count: 4 },
      series: [
        {
          ...aggregate,
          estimated_cost_usd: 0,
          unpriced_request_count: 4,
          bucket_start: '2026-07-27T02:00:00Z',
          bucket_end: '2026-07-27T03:00:00Z',
        },
      ],
      breakdown: [
        {
          ...aggregate,
          estimated_cost_usd: 0,
          unpriced_request_count: 4,
          group_id: 7,
          model: 'gpt-upstream',
        },
      ],
    })
    const { wrapper } = await mountUsage(new UsageApi([report]))

    expect(wrapper.get('[data-test="usage-aggregation-note"]').text()).toContain(
      'Only complete usage with priced cost is included in default token and estimated-cost totals',
    )
    expect(
      wrapper.get<HTMLAnchorElement>('[data-test="usage-prices-link"]').attributes('href'),
    ).toBe('/settings/model-prices')
    for (const selector of [
      '[data-test="usage-kpi-cost"]',
      '[data-test="usage-series-cost-0"]',
      '[data-test="usage-breakdown-cost-0"]',
    ]) {
      expect(wrapper.get(selector).text()).toContain('Unknown')
      expect(wrapper.get(selector).text()).not.toContain('$0')
      expect(wrapper.get(selector).text()).not.toContain('Free')
    }
  })

  it.each([
    ['one point', [usageReport().series[0]], 'usage-sparkline-marker'],
    ['no points', [], 'usage-sparkline-empty'],
  ])('renders the %s sparkline state without fabricating data', async (_case, series, testID) => {
    const { wrapper } = await mountUsage(new UsageApi([usageReport({ series })]))

    expect(wrapper.find(`[data-test="${testID}"]`).exists()).toBe(true)
  })

  it('renders time and upstream-model breakdown tables plus the backend top-100 warning', async () => {
    const { wrapper } = await mountUsage(new UsageApi([usageReport({ breakdown_truncated: true })]))

    expect(wrapper.get('[data-test="usage-series-table"]').text()).toContain('2026-07-27T02:00:00Z')
    expect(wrapper.get('[data-test="usage-breakdown-table"]').text()).toContain('gpt-upstream')
    expect(wrapper.get('[data-test="usage-breakdown-table"]').text()).toContain('Upstream model')
    expect(wrapper.get('[data-test="usage-breakdown-truncated"]').text()).toContain('top 100')
  })

  it('renders loading, no-data, terminal error, and stale-data states without leaking errors', async () => {
    const pending = deferred<UsageReportDto>()
    const loading = await mountApp(UsageTab, {
      api: new UsageApi([pending.promise]),
      queryClient: queryClient(),
      path: '/monitor?tab=usage&range=24h',
      locale: 'en-US',
      mounting: { attachTo: document.body },
    })
    expect(loading.wrapper.text()).toContain('Loading usage report')
    loading.wrapper.unmount()

    const noData = await mountUsage(
      new UsageApi([
        usageReport({
          summary: {
            ...aggregate,
            request_count: 0,
            success_count: 0,
            failure_count: 0,
            total_tokens: 0,
            estimated_cost_usd: 0,
            usage_missing_count: 0,
            partial_count: 0,
            unpriced_request_count: 0,
          },
          series: [],
          breakdown: [],
        }),
      ]),
    )
    expect(noData.wrapper.get('[data-test="usage-empty"]').text()).toContain(
      'No persisted usage matches',
    )
    noData.wrapper.unmount()

    const failure = await mountUsage(
      new UsageApi([Promise.reject(new Error('usage-secret-canary'))]),
    )
    expect(failure.wrapper.text()).toContain('Unable to load usage report')
    expect(failure.wrapper.text()).not.toContain('usage-secret-canary')
    failure.wrapper.unmount()

    const staleFailure = deferred<UsageReportDto>()
    const client = queryClient()
    const stale = await mountUsage(
      new UsageApi([usageReport(), staleFailure.promise]),
      undefined,
      client,
    )
    const invalidation = client.invalidateQueries({
      queryKey: controlQueryKeys.usage.report({ range: '24h' }),
    })
    await Promise.resolve()
    staleFailure.reject(new Error('stale-secret-canary'))
    await invalidation
    await flushPromises()
    expect(stale.wrapper.get('[data-test="usage-stale"]').text()).toContain('may be stale')
    expect(stale.wrapper.get('[data-test="usage-kpi-total-tokens"]').text()).toContain('177')
    expect(stale.wrapper.text()).not.toContain('stale-secret-canary')
  })
})
