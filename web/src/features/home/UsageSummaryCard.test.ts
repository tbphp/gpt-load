import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import type { UsageAggregateDto, UsageReportDto } from '@/app/resources/usage'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import UsageSummaryCard from './UsageSummaryCard.vue'

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
    series: [],
    breakdown: [],
    breakdown_truncated: false,
    collection_health: {
      scope: 'current_process',
      dropped_total: 2,
      write_failure_total: 1,
      last_write_failure_at: '2026-07-27T03:00:00Z',
    },
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

class UsageApi implements ApiClient {
  readonly requests: Array<{ path: ApiPath; options?: ApiRequestOptions }> = []

  constructor(private readonly report: UsageReportDto | Promise<UsageReportDto>) {}

  request<T>(path: ApiPath, options?: ApiRequestOptions): Promise<T> {
    this.requests.push({ path, options })
    if (path === '/api/usage?range=24h') return Promise.resolve(this.report as T)
    throw new Error(`Unexpected request: ${path}`)
  }
}

function queryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountSummary(
  api: ApiClient,
  client = queryClient(),
  locale: 'zh-CN' | 'en-US' | 'ja-JP' = 'en-US',
) {
  const mounted = await mountApp(UsageSummaryCard, {
    api,
    queryClient: client,
    locale,
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

describe('UsageSummaryCard', () => {
  it('uses the shared unfiltered 24-hour query and renders data with a canonical detail link', async () => {
    const api = new UsageApi(usageReport())
    const { wrapper } = await mountSummary(api)

    expect(api.requests).toEqual([
      {
        path: '/api/usage?range=24h',
        options: { method: 'GET', signal: expect.any(AbortSignal) },
      },
    ])
    expect(wrapper.get('[data-test="home-usage-requests"]').text()).toContain('12')
    expect(wrapper.get('[data-test="home-usage-tokens"]').text()).toContain('177')
    expect(wrapper.get('[data-test="home-usage-cost"]').text()).toContain('$0.123456')
    expect(wrapper.get('time').attributes('datetime')).toBe('2026-07-27T04:00:01Z')
    expect(
      wrapper.get<HTMLAnchorElement>('[data-test="home-usage-detail"]').attributes('href'),
    ).toBe('/monitor?tab=usage&range=24h')
    expect(wrapper.find('[data-test="usage-sparkline"]').exists()).toBe(false)
    expect(wrapper.find('a[href="/settings/model-prices"]').exists()).toBe(false)
  })

  it('renders a true zero-request state without fabricating a success rate', async () => {
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
      collection_health: {
        ...usageReport().collection_health,
        dropped_total: 0,
        write_failure_total: 0,
        last_write_failure_at: null,
      },
    })
    const { wrapper } = await mountSummary(new UsageApi(report))

    expect(wrapper.get('[data-test="home-usage-empty"]').text()).toContain('No persisted requests')
    expect(wrapper.text()).not.toMatch(/NaN|100%/)
    expect(wrapper.get('[data-test="home-usage-detail"]').attributes('href')).toBe(
      '/monitor?tab=usage&range=24h',
    )
  })

  it('keeps known zero quality counts neutral when persisted usage has no quality gaps', async () => {
    const report = usageReport({
      summary: {
        ...aggregate,
        ...zeroTokens,
        request_count: 1,
        success_count: 1,
        failure_count: 0,
        estimated_cost_usd: 0,
        usage_missing_count: 0,
        partial_count: 0,
        unpriced_request_count: 0,
      },
      collection_health: {
        ...usageReport().collection_health,
        dropped_total: 0,
        write_failure_total: 0,
        last_write_failure_at: null,
      },
    })
    const { wrapper } = await mountSummary(new UsageApi(report))

    expect(wrapper.get('[data-test="home-usage-quality"]').isVisible()).toBe(true)
    for (const quality of ['missing', 'partial', 'unpriced']) {
      const status = wrapper.get(`[data-test="home-usage-quality-${quality}"]`)
      expect(status.text()).toContain('0')
      expect(status.classes()).toContain('status-badge--neutral')
    }
    expect(wrapper.get('[data-test="home-usage-tokens"]').text()).toContain('0')
  })

  it('does not present excluded missing usage as a known zero-token value', async () => {
    const report = usageReport({
      summary: {
        ...aggregate,
        ...zeroTokens,
        request_count: 1,
        success_count: 1,
        failure_count: 0,
        estimated_cost_usd: 0,
        usage_missing_count: 1,
        partial_count: 0,
        unpriced_request_count: 0,
      },
    })
    const { wrapper } = await mountSummary(new UsageApi(report))

    expect(wrapper.get('[data-test="home-usage-tokens"]').text()).toContain('Unknown')
    expect(wrapper.get('[data-test="home-usage-tokens"]').text()).not.toMatch(/\b0\b/)
    expect(wrapper.get('[data-test="home-usage-quality-missing"]').text()).toBe('Usage missing 1')
  })

  it('keeps incomplete, unpriced, dropped, and write-failure quality visible with process scope', async () => {
    const report = usageReport({
      summary: { ...aggregate, estimated_cost_usd: 0 },
    })
    const { wrapper } = await mountSummary(new UsageApi(report))

    expect(wrapper.get('[data-test="home-usage-cost"]').text()).toContain('$0.00')
    expect(wrapper.get('[data-test="home-usage-cost"]').text()).toContain('unknown')
    expect(wrapper.get('[data-test="home-usage-cost"]').text()).not.toContain('Free')
    expect(wrapper.get('[data-test="home-usage-quality-missing"]').text()).toBe('Usage missing 2')
    expect(wrapper.get('[data-test="home-usage-quality-partial"]').text()).toBe('Usage partial 3')
    expect(wrapper.get('[data-test="home-usage-quality-unpriced"]').text()).toBe('Cost unpriced 4')
    for (const quality of ['missing', 'partial', 'unpriced']) {
      expect(wrapper.get(`[data-test="home-usage-quality-${quality}"]`).classes()).toContain(
        'status-badge--warning',
      )
    }
    expect(wrapper.get('[data-test="home-usage-pipeline-warning"]').text()).toMatch(
      /current process/i,
    )
    expect(wrapper.get('[data-test="home-usage-pipeline-warning"]').text()).toContain('Dropped 2')
    expect(wrapper.get('[data-test="home-usage-pipeline-warning"]').text()).toContain(
      'Write failures 1',
    )
  })

  it('keeps a positive sub-cent estimated cost visible', async () => {
    const report = usageReport({
      summary: {
        ...aggregate,
        estimated_cost_usd: 0.00000049,
        unpriced_request_count: 0,
      },
    })
    const { wrapper } = await mountSummary(new UsageApi(report))

    const displayedCost = wrapper.get('[data-test="home-usage-cost"] strong').text()
    expect(displayedCost).toBe('$0.00000049')
    expect(displayedCost).not.toBe('$0.00')
  })

  it('shows a safe initial error without exposing the transport error', async () => {
    const { wrapper } = await mountSummary(
      new UsageApi(Promise.reject(new Error('usage-secret-canary'))),
    )

    expect(wrapper.get('[data-test="home-usage-error"]').text()).toContain(
      'Unable to load the 24-hour usage summary',
    )
    expect(wrapper.text()).not.toContain('usage-secret-canary')
  })

  it('retains stale data from the exact Monitor cache key when refresh fails', async () => {
    const refresh = deferred<UsageReportDto>()
    const client = queryClient()
    client.setQueryData(controlQueryKeys.usage.report({ range: '24h' }), usageReport())

    const { wrapper } = await mountSummary(new UsageApi(refresh.promise), client)
    refresh.reject(new Error('stale-secret-canary'))
    await flushPromises()

    expect(wrapper.get('[data-test="home-usage-stale"]').text()).toContain('may be stale')
    expect(wrapper.get('[data-test="home-usage-requests"]').text()).toContain('12')
    expect(wrapper.text()).not.toContain('stale-secret-canary')
  })
})
