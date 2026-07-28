import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import { ApiError } from '@/api/errors'
import type { UsageReportDto } from '@/app/resources/usage'
import { controlQueryKeys } from '@/app/query-keys'
import type { AccessKeyOptionDto, GroupSummary } from '@/api/control/types'
import type { RuntimeHealthDto } from '@/app/resources/health'
import { FakeApi } from '@/test/fake-api'
import { mountApp } from '@/test/mount-app'

import HomeView from './HomeView.vue'

const groupFixture: GroupSummary = {
  id: 1,
  name: 'Example',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai'],
  models: [{ id: 'gpt-real', alias: '' }],
  enabled: true,
  key_count: 2,
}
const accessKeyFixture: AccessKeyOptionDto = {
  id: 1,
  name: 'Default',
  status: 'active',
}
const healthFixture: RuntimeHealthDto = {
  observed_at: '2026-07-25T10:00:00Z',
  snapshot_revision: 8,
  stats_window_seconds: 300,
  counts: { total: 2, available: 1, cooldown: 1, blacklisted: 0, disabled: 0 },
  groups: [
    {
      id: 1,
      name: 'Example',
      enabled: true,
      counts: { total: 2, available: 1, cooldown: 1, blacklisted: 0, disabled: 0 },
    },
  ],
  cooldown_keys: [],
  blacklisted_keys: [],
  request_log: {
    enqueued_total: 0,
    persisted_total: 0,
    dropped_not_running_total: 0,
    dropped_queue_full_total: 0,
    dropped_stopping_total: 0,
    dropped_persist_failed_total: 0,
    dropped_shutdown_total: 0,
    dropped_total: 0,
    write_failure_total: 0,
    retention_delete_failure_total: 0,
    queue_depth: 0,
    queue_capacity: 0,
    last_write_failure_at: null,
    last_retention_failure_at: null,
  },
}

const usageFixture: UsageReportDto = {
  observed_at: '2026-07-25T10:00:01Z',
  range: '24h',
  granularity: 'hour',
  timezone: 'UTC',
  from: '2026-07-24T11:00:00Z',
  to: '2026-07-25T11:00:00Z',
  summary: {
    request_count: 4,
    success_count: 3,
    failure_count: 1,
    uncached_input_tokens: 10,
    cache_read_tokens: 2,
    cache_write_5m_tokens: 0,
    cache_write_1h_tokens: 0,
    output_tokens: 5,
    total_tokens: 17,
    estimated_cost_usd: 0.0025,
    usage_missing_count: 0,
    partial_count: 0,
    unpriced_request_count: 0,
  },
  series: [],
  breakdown: [],
  breakdown_truncated: false,
  collection_health: {
    scope: 'current_process',
    dropped_total: healthFixture.request_log.dropped_total,
    write_failure_total: healthFixture.request_log.write_failure_total,
    last_write_failure_at: healthFixture.request_log.last_write_failure_at,
  },
}

async function mountHome(api: FakeApi, origin?: string, queryClient?: QueryClient) {
  api.when('/api/usage?range=24h').resolve(usageFixture)
  const mounted = await mountApp(HomeView, {
    api,
    queryClient: queryClient ?? new QueryClient({ defaultOptions: { queries: { retry: false } } }),
    mounting: origin ? { props: { origin } } : undefined,
  })
  await flushPromises()
  return mounted.wrapper
}

describe('HomeView', () => {
  it('keeps the semantic task order Operational Overview, Groups, then Connection Setup', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(healthFixture)
    api.when('/api/access-keys/options').resolve([accessKeyFixture])

    const wrapper = await mountHome(api)
    const sections = wrapper.findAll(
      '[data-test="home-operational-overview"], [data-test="home-groups"], [data-test="home-connection"]',
    )

    expect(sections.map((section) => section.attributes('data-test'))).toEqual([
      'home-operational-overview',
      'home-groups',
      'home-connection',
    ])
    expect(sections[0]?.find('[data-test="home-usage-requests"]').exists()).toBe(true)
  })

  it('keeps Groups visible when Health fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when('/api/access-keys/options').resolve([])

    const wrapper = await mountHome(api)

    expect(wrapper.get('[data-group-id="1"]').text()).toContain('Example')
    expect(wrapper.get('[data-group-id="1"]').text()).toContain('状态未知')
    expect(wrapper.text()).toContain('健康状态暂不可用')
    expect(wrapper.text()).not.toContain('离线')
  })

  it('keeps Health and connection sections when Groups fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when('/api/health').resolve(healthFixture)
    api.when('/api/access-keys/options').resolve([accessKeyFixture])

    const wrapper = await mountHome(api)

    expect(wrapper.text()).toContain('在线')
    expect(wrapper.text()).toContain('Base URL')
    expect(wrapper.text()).toContain('无法加载 Group')
  })

  it('keeps overview and Groups mounted when the Usage summary fails independently', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(healthFixture)
    api.when('/api/access-keys/options').resolve([accessKeyFixture])
    api
      .when('/api/usage?range=24h')
      .reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'usage-secret-canary'))

    const wrapper = await mountHome(api)

    expect(wrapper.text()).toContain('在线')
    expect(wrapper.text()).toContain('Base URL')
    expect(wrapper.get('[data-group-id="1"]').text()).toContain('Example')
    expect(wrapper.get('[data-test="home-usage-error"]').text()).toContain(
      '无法加载最近 24 小时用量摘要',
    )
    expect(wrapper.text()).not.toContain('usage-secret-canary')
  })

  it('retains stale Health data when a background refresh fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when('/api/access-keys/options').resolve([])
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(controlQueryKeys.health(), healthFixture)

    const wrapper = await mountHome(api, undefined, queryClient)

    expect(wrapper.text()).toContain('健康数据可能已过期')
    expect(wrapper.text()).toContain('快照修订 8')
    expect(wrapper.get('[data-group-id="1"]').text()).toContain('可服务')
  })

  it('retains masked stale AccessKey data when a background refresh fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(healthFixture)
    api
      .when('/api/access-keys/options')
      .reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(controlQueryKeys.accessKeys.options(), [accessKeyFixture])

    const wrapper = await mountHome(api, undefined, queryClient)

    expect(wrapper.text()).toContain('AccessKey 数据可能已过期')
    expect(wrapper.text()).toContain('Default')
    expect(wrapper.html()).not.toContain('ACCESS_KEY_CANARY')
  })

  it('retains stale Group data when a background refresh fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when('/api/health').resolve(healthFixture)
    api.when('/api/access-keys/options').resolve([])
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(controlQueryKeys.groups.list(), [groupFixture])

    const wrapper = await mountHome(api, undefined, queryClient)

    expect(wrapper.get('[data-group-id="1"]').text()).toContain('Example')
    expect(wrapper.text()).toContain('Group 数据可能已过期')
  })

  it('warns for loopback origins without blocking copy', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([])
    api.when('/api/health').resolve(healthFixture)
    api.when('/api/access-keys/options').resolve([])

    const wrapper = await mountHome(api, 'http://127.0.0.1:3001/')

    expect(wrapper.get('[role="note"]').text()).toContain('仅当前机器')
    expect(wrapper.get('[aria-label="复制 Base URL"]').attributes()).not.toHaveProperty('disabled')
  })

  it('renders real health and usage counts without fabricated health metrics or a chart', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(healthFixture)
    api
      .when('/api/access-keys/options')
      .resolve([{ ...accessKeyFixture, key: 'ACCESS_KEY_CANARY' }])

    const wrapper = await mountHome(api)

    expect(wrapper.get('[data-test="home-service-status"]').classes()).toContain(
      'service-status--normal',
    )
    expect(wrapper.get('[data-test="home-health-available"]').attributes('data-state')).toBe(
      'normal',
    )
    expect(wrapper.get('[data-test="home-health-cooldown"]').attributes('data-state')).toBe(
      'anomaly',
    )
    expect(wrapper.get('[data-test="home-health-cooldown"]').find('svg').exists()).toBe(true)
    expect(wrapper.get('[data-test="home-health-blacklisted"]').attributes('data-state')).toBe(
      'normal',
    )
    expect(wrapper.get('[data-test="home-health-disabled"]').attributes('data-state')).toBe(
      'normal',
    )
    expect(wrapper.text()).toContain('可用 1')
    expect(wrapper.text()).toContain('冷却 1')
    expect(wrapper.text()).toContain('修订 8')
    expect(wrapper.text()).toContain('最近 24 小时')
    expect(wrapper.text()).not.toMatch(/健康率|吞吐|趋势/)
    expect(wrapper.find('[data-test="usage-sparkline"]').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('ACCESS_KEY_CANARY')
  })

  it('shows first-run actions and a model placeholder without invented configuration', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([])
    api.when('/api/health').resolve({ ...healthFixture, groups: [] })
    api.when('/api/access-keys/options').resolve([])

    const wrapper = await mountHome(api)

    expect(wrapper.get('[href="/import"]').text()).toContain('导入上游密钥')
    expect(wrapper.get('[href="/access-keys"]').text()).toContain('创建 AccessKey')
    expect(wrapper.text()).toContain('<MODEL_ID>')
  })
})
