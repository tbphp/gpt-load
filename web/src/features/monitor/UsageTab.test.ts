import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import type { UsageAggregateDto, UsageReportDto } from '@/app/resources/usage'
import type { GroupSummary } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import UsageTab from './UsageTab.vue'
import usageTabSource from './UsageTab.vue?raw'

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

const zeroTokens = {
  uncached_input_tokens: 0,
  cache_read_tokens: 0,
  cache_write_5m_tokens: 0,
  cache_write_1h_tokens: 0,
  output_tokens: 0,
  total_tokens: 0,
} as const

function usageReport(overrides: Partial<UsageReportDto> = {}): UsageReportDto {
  return {
    range: '24h',
    granularity: 'hour',
    timezone: 'UTC',
    from: '2026-07-26T05:00:00Z',
    to: '2026-07-27T05:00:00Z',
    observed_at: '2026-07-27T04:00:01Z',
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
    breakdown_order: 'requests',
    breakdown_group_count: 1,
    collection_health: {
      scope: 'current_process',
      dropped_total: 2,
      write_failure_total: 1,
      last_write_failure_at: '2026-07-27T03:00:00Z',
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
    protocols: ['openai-chat-completions'],
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

  it('keeps every filter as one draft and applies range, Group, and model atomically', async () => {
    const api = new UsageApi([usageReport(), usageReport(), usageReport(), usageReport()])
    const { router, wrapper } = await mountUsage(
      api,
      '/monitor?tab=usage&range=24h&group_id=7&model=gpt-upstream',
    )

    await wrapper.get('[data-test="usage-range"]').setValue('30d')
    await wrapper.get('[data-test="usage-group"]').setValue('')
    await wrapper.get('[data-test="usage-model"]').setValue('claude-upstream')
    expect(router.currentRoute.value.fullPath).toBe(
      '/monitor?tab=usage&range=24h&group_id=7&model=gpt-upstream',
    )
    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))).toHaveLength(1)
    expect(wrapper.get('[data-test="usage-filter-dirty"]').text()).toContain('not applied')
    const appliedBefore = wrapper.get('[data-test="usage-applied-filters"]').text()
    expect(appliedBefore).toContain('Last 24 hours')
    expect(appliedBefore).toContain('gpt-upstream')
    expect(appliedBefore).not.toContain('claude-upstream')

    await wrapper.get('[data-test="usage-filter-form"]').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe(
      '/monitor?tab=usage&range=30d&model=claude-upstream',
    )
    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))).toHaveLength(2)
    expect(wrapper.find('[data-test="usage-filter-dirty"]').exists()).toBe(false)

    await wrapper.get('[data-test="usage-reset"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe(
      '/monitor?tab=usage&range=30d&model=claude-upstream',
    )
    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))).toHaveLength(2)
    expect(wrapper.get<HTMLSelectElement>('[data-test="usage-range"]').element.value).toBe('24h')
    expect(wrapper.get<HTMLInputElement>('[data-test="usage-model"]').element.value).toBe('')
    expect(wrapper.get('[data-test="usage-applied-filters"]').text()).toContain('claude-upstream')

    await wrapper.get('[data-test="usage-filter-form"]').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=usage&range=24h')
    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))).toHaveLength(3)
  })

  it('preserves an unapplied complete draft on Refresh and separates observed/refreshed time', async () => {
    const api = new UsageApi([usageReport(), usageReport({ observed_at: '2026-07-27T04:30:00Z' })])
    const { router, wrapper } = await mountUsage(
      api,
      '/monitor?tab=usage&range=24h&group_id=7&model=gpt-upstream',
    )

    await wrapper.get('[data-test="usage-group"]').setValue('')
    await wrapper.get('[data-test="usage-model"]').setValue('draft-upstream')
    await wrapper.get('[data-test="usage-range"]').setValue('30d')
    await wrapper.get('[data-test="usage-refresh"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe(
      '/monitor?tab=usage&range=24h&group_id=7&model=gpt-upstream',
    )
    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))).toHaveLength(2)
    expect(wrapper.get<HTMLSelectElement>('[data-test="usage-range"]').element.value).toBe('30d')
    expect(wrapper.get<HTMLSelectElement>('[data-test="usage-group"]').element.value).toBe('')
    expect(wrapper.get<HTMLInputElement>('[data-test="usage-model"]').element.value).toBe(
      'draft-upstream',
    )
    expect(wrapper.get('[data-test="usage-observed-at"]').attributes('datetime')).toBe(
      '2026-07-27T04:30:00Z',
    )
    expect(wrapper.get('[data-test="usage-refreshed-at"]').attributes('datetime')).not.toBe(
      '2026-07-27T04:30:00Z',
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

  it('uses backend totals and presents success/failure plus five token categories', async () => {
    const { wrapper } = await mountUsage(new UsageApi())

    expect(wrapper.get('[data-test="usage-kpi-total-tokens"]').text()).toContain('177')
    expect(wrapper.get('[data-test="usage-kpi-outcomes"]').text()).toContain('9')
    expect(wrapper.get('[data-test="usage-kpi-outcomes"]').text()).toContain('3')
    expect(wrapper.get('[data-test="usage-kpi-outcomes"]').text()).not.toContain('%')
    const tokenDefinition = wrapper.get('[data-test="usage-summary-token-definition"]').text()
    for (const value of ['100', '20', '3', '4', '50']) {
      expect(tokenDefinition).toContain(value)
    }
  })

  it('separates durable-window quality from current-process health with fixed scope semantics', async () => {
    const { wrapper } = await mountUsage(new UsageApi())

    expect(wrapper.get('[data-test="usage-window-quality"]').text()).toContain('selected window')
    expect(wrapper.get('[data-test="usage-quality-missing"]').text()).toContain('2')
    expect(wrapper.get('[data-test="usage-quality-partial"]').text()).toContain('3')
    expect(wrapper.get('[data-test="usage-quality-unpriced"]').text()).toContain('4')
    expect(wrapper.get('[data-test="usage-quality-overlap"]').text()).toContain('may overlap')
    const processHealth = wrapper.get('[data-test="usage-process-health"]')
    expect(processHealth.text()).toContain('current process')
    expect(processHealth.text()).toContain('reset when the process restarts')
    expect(processHealth.text()).toContain('2026-07-27T03:00:00Z')
    expect(processHealth.get('[data-test="usage-quality-dropped"]').text()).toContain('2')
    expect(processHealth.get('[data-test="usage-quality-write-failures"]').text()).toContain('1')
    expect(wrapper.text().indexOf('Current process collection health')).toBeLessThan(
      wrapper.text().indexOf('Persisted request trend'),
    )
    expect(processHealth.get('[data-test="usage-process-dropped-warning"]').text()).toContain(
      'covers only requests persisted successfully',
    )
    expect(processHealth.get('[data-test="usage-process-write-failure-warning"]').text()).toContain(
      'persistence batch failed',
    )
  })

  it('always shows overlap and uses neutral process status when current counters are zero', async () => {
    const report = usageReport({
      summary: {
        ...aggregate,
        usage_missing_count: 0,
        partial_count: 0,
        unpriced_request_count: 0,
      },
      collection_health: {
        scope: 'current_process',
        dropped_total: 0,
        write_failure_total: 0,
        last_write_failure_at: null,
      },
    })
    const { wrapper } = await mountUsage(new UsageApi([report]))

    expect(wrapper.get('[data-test="usage-quality-overlap"]').text()).toContain('may overlap')
    expect(
      wrapper.get('[data-test="usage-quality-dropped"]').find('[class*="status-badge"]').classes(),
    ).toContain('status-badge--neutral')
    expect(
      wrapper
        .get('[data-test="usage-quality-write-failures"]')
        .find('[class*="status-badge"]')
        .classes(),
    ).toContain('status-badge--neutral')
    expect(wrapper.get('[data-test="usage-process-health"]').text()).toContain(
      'has not reported dropped telemetry or write failures',
    )
  })

  it('explains dropped and write-failure process counters independently', async () => {
    const dropped = await mountUsage(
      new UsageApi([
        usageReport({
          collection_health: {
            scope: 'current_process',
            dropped_total: 2,
            write_failure_total: 0,
            last_write_failure_at: null,
          },
        }),
      ]),
    )
    expect(dropped.wrapper.find('[data-test="usage-process-dropped-warning"]').exists()).toBe(true)
    expect(dropped.wrapper.find('[data-test="usage-process-write-failure-warning"]').exists()).toBe(
      false,
    )
    dropped.wrapper.unmount()

    const writeFailure = await mountUsage(
      new UsageApi([
        usageReport({
          collection_health: {
            scope: 'current_process',
            dropped_total: 0,
            write_failure_total: 1,
            last_write_failure_at: '2026-07-27T03:00:00Z',
          },
        }),
      ]),
    )
    expect(writeFailure.wrapper.find('[data-test="usage-process-dropped-warning"]').exists()).toBe(
      false,
    )
    expect(
      writeFailure.wrapper.find('[data-test="usage-process-write-failure-warning"]').exists(),
    ).toBe(true)
  })

  it('keeps current-process health visible when the durable window is empty', async () => {
    const report = usageReport({
      summary: {
        ...aggregate,
        ...zeroTokens,
        request_count: 0,
        success_count: 0,
        failure_count: 0,
        estimated_cost_usd: 0,
        usage_missing_count: 0,
        partial_count: 0,
        unpriced_request_count: 0,
      },
      series: [],
      breakdown: [],
    })
    const { wrapper } = await mountUsage(new UsageApi([report]))

    expect(wrapper.get('[data-test="usage-empty"]').text()).toContain('No persisted usage matches')
    expect(wrapper.get('[data-test="usage-process-health"]').text()).toContain('Dropped')
    expect(wrapper.get('[data-test="usage-process-health"]').text()).toContain('2')
    expect(wrapper.find('[data-test="usage-kpi-total-tokens"]').exists()).toBe(false)
  })

  it('conditionally explains partial and unpriced exclusions and preserves known zero cost', async () => {
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
      expect(wrapper.get(selector).text()).toContain('$0.00')
      expect(wrapper.get(selector).text()).toContain('unknown')
      expect(wrapper.get(selector).text()).not.toContain('Free')
    }
    expect(wrapper.get('[data-test="usage-unpriced-explanation"]').text()).toContain(
      'unsupported billing detail or diagnostics',
    )
    expect(wrapper.get('[data-test="usage-unpriced-explanation"]').text()).toContain(
      'tokens are excluded',
    )
    expect(wrapper.get('[data-test="usage-partial-explanation"]').text()).toContain(
      'usage-only final chunk',
    )

    const complete = await mountUsage(
      new UsageApi([
        usageReport({
          summary: {
            ...aggregate,
            partial_count: 0,
            unpriced_request_count: 0,
          },
        }),
      ]),
    )
    expect(complete.wrapper.find('[data-test="usage-unpriced-explanation"]').exists()).toBe(false)
    expect(complete.wrapper.find('[data-test="usage-partial-explanation"]').exists()).toBe(false)
  })

  it('refreshes without changing the applied URL filters', async () => {
    const api = new UsageApi([usageReport(), usageReport()])
    const { router, wrapper } = await mountUsage(api)

    await wrapper.get('[data-test="usage-refresh"]').trigger('click')
    await flushPromises()

    expect(api.requests.filter(({ path }) => path.startsWith('/api/usage?'))).toHaveLength(2)
    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=usage&range=24h')
  })

  it.each([
    ['one point', [usageReport().series[0]], 'trend-request-marker'],
    ['no points', [], 'trend-chart-empty'],
  ])('renders the %s trend state without fabricating data', async (_case, series, testID) => {
    const { wrapper } = await mountUsage(new UsageApi([usageReport({ series })]))

    expect(wrapper.find(`[data-test="${testID}"]`).exists()).toBe(true)
  })

  it('uses the shared request-and-failure TrendChart without retaining the old sparkline', async () => {
    const { wrapper } = await mountUsage(new UsageApi())

    expect(wrapper.get('[data-test="trend-request-path"]').attributes('d')).not.toBe('')
    expect(wrapper.findAll('[data-test="trend-failure-bar"]')).toHaveLength(1)
    expect(usageTabSource).toContain("import TrendChart from '@/components/charts/TrendChart.vue'")
    expect(usageTabSource).not.toContain(['Usage', 'Sparkline'].join(''))
  })

  it('renders time and upstream-model breakdown tables plus the backend top-100 warning', async () => {
    const { wrapper } = await mountUsage(new UsageApi([usageReport({ breakdown_truncated: true })]))

    expect(wrapper.get('[data-test="usage-series-table"]').text()).toContain('2026-07-27T02:00:00Z')
    expect(wrapper.get('[data-test="usage-breakdown-table"]').text()).toContain('gpt-upstream')
    expect(wrapper.get('[data-test="usage-breakdown-table"]').text()).toContain('Upstream model')
    for (const heading of [
      'Uncached input',
      'Cache read',
      'Cache write (5m)',
      'Cache write (1h)',
      'Output',
    ]) {
      expect(wrapper.get('[data-test="usage-breakdown-table"]').text()).toContain(heading)
    }
    expect(wrapper.get('[data-test="usage-breakdown-truncated"]').text()).toContain('top 100')
  })

  it('labels deleted or unknown Groups in filters and breakdown rows', async () => {
    const report = usageReport({
      breakdown: [{ ...aggregate, group_id: 99, model: 'orphan-model' }],
    })
    const { wrapper } = await mountUsage(
      new UsageApi([report], [group(7, 'Primary')]),
      '/monitor?tab=usage&range=24h&group_id=99',
    )

    expect(wrapper.get('[data-test="usage-group"]').text()).toContain('#99 · Deleted or unknown')
    expect(wrapper.get('[data-test="usage-breakdown-table"]').text()).toContain(
      '#99 · Deleted or unknown',
    )
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
            ...zeroTokens,
            request_count: 0,
            success_count: 0,
            failure_count: 0,
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

  it.each([
    [0, '$0.00'],
    [0.01, '$0.01'],
    [0.00000001, '$0.00000001'],
    [0.000000001, '<$0.00000001'],
  ])(
    'formats estimated USD %s without rounding a positive value to zero',
    async (cost, expected) => {
      const report = usageReport({
        summary: {
          ...aggregate,
          estimated_cost_usd: cost,
          unpriced_request_count: 0,
        },
      })
      const { wrapper } = await mountUsage(new UsageApi([report]))

      expect(wrapper.get('[data-test="usage-kpi-cost"]').text()).toContain(expected)
    },
  )
})
