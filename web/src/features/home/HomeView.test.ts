import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import { ApiError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import type { AccessKeyDto, GroupSummary, RuntimeHealthDto } from '@/api/control/types'
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
const accessKeyFixture: AccessKeyDto = {
  id: 1,
  name: 'Default',
  key: 'ACCESS_KEY_CANARY',
  status: 'active',
  filters: { groups: [], protocols: [], models: [] },
  rpm_limit: 0,
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

async function mountHome(api: FakeApi, origin?: string, queryClient?: QueryClient) {
  const mounted = await mountApp(HomeView, {
    api,
    queryClient: queryClient ?? new QueryClient({ defaultOptions: { queries: { retry: false } } }),
    mounting: origin ? { props: { origin } } : undefined,
  })
  await flushPromises()
  return mounted.wrapper
}

describe('HomeView', () => {
  it('keeps Groups visible when Health fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when('/api/access-keys').resolve([])

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
    api.when('/api/access-keys').resolve([accessKeyFixture])

    const wrapper = await mountHome(api)

    expect(wrapper.text()).toContain('在线')
    expect(wrapper.text()).toContain('Base URL')
    expect(wrapper.text()).toContain('无法加载 Group')
  })

  it('retains stale Health data when a background refresh fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when('/api/access-keys').resolve([])
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
    api.when('/api/access-keys').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(controlQueryKeys.accessKeys.list(), [accessKeyFixture])

    const wrapper = await mountHome(api, undefined, queryClient)

    expect(wrapper.text()).toContain('AccessKey 数据可能已过期')
    expect(wrapper.text()).toContain('Default')
    expect(wrapper.html()).not.toContain('ACCESS_KEY_CANARY')
  })

  it('retains stale Group data when a background refresh fails', async () => {
    const api = new FakeApi()
    api.when('/api/groups').reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'failed'))
    api.when('/api/health').resolve(healthFixture)
    api.when('/api/access-keys').resolve([])
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
    api.when('/api/access-keys').resolve([])

    const wrapper = await mountHome(api, 'http://127.0.0.1:3001/')

    expect(wrapper.get('[role="note"]').text()).toContain('仅当前机器')
    expect(wrapper.get('[aria-label="复制 Base URL"]').attributes()).not.toHaveProperty('disabled')
  })

  it('renders real health counts and no fabricated dashboard metrics', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([groupFixture])
    api.when('/api/health').resolve(healthFixture)
    api.when('/api/access-keys').resolve([accessKeyFixture])

    const wrapper = await mountHome(api)

    expect(wrapper.text()).toContain('可用 1')
    expect(wrapper.text()).toContain('冷却 1')
    expect(wrapper.text()).toContain('修订 8')
    expect(wrapper.text()).not.toMatch(/成功率|健康率|吞吐|Token|费用|趋势/)
    expect(wrapper.html()).not.toContain('ACCESS_KEY_CANARY')
  })

  it('shows first-run actions and a model placeholder without invented configuration', async () => {
    const api = new FakeApi()
    api.when('/api/groups').resolve([])
    api.when('/api/health').resolve({ ...healthFixture, groups: [] })
    api.when('/api/access-keys').resolve([])

    const wrapper = await mountHome(api)

    expect(wrapper.get('[href="/import"]').text()).toContain('导入上游密钥')
    expect(wrapper.get('[href="/access-keys"]').text()).toContain('创建 AccessKey')
    expect(wrapper.text()).toContain('<MODEL_ID>')
  })
})
