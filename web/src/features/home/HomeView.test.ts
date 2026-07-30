import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import { ApiError } from '@/api/errors'
import type { GroupSummary } from '@/api/control/types'
import type { RuntimeHealthDto } from '@/app/resources/health'
import type { UsageAggregateDto, UsageReportDto } from '@/app/resources/usage'
import { controlQueryKeys } from '@/app/query-keys'
import { FakeApi } from '@/test/fake-api'
import { mountApp } from '@/test/mount-app'

import HomeView from './HomeView.vue'

const usage24Path = '/api/usage?range=24h&breakdown_order=cost' as const
const usage30Path = '/api/usage?range=30d&breakdown_order=cost' as const

const groupFixture: GroupSummary = {
  id: 7,
  name: 'Primary',
  upstream_url: 'https://api.example.test',
  protocols: ['openai'],
  models: [{ id: 'gpt-4o', alias: '' }],
  enabled: true,
  key_count: 3,
}

const requestLog: RuntimeHealthDto['request_log'] = {
  enqueued_total: 20,
  persisted_total: 20,
  dropped_not_running_total: 0,
  dropped_queue_full_total: 0,
  dropped_stopping_total: 0,
  dropped_persist_failed_total: 0,
  dropped_shutdown_total: 0,
  dropped_total: 0,
  write_failure_total: 0,
  retention_delete_failure_total: 0,
  queue_depth: 0,
  queue_capacity: 128,
  last_write_failure_at: null,
  last_retention_failure_at: null,
}

function health(overrides: Partial<RuntimeHealthDto> = {}): RuntimeHealthDto {
  return {
    observed_at: '2026-07-29T06:32:07Z',
    snapshot_revision: 28,
    stats_window_seconds: 300,
    counts: { total: 3, available: 3, cooldown: 0, blacklisted: 0, disabled: 0 },
    groups: [
      {
        id: 7,
        name: 'Primary',
        enabled: true,
        counts: { total: 3, available: 3, cooldown: 0, blacklisted: 0, disabled: 0 },
      },
    ],
    cooldown_keys: [],
    blacklisted_keys: [],
    request_log: requestLog,
    ...overrides,
  }
}

const aggregate: UsageAggregateDto = {
  request_count: 20,
  success_count: 18,
  failure_count: 2,
  uncached_input_tokens: 1_000,
  cache_read_tokens: 200,
  cache_write_5m_tokens: 0,
  cache_write_1h_tokens: 0,
  output_tokens: 400,
  total_tokens: 1_600,
  estimated_cost_usd: 2.5,
  usage_missing_count: 0,
  partial_count: 0,
  unpriced_request_count: 0,
}

const zeroAggregate: UsageAggregateDto = {
  request_count: 0,
  success_count: 0,
  failure_count: 0,
  uncached_input_tokens: 0,
  cache_read_tokens: 0,
  cache_write_5m_tokens: 0,
  cache_write_1h_tokens: 0,
  output_tokens: 0,
  total_tokens: 0,
  estimated_cost_usd: 0,
  usage_missing_count: 0,
  partial_count: 0,
  unpriced_request_count: 0,
}

function usage(
  range: '24h' | '30d' = '24h',
  overrides: Partial<UsageReportDto> = {},
): UsageReportDto {
  const hourly = range === '24h'
  return {
    range,
    granularity: hourly ? 'hour' : 'day',
    timezone: 'UTC',
    from: hourly ? '2026-07-28T07:00:00Z' : '2026-06-29T00:00:00Z',
    to: hourly ? '2026-07-29T07:00:00Z' : '2026-07-29T00:00:00Z',
    observed_at: hourly ? '2026-07-29T06:32:00Z' : '2026-07-28T23:00:00Z',
    summary: aggregate,
    series: [
      {
        ...aggregate,
        bucket_start: hourly ? '2026-07-29T05:00:00Z' : '2026-07-28T00:00:00Z',
        bucket_end: hourly ? '2026-07-29T06:00:00Z' : '2026-07-29T00:00:00Z',
      },
    ],
    breakdown: [
      {
        ...aggregate,
        group_id: 7,
        model: 'gpt-4o',
      },
    ],
    breakdown_truncated: false,
    breakdown_order: 'cost',
    breakdown_group_count: 1,
    collection_health: {
      scope: 'current_process',
      dropped_total: 0,
      write_failure_total: 0,
      last_write_failure_at: null,
    },
    ...overrides,
  }
}

function problemHealth(): RuntimeHealthDto {
  return health({
    counts: { total: 3, available: 1, cooldown: 1, blacklisted: 1, disabled: 0 },
    groups: [
      {
        id: 7,
        name: 'Primary',
        enabled: true,
        counts: { total: 3, available: 1, cooldown: 1, blacklisted: 1, disabled: 0 },
      },
    ],
    cooldown_keys: [
      {
        key_id: 11,
        group_id: 7,
        group_name: 'Primary',
        cooldown_until: '2026-07-29T06:36:00Z',
        failure_count: 5,
        recent_success_count: 0,
        recent_failure_count: 5,
        consecutive_failure_count: 5,
        weight_manual: null,
        weight_auto: 40,
        recovery: {
          automatic: true,
          mode: 'cooldown_expiry',
          at: '2026-07-29T06:36:00Z',
        },
        mask: 'rate****safe',
        last_failure_category: 'rate_limited',
        last_status_code: 429,
      },
    ],
    blacklisted_keys: [
      {
        key_id: 12,
        group_id: 7,
        group_name: 'Primary',
        failure_count: 12,
        recent_success_count: 0,
        recent_failure_count: 8,
        consecutive_failure_count: 12,
        weight_manual: null,
        weight_auto: 0,
        recovery: { automatic: true, mode: 'validation_probe', at: null },
        mask: 'inva****lock',
        last_failure_category: 'invalid_key',
        last_status_code: 401,
      },
    ],
  })
}

function configureNormal(api: FakeApi, usageReport = usage()): void {
  api.when('/api/groups').resolve([groupFixture])
  api.when('/api/health').resolve(health())
  api.when(usage24Path).resolve(usageReport)
}

async function mountHome(api: FakeApi, queryClient?: QueryClient) {
  const mounted = await mountApp(HomeView, {
    api,
    queryClient: queryClient ?? new QueryClient({ defaultOptions: { queries: { retry: false } } }),
  })
  await flushPromises()
  return mounted
}

function appearsBefore(first: Element, second: Element): boolean {
  return Boolean(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING)
}

describe('HomeView Ledger states', () => {
  it('renders the normal Ledger order and a completely static connection placeholder', async () => {
    const api = new FakeApi()
    configureNormal(api)
    const { wrapper } = await mountHome(api)
    const ranking = wrapper.get('[data-test="home-cost-ranking"]')
    const connection = wrapper.get('[data-test="home-connection-placeholder"]')

    expect(wrapper.get('[data-test="home-lede"]').classes()).toContain('home-lede--normal')
    expect(wrapper.get('[data-test="home-success-rate"]').text()).toContain('90')
    expect(wrapper.find('[data-test="trend-request-path"]').exists()).toBe(true)
    expect(appearsBefore(ranking.element, connection.element)).toBe(true)
    expect(wrapper.find('[data-group-id]').exists()).toBe(false)
    expect(
      connection
        .find('button, a, input, select, textarea, code, [role="button"], [aria-expanded]')
        .exists(),
    ).toBe(false)
    expect(connection.text()).toContain('占位 · 待专门设计')
    expect(connection.text()).not.toMatch(/Base URL|curl|复制/)
    expect(api.requests.map(({ path }) => path)).toEqual([
      '/api/groups',
      '/api/health',
      usage24Path,
    ])
    expect(api.requests.some(({ path }) => path.includes('access-keys'))).toBe(false)
  })

  it('renders warning problem Groups with safe masks and canonical deep links', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(problemHealth())
    api.when(usage24Path).resolve(usage())
    const { wrapper } = await mountHome(api)

    expect(wrapper.get('[data-test="home-lede"]').classes()).toContain('home-lede--warning')
    expect(wrapper.get('[data-test="home-problem-groups"]').text()).toContain('rate****safe')
    expect(wrapper.get('[data-test="home-problem-groups"]').text()).toContain('inva****lock')
    expect(wrapper.get('[data-test="home-problem-link"]').attributes('href')).toBe(
      '/groups/7?tab=keys&key_state=problem',
    )
  })

  it('keeps usage visible when health is unknown without cached data', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'health-canary'))
    api.when(usage24Path).resolve(usage())
    const { wrapper } = await mountHome(api)

    expect(wrapper.get('h1').text()).toContain('无法确认服务状态')
    expect(wrapper.find('[data-test="home-health-retry"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="home-health-usage-independence"]').text()).toContain(
      '用量数据来自独立数据源',
    )
    expect(wrapper.get('[data-test="home-success-rate"]').text()).toContain('90')
    expect(wrapper.text()).not.toContain('health-canary')
  })

  it('retains cached health as stale while leaving current usage independent', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when(usage24Path).resolve(usage())
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(controlQueryKeys.health(), health())
    const { wrapper } = await mountHome(api, queryClient)

    expect(wrapper.get('[data-test="home-lede"]').classes()).toContain('home-lede--neutral')
    expect(wrapper.get('h1').text()).toContain('最近一次观测')
    expect(wrapper.text()).toContain('当前健康检查失败')
    expect(wrapper.find('[data-test="home-success-rate"]').exists()).toBe(true)
  })

  it('renders one full-page first-run state only after Groups are confirmed empty', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([])
    api.when('/api/health').resolve(health({ groups: [] }))
    api.when(usage24Path).resolve(usage())
    const { wrapper } = await mountHome(api)

    expect(wrapper.get('[data-test="home-zero-groups"]').text()).toContain('尚未配置 Group')
    expect(wrapper.get('[data-test="home-zero-groups"] h1').text()).toBe('尚未配置 Group')
    expect(wrapper.find('[data-test="home-zero-groups"] [href="/import"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="home-lede"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-connection-placeholder"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-cost-ranking"]').exists()).toBe(false)
  })

  it('promotes the static connection placeholder directly after the lede for zero usage', async () => {
    const api = new FakeApi()
    configureNormal(
      api,
      usage('24h', {
        summary: zeroAggregate,
        series: [],
        breakdown: [],
        breakdown_group_count: 0,
      }),
    )
    const { wrapper } = await mountHome(api)
    const lede = wrapper.get('[data-test="home-lede"]')
    const connection = wrapper.get('[data-test="home-connection-placeholder"]')
    const empty = wrapper.get('[data-test="home-zero-usage"]')

    expect(appearsBefore(lede.element, connection.element)).toBe(true)
    expect(appearsBefore(connection.element, empty.element)).toBe(true)
    expect(wrapper.find('[data-test="home-success-rate"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-cost-ranking"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="home-connection-placeholder"]')).toHaveLength(1)
  })

  it('lets inventory, health and usage loading regions resolve independently', async () => {
    const api = new FakeApi()
    const groupsRoute = api.when('/api/groups')
    const healthRoute = api.when('/api/health')
    const usageRoute = api.when(usage24Path)
    const mounted = await mountApp(HomeView, {
      api,
      queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }),
    })
    await flushPromises()

    expect(mounted.wrapper.find('[data-test="home-inventory-loading"]').exists()).toBe(true)
    expect(mounted.wrapper.get('[data-test="home-lede"]').text()).toContain('正在加载运行健康状态')
    expect(mounted.wrapper.find('[data-test="home-usage-loading"]').exists()).toBe(true)

    groupsRoute.resolve([groupFixture])
    await flushPromises()
    expect(mounted.wrapper.find('[data-test="home-inventory-loading"]').exists()).toBe(false)
    expect(mounted.wrapper.find('[data-test="home-usage-loading"]').exists()).toBe(true)

    healthRoute.resolve(health())
    await flushPromises()
    expect(mounted.wrapper.get('[data-test="home-lede"]').find('h1').exists()).toBe(true)
    expect(mounted.wrapper.find('[data-test="home-usage-loading"]').exists()).toBe(true)

    usageRoute.resolve(usage())
    await flushPromises()
    expect(mounted.wrapper.find('[data-test="home-success-rate"]').exists()).toBe(true)
    mounted.wrapper.unmount()
  })

  it('shows only dropped/write failures as the Home data-quality warning', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(
      health({
        request_log: {
          ...requestLog,
          dropped_total: 3,
          write_failure_total: 2,
          retention_delete_failure_total: 9,
        },
      }),
    )
    api.when(usage24Path).resolve(usage())
    const { wrapper } = await mountHome(api)

    const warning = wrapper.get('[data-test="home-pipeline-warning"]')
    expect(warning.text()).toContain('丢弃 3')
    expect(warning.text()).toContain('写入失败 2')
    expect(warning.text()).not.toContain('9')
  })

  it('requests explicit cost order for 24h and changes canonical identity for 30d', async () => {
    const api = new FakeApi()
    configureNormal(api)
    api.when(usage30Path).resolve(usage('30d'))
    const { wrapper } = await mountHome(api)

    const range30d = wrapper.get('[data-segment-value="30d"]')
    await range30d.trigger('mousedown', { button: 0, ctrlKey: false })
    await range30d.trigger('click')
    await flushPromises()

    expect(api.requests.some(({ path }) => path === usage24Path)).toBe(true)
    expect(api.requests.some(({ path }) => path === usage30Path)).toBe(true)
    expect(wrapper.get('[data-segment-value="30d"]').attributes('aria-selected')).toBe('true')
  })

  it('refetches each Home resource only when visibility returns', async () => {
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    let hidden = false
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => hidden,
    })
    const api = new FakeApi()
    configureNormal(api)
    const { wrapper } = await mountHome(api)
    expect(api.requests).toHaveLength(3)

    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(api.requests).toHaveLength(3)

    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(api.requests).toHaveLength(6)

    wrapper.unmount()
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('does not turn an inventory error into zero Groups or hide healthy usage', async () => {
    const api = new FakeApi()
    api.when('/api/groups').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'group-canary'))
    api.when('/api/health').resolve(health())
    api.when(usage24Path).resolve(usage())
    const { wrapper } = await mountHome(api)

    expect(wrapper.get('[data-test="home-inventory-error"]').text()).toContain(
      '无法加载 Group 清单',
    )
    expect(wrapper.find('[data-test="home-zero-groups"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-success-rate"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('group-canary')
  })

  it('keeps health and the placeholder when usage fails, and shows cached usage as stale', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(health())
    api.when(usage24Path).reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'usage-canary'))
    const emptyClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const failed = await mountHome(api, emptyClient)

    expect(failed.wrapper.get('[data-test="home-usage-error"]').text()).toContain(
      '无法加载用量数据',
    )
    expect(failed.wrapper.find('[data-test="home-lede"]').exists()).toBe(true)
    expect(failed.wrapper.find('[data-test="home-connection-placeholder"]').exists()).toBe(true)
    expect(failed.wrapper.text()).not.toContain('usage-canary')
    failed.wrapper.unmount()

    const staleApi = new FakeApi()
    staleApi.when('/api/groups').resolve([groupFixture])
    staleApi.when('/api/health').resolve(health())
    staleApi
      .when(usage24Path)
      .reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'usage-stale-canary'))
    const staleClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    staleClient.setQueryData(
      controlQueryKeys.usage.report({ range: '24h', breakdown_order: 'cost' }),
      usage(),
    )
    const stale = await mountHome(staleApi, staleClient)

    expect(stale.wrapper.get('[data-test="home-usage-stale"]').text()).toContain(
      '用量数据可能已过期',
    )
    expect(stale.wrapper.find('[data-test="home-success-rate"]').exists()).toBe(true)
  })
})
