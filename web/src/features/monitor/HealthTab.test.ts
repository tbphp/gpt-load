import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiPath } from '@/api/client'
import { ApiError } from '@/api/errors'
import type {
  HealthProblemKeyDto,
  KeyCounts,
  RequestLogHealthDto,
  RuntimeHealthDto,
} from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import HealthTab from './HealthTab.vue'

const zeroCounts: KeyCounts = {
  total: 0,
  available: 0,
  cooldown: 0,
  blacklisted: 0,
  disabled: 0,
}

const emptyRequestLog: RequestLogHealthDto = {
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
}

const cooldownKey: HealthProblemKeyDto & { key: string } = {
  key_id: 11,
  group_id: 7,
  group_name: 'Alpha',
  key: 'sk-never-render-this',
  cooldown_until: '2026-07-25T10:02:00Z',
  failure_count: 5,
  recent_success_count: 2,
  recent_failure_count: 3,
  consecutive_failure_count: 2,
  weight_manual: null,
  weight_auto: 80,
  recovery: {
    automatic: true,
    mode: 'cooldown_expiry',
    at: '2026-07-25T10:02:00Z',
  },
}

const blacklistedKey: HealthProblemKeyDto = {
  key_id: 12,
  group_id: 8,
  group_name: 'Disabled Beta',
  failure_count: 9,
  recent_success_count: 0,
  recent_failure_count: 5,
  consecutive_failure_count: 5,
  weight_manual: 40,
  weight_auto: 0,
  recovery: {
    automatic: true,
    mode: 'validation_probe',
    at: null,
  },
}

function healthFixture(overrides: Partial<RuntimeHealthDto> = {}): RuntimeHealthDto {
  return {
    observed_at: '2026-07-25T10:00:00Z',
    snapshot_revision: 42,
    stats_window_seconds: 300,
    counts: zeroCounts,
    groups: [],
    cooldown_keys: [],
    blacklisted_keys: [],
    request_log: emptyRequestLog,
    ...overrides,
  }
}

class HealthApi implements ApiClient {
  readonly requests: ApiPath[] = []

  constructor(private readonly respond: () => Promise<RuntimeHealthDto>) {}

  request<T>(path: ApiPath): Promise<T> {
    this.requests.push(path)
    return this.respond() as Promise<T>
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

function createQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountHealth(api: ApiClient, queryClient = createQueryClient()) {
  const mounted = await mountApp(HealthTab, {
    api,
    queryClient,
    path: '/monitor?tab=health',
  })
  await flushPromises()
  return { ...mounted, queryClient }
}

describe('HealthTab', () => {
  it.each([
    ['total zero', zeroCounts, '尚无 Key', ['总数 0', '可用 0', '冷却 0', '拉黑 0', '停用 0']],
    [
      'available zero',
      { total: 2, available: 0, cooldown: 1, blacklisted: 1, disabled: 0 },
      '当前无可用 Key',
      ['总数 2', '可用 0', '冷却 1', '拉黑 1', '停用 0'],
    ],
    [
      'available plus cooldown and blacklist',
      { total: 4, available: 2, cooldown: 1, blacklisted: 1, disabled: 0 },
      '2 个 Key 可用',
      ['总数 4', '可用 2', '冷却 1', '拉黑 1', '停用 0'],
    ],
  ] as const)('renders backend counts for %s', async (_name, counts, status, labels) => {
    const api = new HealthApi(() => Promise.resolve(healthFixture({ counts })))

    const { wrapper } = await mountHealth(api)

    expect(wrapper.text()).toContain(status)
    for (const label of labels) expect(wrapper.text()).toContain(label)
    expect(wrapper.text()).toContain('修订 42')
    expect(wrapper.text()).toContain('2026')
  })

  it('renders disabled Groups and an explicit no-runtime-exception state', async () => {
    const api = new HealthApi(() =>
      Promise.resolve(
        healthFixture({
          counts: { total: 3, available: 0, cooldown: 0, blacklisted: 0, disabled: 3 },
          groups: [
            {
              id: 8,
              name: 'Disabled Beta',
              enabled: false,
              counts: { total: 3, available: 0, cooldown: 0, blacklisted: 0, disabled: 3 },
            },
          ],
        }),
      ),
    )

    const { wrapper } = await mountHealth(api)

    expect(wrapper.get('a[href="/groups/8"]').text()).toBe('Disabled Beta')
    expect(wrapper.text()).toContain('Group 已停用')
    expect(wrapper.text()).toContain('当前没有冷却或拉黑的 Key')
  })

  it('shows problem Keys by ID with real recovery facts and no fabricated metric or secret', async () => {
    const api = new HealthApi(() =>
      Promise.resolve(
        healthFixture({
          counts: { total: 3, available: 1, cooldown: 1, blacklisted: 1, disabled: 0 },
          groups: [
            {
              id: 7,
              name: 'Alpha',
              enabled: true,
              counts: { total: 2, available: 1, cooldown: 1, blacklisted: 0, disabled: 0 },
            },
            {
              id: 8,
              name: 'Disabled Beta',
              enabled: false,
              counts: { total: 1, available: 0, cooldown: 0, blacklisted: 1, disabled: 0 },
            },
          ],
          cooldown_keys: [cooldownKey],
          blacklisted_keys: [blacklistedKey],
        }),
      ),
    )

    const { wrapper } = await mountHealth(api)

    expect(wrapper.text()).toContain('Key #11')
    expect(wrapper.text()).toContain('Key #12')
    expect(wrapper.text()).not.toContain('sk-')
    expect(wrapper.get('[data-test="remaining-11"]').text()).toContain('2:00')

    await wrapper.get('[data-test="problem-key-11"]').trigger('click')
    await wrapper.get('[data-test="problem-key-12"]').trigger('click')
    expect(wrapper.text()).toContain('冷却到期')
    expect(wrapper.text()).toContain('验证探测')
    expect(wrapper.text()).toContain('运行时决定探测时间')
    expect(wrapper.text()).not.toMatch(/下次探测.*\d/)
    expect(wrapper.text()).not.toMatch(/成功率|健康率|百分比|Usage|Token|费用|趋势/)
    expect(wrapper.find('svg[data-chart]').exists()).toBe(false)

    expect(wrapper.get('[data-test="failure-count-11"]').text()).toBe('5')
    expect(wrapper.get('[data-test="auto-weight-11"]').text()).toBe('80')
  })

  it('renders raw RequestLog pipeline counters and failure timestamps', async () => {
    const api = new HealthApi(() =>
      Promise.resolve(
        healthFixture({
          request_log: {
            enqueued_total: 101,
            persisted_total: 96,
            dropped_not_running_total: 1,
            dropped_queue_full_total: 2,
            dropped_stopping_total: 3,
            dropped_persist_failed_total: 4,
            dropped_shutdown_total: 5,
            dropped_total: 15,
            write_failure_total: 6,
            retention_delete_failure_total: 7,
            queue_depth: 8,
            queue_capacity: 64,
            last_write_failure_at: '2026-07-25T09:59:00Z',
            last_retention_failure_at: '2026-07-25T09:58:00Z',
          },
        }),
      ),
    )

    const { wrapper } = await mountHealth(api)

    expect(wrapper.get('[data-test="request-log-enqueued"]').text()).toBe('101')
    expect(wrapper.get('[data-test="request-log-persisted"]').text()).toBe('96')
    expect(wrapper.get('[data-test="request-log-dropped-queue-full"]').text()).toBe('2')
    expect(wrapper.get('[data-test="request-log-dropped-total"]').text()).toBe('15')
    expect(wrapper.get('[data-test="request-log-write-failures"]').text()).toBe('6')
    expect(wrapper.get('[data-test="request-log-retention-failures"]').text()).toBe('7')
    expect(wrapper.get('[data-test="request-log-queue-depth"]').text()).toBe('8')
    expect(wrapper.get('[data-test="request-log-queue-capacity"]').text()).toBe('64')
    expect(wrapper.text()).toContain('09:59')
    expect(wrapper.text()).toContain('09:58')
  })

  it('derives remaining time from observed_at plus local elapsed and freezes after refresh error', async () => {
    vi.useFakeTimers()
    vi.setSystemTime('2026-07-25T10:00:00Z')
    const refresh = deferred<RuntimeHealthDto>()
    const api = new HealthApi(() => refresh.promise)
    const queryClient = createQueryClient()
    queryClient.setQueryData(
      controlQueryKeys.health(),
      healthFixture({
        counts: { total: 1, available: 0, cooldown: 1, blacklisted: 0, disabled: 0 },
        cooldown_keys: [cooldownKey],
      }),
    )

    const { wrapper } = await mountHealth(api, queryClient)

    expect(wrapper.get('[data-test="remaining-11"]').text()).toContain('2:00')
    await vi.advanceTimersByTimeAsync(30_000)
    expect(wrapper.get('[data-test="remaining-11"]').text()).toContain('1:30')

    refresh.reject(new ApiError(500, 'INTERNAL_SERVER_ERROR', 'refresh failed'))
    await flushPromises()
    expect(wrapper.text()).toContain('健康数据可能已过期')

    await vi.advanceTimersByTimeAsync(30_000)
    expect(wrapper.get('[data-test="remaining-11"]').text()).toContain('1:30')
  })

  it('pauses polling while hidden and refreshes exactly once when visible again', async () => {
    vi.useFakeTimers()
    let visibilityState: DocumentVisibilityState = 'visible'
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibilityState)
    const api = new HealthApi(() => Promise.resolve(healthFixture()))

    await mountHealth(api)
    expect(api.requests).toHaveLength(1)

    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.requests).toHaveLength(2)

    visibilityState = 'hidden'
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(30_000)
    expect(api.requests).toHaveLength(2)

    visibilityState = 'visible'
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(api.requests).toHaveLength(3)
  })

  it('keeps the expanded problem Key selected by key_id after refreshed arrays reorder', async () => {
    const secondCooldownKey: HealthProblemKeyDto = {
      ...cooldownKey,
      key_id: 13,
      failure_count: 1,
    }
    const initial = healthFixture({
      counts: { total: 2, available: 0, cooldown: 2, blacklisted: 0, disabled: 0 },
      cooldown_keys: [cooldownKey, secondCooldownKey],
    })
    const queryClient = createQueryClient()
    const api = new HealthApi(() => Promise.resolve(initial))
    const { wrapper } = await mountHealth(api, queryClient)

    await wrapper.get('[data-test="problem-key-11"]').trigger('click')
    expect(wrapper.get('[data-test="failure-count-11"]').text()).toBe('5')

    queryClient.setQueryData(controlQueryKeys.health(), {
      ...initial,
      observed_at: '2026-07-25T10:00:10Z',
      cooldown_keys: [secondCooldownKey, { ...cooldownKey, failure_count: 9 }],
    })
    await flushPromises()

    expect(wrapper.get('[data-test="problem-key-11"]').attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[data-test="failure-count-11"]').text()).toBe('9')
  })
})
