import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import type {
  RequestLogAttemptDto,
  RequestLogItemDto,
  RequestLogPageDto,
} from '@/app/resources/request-logs'
import type { AccessKeyOptionDto, GroupSummary } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import LogsTab from './LogsTab.vue'

const firstRequestID = 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173'

afterEach(() => {
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
})

function attemptFixture(sequence: number): RequestLogAttemptDto {
  return {
    sequence,
    group_id: 7,
    group_name: 'Historical Group',
    key_id: 21,
    upstream_model: 'gpt-upstream',
    status_code: 200,
    duration_ms: 100,
    failure_category: 'ok',
    action: 'terminate',
    will_retry: false,
    error_code: '',
    error_summary: '',
    committed: true,
  }
}

function logFixture(overrides: Partial<RequestLogItemDto> = {}): RequestLogItemDto {
  return {
    request_id: firstRequestID,
    completed_at: '2026-07-25T10:00:01Z',
    access_key: { id: 12, name: 'client', deleted: false },
    protocol: 'openai-chat-completions',
    client_model: 'gpt-client',
    upstream_model: 'gpt-upstream',
    status: 'success',
    status_code: 200,
    duration_ms: 125,
    error_code: '',
    error_summary: '',
    affinity_hit: false,
    attempts: [],
    group_id: 7,
    usage_state: 'complete',
    cost_state: 'priced',
    uncached_input_tokens: 100,
    cache_read_tokens: 20,
    cache_write_5m_tokens: 3,
    cache_write_1h_tokens: 4,
    output_tokens: 50,
    estimated_cost_usd: 0.123,
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

class LogsApi implements ApiClient {
  readonly requests: Array<{ path: ApiPath; options?: ApiRequestOptions }> = []
  private logRequestCount = 0

  constructor(
    private readonly pages: Array<RequestLogPageDto | Promise<RequestLogPageDto>> = [
      {
        items: [logFixture()],
        next_cursor: null,
      },
    ],
    private readonly options: {
      groups?: GroupSummary[] | Promise<GroupSummary[]>
      accessKeys?: AccessKeyOptionDto[] | Promise<AccessKeyOptionDto[]>
      groupsError?: Error
      accessKeysError?: Error
    } = {},
  ) {}

  request<T>(path: ApiPath, options?: ApiRequestOptions): Promise<T> {
    this.requests.push({ path, options })
    if (path.startsWith('/api/logs')) {
      const page = this.pages[this.logRequestCount] ?? this.pages.at(-1)
      this.logRequestCount += 1
      return Promise.resolve(page as T)
    }
    if (path === '/api/groups') {
      if (this.options.groupsError) return Promise.reject(this.options.groupsError)
      return Promise.resolve((this.options.groups ?? []) as T)
    }
    if (path === '/api/access-keys/options') {
      if (this.options.accessKeysError) return Promise.reject(this.options.accessKeysError)
      return Promise.resolve((this.options.accessKeys ?? []) as T)
    }
    throw new Error(`Unexpected request: ${path}`)
  }
}

function createQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountLogs(
  api: ApiClient,
  path = '/monitor?tab=logs',
  locale: 'zh-CN' | 'en-US' | 'ja-JP' = 'zh-CN',
) {
  const queryClient = createQueryClient()
  const mounted = await mountApp(LogsTab, {
    api,
    queryClient,
    path,
    locale,
    mounting: { attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient }
}

describe('LogsTab', () => {
  it('loads the first page without implicit time, limit, page, or cursor parameters', async () => {
    const api = new LogsApi([
      {
        items: [
          logFixture({
            protocol: 'anthropic',
            upstream_model: 'claude-upstream',
            status: 'incomplete',
            status_code: 206,
            duration_ms: 125,
            attempts: [attemptFixture(1), attemptFixture(2)],
          }),
        ],
        next_cursor: null,
      },
    ])

    const { wrapper } = await mountLogs(api)

    expect(api.requests.filter(({ path }) => path.startsWith('/api/logs'))).toEqual([
      {
        path: '/api/logs',
        options: { method: 'GET', signal: expect.any(AbortSignal) },
      },
    ])
    const row = wrapper.get(`[data-test="log-row-${firstRequestID}"]`).text()
    for (const fact of [
      '2026-07-25T10:00:01Z',
      firstRequestID,
      'client · #12',
      'Anthropic',
      'gpt-client',
      'claude-upstream',
      '未完成',
      '206',
      '125 ms',
      '2 次尝试',
      '查看详情',
    ]) {
      expect(row).toContain(fact)
    }
  })

  it('keeps the existing seven table columns and identifies list/filter model values as client models', async () => {
    const { wrapper } = await mountLogs(new LogsApi(), '/monitor?tab=logs', 'en-US')

    expect(wrapper.findAll('thead th').map((header) => header.text())).toEqual([
      'Completed',
      'Request ID',
      'Protocol and models',
      'AccessKey',
      'Final status',
      'Duration and attempts',
      'Actions',
    ])
    expect(wrapper.get('label[for="logs-model"]').text()).toBe('Client model')
    const routeCell = wrapper.get(`[data-test="log-row-${firstRequestID}"] td:nth-child(3)`).text()
    expect(routeCell).toContain('Client model')
    expect(routeCell).toContain('gpt-client')
    expect(routeCell).toContain('Upstream model')
    expect(routeCell).toContain('gpt-upstream')
  })

  it('passes the opaque forward cursor unchanged, appends once, and hides Load more at the end', async () => {
    const opaqueCursor = 'opaque:server/+token=='
    const secondRequestID = 'b4d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const api = new LogsApi([
      {
        items: [logFixture()],
        next_cursor: opaqueCursor,
      },
      {
        items: [logFixture({ request_id: secondRequestID, client_model: 'second-model' })],
        next_cursor: null,
      },
    ])

    const { wrapper } = await mountLogs(api)
    await wrapper.get('[data-test="logs-load-more"]').trigger('click')
    await flushPromises()

    const logRequests = api.requests.filter(({ path }) => path.startsWith('/api/logs'))
    expect(logRequests).toHaveLength(2)
    expect(
      new URL(logRequests[1]!.path, 'https://gpt-load.invalid').searchParams.get('cursor'),
    ).toBe(opaqueCursor)
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    expect(wrapper.text()).toContain('second-model')
    expect(wrapper.find('[data-test="logs-load-more"]').exists()).toBe(false)
  })

  it('keeps loaded rows and retries only the failed next page with safe local feedback', async () => {
    const nextPageFailure = deferred<RequestLogPageDto>()
    const nextRequestID = '94d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const api = new LogsApi([
      { items: [logFixture()], next_cursor: 'page-2' },
      nextPageFailure.promise,
      {
        items: [logFixture({ request_id: nextRequestID, client_model: 'retried-next-model' })],
        next_cursor: null,
      },
    ])
    const { wrapper } = await mountLogs(api)

    await wrapper.get('[data-test="logs-load-more"]').trigger('click')
    nextPageFailure.reject(new Error('next-page-secret-canary'))
    await flushPromises()

    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.find(`[data-test="log-row-${firstRequestID}"]`).exists()).toBe(true)
    expect(wrapper.get('[data-test="logs-next-page-failed"]').text()).toContain('加载下一页失败')
    expect(wrapper.find('[data-test="logs-stale"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('next-page-secret-canary')

    await wrapper.get('[data-test="logs-next-page-retry"]').trigger('click')
    await flushPromises()

    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    expect(wrapper.get(`[data-test="log-row-${nextRequestID}"]`).text()).toContain(
      'retried-next-model',
    )
    expect(wrapper.find('[data-test="logs-next-page-failed"]').exists()).toBe(false)
  })

  it('applies filters through the URL and clears prior rows while the new first page loads', async () => {
    const filtered = deferred<RequestLogPageDto>()
    const filteredRequestID = 'c4d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const api = new LogsApi([{ items: [logFixture()], next_cursor: null }, filtered.promise])
    const { router, wrapper } = await mountLogs(api)

    await wrapper.get('[data-test="logs-status"]').setValue('error')
    await wrapper.get('[data-test="logs-filter-form"]').trigger('submit')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=logs&status=error')
    expect(wrapper.find(`[data-test="log-row-${firstRequestID}"]`).exists()).toBe(false)

    filtered.resolve({
      items: [
        logFixture({
          request_id: filteredRequestID,
          client_model: 'filtered-model',
          status: 'error',
        }),
      ],
      next_cursor: null,
    })
    await flushPromises()

    expect(wrapper.find(`[data-test="log-row-${firstRequestID}"]`).exists()).toBe(false)
    expect(wrapper.get(`[data-test="log-row-${filteredRequestID}"]`).text()).toContain(
      'filtered-model',
    )
  })

  it('cancels an old-filter page request and ignores its late response after Apply', async () => {
    const oldNext = deferred<RequestLogPageDto>()
    const filtered = deferred<RequestLogPageDto>()
    const lateRequestID = 'd4d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const filteredRequestID = 'e4d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const api = new LogsApi([
      { items: [logFixture()], next_cursor: 'old-next' },
      oldNext.promise,
      filtered.promise,
    ])
    const { wrapper } = await mountLogs(api)

    await wrapper.get('[data-test="logs-load-more"]').trigger('click')
    await flushPromises()
    const oldPageRequest = api.requests.filter(({ path }) => path.startsWith('/api/logs'))[1]
    expect(oldPageRequest?.options?.signal?.aborted).toBe(false)

    await wrapper.get('[data-test="logs-status"]').setValue('error')
    await wrapper.get('[data-test="logs-filter-form"]').trigger('submit')
    await flushPromises()

    expect(oldPageRequest?.options?.signal?.aborted).toBe(true)
    expect(wrapper.find(`[data-test="log-row-${firstRequestID}"]`).exists()).toBe(false)

    oldNext.resolve({
      items: [logFixture({ request_id: lateRequestID, client_model: 'late-old-model' })],
      next_cursor: null,
    })
    await flushPromises()
    expect(wrapper.find(`[data-test="log-row-${lateRequestID}"]`).exists()).toBe(false)

    filtered.resolve({
      items: [
        logFixture({
          request_id: filteredRequestID,
          client_model: 'current-model',
          status: 'error',
        }),
      ],
      next_cursor: null,
    })
    await flushPromises()

    expect(wrapper.find(`[data-test="log-row-${lateRequestID}"]`).exists()).toBe(false)
    expect(wrapper.get(`[data-test="log-row-${filteredRequestID}"]`).text()).toContain(
      'current-model',
    )
  })

  it('refreshes the first page atomically and ignores a canceled late next page', async () => {
    const inFlightNext = deferred<RequestLogPageDto>()
    const refreshed = deferred<RequestLogPageDto>()
    const secondRequestID = 'f4d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const lateRequestID = '14d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const refreshedRequestID = '24d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const refreshedPage: RequestLogPageDto = {
      items: [
        logFixture({
          request_id: refreshedRequestID,
          client_model: 'refreshed-model',
          status: 'error',
        }),
      ],
      next_cursor: null,
    }
    const api = new LogsApi([
      { items: [logFixture({ status: 'error' })], next_cursor: 'page-2' },
      {
        items: [
          logFixture({
            request_id: secondRequestID,
            client_model: 'second-page-model',
            status: 'error',
          }),
        ],
        next_cursor: 'page-3',
      },
      inFlightNext.promise,
      refreshed.promise,
    ])
    const { queryClient, wrapper } = await mountLogs(api, '/monitor?tab=logs&status=error')

    await wrapper.get('[data-test="logs-load-more"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="logs-load-more"]').trigger('click')
    await flushPromises()
    const inFlightRequest = api.requests.filter(({ path }) => path.startsWith('/api/logs'))[2]
    expect(inFlightRequest?.options?.signal?.aborted).toBe(false)

    await wrapper.get('[data-test="logs-refresh"]').trigger('click')
    await flushPromises()

    expect(inFlightRequest?.options?.signal?.aborted).toBe(true)
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    const refreshRequest = api.requests.filter(({ path }) => path.startsWith('/api/logs'))[3]
    expect(refreshRequest?.path).toBe('/api/logs?status=error')

    refreshed.resolve(refreshedPage)
    await flushPromises()

    expect(queryClient.getQueryData(controlQueryKeys.logs.list({ status: 'error' }))).toEqual({
      pages: [refreshedPage],
      pageParams: [null],
    })
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.text()).toContain('refreshed-model')

    inFlightNext.resolve({
      items: [logFixture({ request_id: lateRequestID, client_model: 'late-page-model' })],
      next_cursor: null,
    })
    await flushPromises()
    expect(wrapper.find(`[data-test="log-row-${lateRequestID}"]`).exists()).toBe(false)
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
  })

  it('prevents Load more from starting or overwriting data while Refresh is pending', async () => {
    const refresh = deferred<RequestLogPageDto>()
    const prohibitedNextPage = deferred<RequestLogPageDto>()
    const refreshedRequestID = '64d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const prohibitedRequestID = '74d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const refreshedPage: RequestLogPageDto = {
      items: [
        logFixture({
          request_id: refreshedRequestID,
          client_model: 'refresh-wins',
        }),
      ],
      next_cursor: null,
    }
    const api = new LogsApi([
      { items: [logFixture()], next_cursor: 'page-2' },
      refresh.promise,
      prohibitedNextPage.promise,
    ])
    const { queryClient, wrapper } = await mountLogs(api)

    await wrapper.get('[data-test="logs-refresh"]').trigger('click')
    await flushPromises()
    const loadMore = wrapper.get<HTMLButtonElement>('[data-test="logs-load-more"]')
    expect(loadMore.attributes('disabled')).toBeDefined()

    loadMore.element.removeAttribute('disabled')
    await loadMore.trigger('click')
    await flushPromises()
    expect(api.requests.filter(({ path }) => path.startsWith('/api/logs'))).toHaveLength(2)

    refresh.resolve(refreshedPage)
    await flushPromises()
    prohibitedNextPage.resolve({
      items: [
        logFixture({
          request_id: prohibitedRequestID,
          client_model: 'prohibited-late-page',
        }),
      ],
      next_cursor: null,
    })
    await flushPromises()

    expect(queryClient.getQueryData(controlQueryKeys.logs.list({}))).toEqual({
      pages: [refreshedPage],
      pageParams: [null],
    })
    expect(wrapper.text()).toContain('refresh-wins')
    expect(wrapper.text()).not.toContain('prohibited-late-page')
  })

  it('preserves every cached page when Refresh fails and offers a local retry', async () => {
    vi.stubEnv('TZ', 'UTC')
    const refresh = deferred<RequestLogPageDto>()
    const retry = deferred<RequestLogPageDto>()
    const secondRequestID = '34d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const retriedRequestID = '44d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const firstPage: RequestLogPageDto = {
      items: [logFixture({ status: 'error' })],
      next_cursor: 'page-2',
    }
    const secondPage: RequestLogPageDto = {
      items: [
        logFixture({
          request_id: secondRequestID,
          client_model: 'second-page-model',
          status: 'error',
        }),
      ],
      next_cursor: null,
    }
    const api = new LogsApi([firstPage, secondPage, refresh.promise, retry.promise])
    const { queryClient, wrapper } = await mountLogs(api, '/monitor?tab=logs&status=error')
    await wrapper.get('[data-test="logs-load-more"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="logs-model"]').setValue('unapplied-model')
    const lastSuccessful = wrapper.get('[data-test="logs-last-refreshed"]').attributes('datetime')

    expect(lastSuccessful).toBeTruthy()
    expect(wrapper.get('[data-test="logs-timezone"]').text()).toContain('UTC')

    await wrapper.get('[data-test="logs-refresh"]').trigger('click')
    await flushPromises()
    refresh.reject(new Error('refresh-secret-canary'))
    await flushPromises()

    expect(queryClient.getQueryData(controlQueryKeys.logs.list({ status: 'error' }))).toEqual({
      pages: [firstPage, secondPage],
      pageParams: [null, 'page-2'],
    })
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    expect(wrapper.get('[data-test="logs-refresh-failed"]').text()).toContain('刷新请求日志失败')
    expect(wrapper.get('[data-test="logs-stale"]').text()).toContain('最近一次成功')
    expect(wrapper.get('[data-test="logs-last-refreshed"]').attributes('datetime')).toBe(
      lastSuccessful,
    )
    expect(wrapper.get<HTMLInputElement>('[data-test="logs-model"]').element.value).toBe(
      'unapplied-model',
    )
    expect(wrapper.text()).not.toContain('refresh-secret-canary')

    await wrapper.get('[data-test="logs-refresh-retry"]').trigger('click')
    await flushPromises()
    retry.resolve({
      items: [
        logFixture({
          request_id: retriedRequestID,
          client_model: 'retried-model',
          status: 'error',
        }),
      ],
      next_cursor: null,
    })
    await flushPromises()

    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.text()).toContain('retried-model')
    expect(wrapper.find('[data-test="logs-refresh-failed"]').exists()).toBe(false)
  })

  it('distinguishes a system with no logs from a filter with no matches', async () => {
    const unfiltered = await mountLogs(new LogsApi([{ items: [], next_cursor: null }]))

    expect(unfiltered.wrapper.get('[data-test="logs-empty-unfiltered"]').text()).toContain(
      '尚无请求日志',
    )
    unfiltered.wrapper.unmount()

    const filtered = await mountLogs(
      new LogsApi([{ items: [], next_cursor: null }]),
      '/monitor?tab=logs&model=no-match',
    )

    expect(filtered.wrapper.get('[data-test="logs-empty-filtered"]').text()).toContain(
      '没有匹配当前筛选条件',
    )
  })

  it('disables only the failed selector, preserves its deep-link value, and caches safe options', async () => {
    const groupError = 'group-options-secret-canary'
    const api = new LogsApi([{ items: [logFixture()], next_cursor: null }], {
      groupsError: new Error(groupError),
      accessKeys: [
        {
          id: 12,
          name: 'client',
          status: 'active',
        },
      ],
    })

    const { queryClient, wrapper } = await mountLogs(api, '/monitor?tab=logs&group_id=7')

    expect(wrapper.find(`[data-test="log-row-${firstRequestID}"]`).exists()).toBe(true)
    expect(wrapper.get('[data-test="logs-group-options-failed"]').text()).toContain(
      '无法加载 Group 筛选选项',
    )
    expect(wrapper.text()).not.toContain(groupError)
    expect(wrapper.get<HTMLSelectElement>('[data-test="logs-group"]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLSelectElement>('[data-test="logs-group"]').element.value).toBe('7')
    expect(wrapper.get('[data-test="logs-group"]').text()).toContain('#7')
    expect(wrapper.get<HTMLSelectElement>('[data-test="logs-access-key"]').element.disabled).toBe(
      false,
    )
    expect(queryClient.getQueryData(controlQueryKeys.accessKeys.options())).toEqual([
      { id: 12, name: 'client', status: 'active' },
    ])

    wrapper.unmount()
    await flushPromises()
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(queryClient.getQueryData(controlQueryKeys.accessKeys.options())).toBeUndefined()
  })

  it('isolates an AccessKey option failure without disabling Group filtering', async () => {
    const api = new LogsApi([{ items: [logFixture()], next_cursor: null }], {
      groups: [
        {
          id: 7,
          name: 'Primary',
          upstream_url: 'https://api.example.com',
          protocols: ['openai-chat-completions'],
          models: [],
          enabled: true,
          key_count: 1,
        },
      ],
      accessKeysError: new Error('access-key-options-secret-canary'),
    })

    const { wrapper } = await mountLogs(api, '/monitor?tab=logs&access_key_id=12')

    expect(wrapper.get<HTMLSelectElement>('[data-test="logs-group"]').element.disabled).toBe(false)
    expect(wrapper.get<HTMLSelectElement>('[data-test="logs-access-key"]').element.disabled).toBe(
      true,
    )
    expect(wrapper.get<HTMLSelectElement>('[data-test="logs-access-key"]').element.value).toBe('12')
    expect(wrapper.get('[data-test="logs-access-key"]').text()).toContain('#12')
    expect(wrapper.get('[data-test="logs-access-key-options-failed"]').text()).toContain(
      '无法加载 AccessKey 筛选选项',
    )
    expect(wrapper.text()).not.toContain('access-key-options-secret-canary')
    expect(wrapper.find(`[data-test="log-row-${firstRequestID}"]`).exists()).toBe(true)
  })

  it('marks a changed draft with localized text and a non-color icon until it is applied', async () => {
    const { wrapper } = await mountLogs(new LogsApi())

    expect(wrapper.find('[data-test="logs-filter-dirty"]').exists()).toBe(false)

    await wrapper.get('[data-test="logs-model"]').setValue('draft-only-model')

    const dirty = wrapper.get('[data-test="logs-filter-dirty"]')
    expect(dirty.text()).toContain('筛选草稿尚未应用')
    expect(dirty.find('svg[aria-hidden="true"]').exists()).toBe(true)

    await wrapper.get('[data-test="logs-filter-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-test="logs-filter-dirty"]').exists()).toBe(false)
  })

  it('applies every explicit filter once while Reset changes only the local draft', async () => {
    vi.stubEnv('TZ', 'UTC')
    const group: GroupSummary = {
      id: 7,
      name: 'Primary',
      upstream_url: 'https://api.example.com',
      protocols: ['openai-chat-completions'],
      models: [],
      enabled: true,
      key_count: 1,
    }
    const accessKey: AccessKeyOptionDto = {
      id: 12,
      name: 'client',
      status: 'active',
    }
    const api = new LogsApi(
      [
        { items: [logFixture()], next_cursor: null },
        { items: [logFixture({ status: 'error' })], next_cursor: null },
        { items: [logFixture()], next_cursor: null },
      ],
      { groups: [group], accessKeys: [accessKey] },
    )
    const { router, wrapper } = await mountLogs(api)

    await wrapper.get('[data-test="logs-from"]').setValue('2026-07-25T10:00')
    await wrapper.get('[data-test="logs-to"]').setValue('2026-07-25T11:00')
    await wrapper.get('[data-test="logs-group"]').setValue('7')
    await wrapper.get('[data-test="logs-model"]').setValue('provider/model:Exact')
    await wrapper.get('[data-test="logs-access-key"]').setValue('12')
    await wrapper.get('[data-test="logs-status"]').setValue('error')
    await wrapper
      .get('[data-test="logs-request-id"]')
      .setValue('a4d4e121-8ac3-4df4-8ceb-63b10ddc6173')
    await wrapper.get('[data-test="logs-filter-form"]').trigger('submit')
    await flushPromises()

    expect(router.currentRoute.value.query).toEqual({
      tab: 'logs',
      from: '2026-07-25T10:00:00.000Z',
      to: '2026-07-25T11:00:00.000Z',
      group_id: '7',
      model: 'provider/model:Exact',
      access_key_id: '12',
      status: 'error',
      request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
    })
    expect(wrapper.text()).toContain('匹配任一尝试')

    await wrapper.get('[data-test="logs-reset"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query).toEqual({
      tab: 'logs',
      from: '2026-07-25T10:00:00.000Z',
      to: '2026-07-25T11:00:00.000Z',
      group_id: '7',
      model: 'provider/model:Exact',
      access_key_id: '12',
      status: 'error',
      request_id: 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173',
    })
    expect(api.requests.filter(({ path }) => path.startsWith('/api/logs'))).toHaveLength(2)
    for (const testID of [
      'logs-from',
      'logs-to',
      'logs-group',
      'logs-model',
      'logs-access-key',
      'logs-status',
      'logs-request-id',
    ]) {
      expect((wrapper.get(`[data-test="${testID}"]`).element as HTMLInputElement).value).toBe('')
    }
    expect(wrapper.get('[data-test="logs-applied-filters"]').text()).toContain(
      'provider/model:Exact',
    )

    await wrapper.get('[data-test="logs-filter-form"]').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=logs')
    expect(api.requests.filter(({ path }) => path.startsWith('/api/logs'))).toHaveLength(3)
  })

  it('bypasses its own dirty prompt on Apply and keeps applied chips isolated from the draft', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const api = new LogsApi([
      { items: [logFixture({ status: 'success' })], next_cursor: null },
      { items: [logFixture({ status: 'error' })], next_cursor: null },
    ])
    const { router, wrapper } = await mountLogs(
      api,
      `/monitor?tab=logs&status=success&selected_request_id=${firstRequestID}`,
      'en-US',
    )

    await wrapper.get('[data-test="logs-status"]').setValue('error')

    const appliedBefore = wrapper.get('[data-test="logs-applied-filters"]').text()
    expect(appliedBefore).toContain('Success')
    expect(appliedBefore).not.toContain('Error')

    await wrapper.get('[data-test="logs-filter-form"]').trigger('submit')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=logs&status=error')
    expect(confirm).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="logs-applied-filters"]').text()).toContain('Error')
  })

  it('rejects invalid fields locally and marks every field error with aria-invalid', async () => {
    const rawInvalid = 'NOT-A-UUID-secret-canary'
    const api = new LogsApi()
    const invalidPath =
      '/monitor?tab=logs&group_id=0&model=%20model-with-space&access_key_id=1.5&status=failed&request_id=NOT-A-UUID-secret-canary'
    const { router, wrapper } = await mountLogs(api, invalidPath)

    const from = wrapper.get<HTMLInputElement>('[data-test="logs-from"]')
    from.element.type = 'text'
    await from.setValue('2026-02-30T10:00')
    await wrapper.get('[data-test="logs-to"]').setValue('2026-07-25T10:00')
    await wrapper.get('[data-test="logs-filter-form"]').trigger('submit')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe(invalidPath)
    expect(api.requests.filter(({ path }) => path.startsWith('/api/logs'))).toHaveLength(1)
    expect(wrapper.text()).toContain('请输入有效的本地日期时间')
    expect(wrapper.text()).toContain('客户端模型不能包含首尾空白或控制字符')
    expect(wrapper.text()).toContain('请输入规范的小写 UUIDv4')
    expect(wrapper.text()).not.toContain(rawInvalid)
    for (const testID of [
      'logs-from',
      'logs-group',
      'logs-model',
      'logs-access-key',
      'logs-status',
      'logs-request-id',
    ]) {
      expect(wrapper.get(`[data-test="${testID}"]`).attributes('aria-invalid')).toBe('true')
    }
    expect(wrapper.get('[data-test="logs-to"]').attributes('aria-invalid')).toBeUndefined()
  })

  it('does not append the same request twice when a next-page response is duplicated', async () => {
    const secondRequestID = '54d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    const api = new LogsApi([
      { items: [logFixture()], next_cursor: 'duplicate-next' },
      {
        items: [
          logFixture(),
          logFixture({ request_id: secondRequestID, client_model: 'unique-next-model' }),
        ],
        next_cursor: null,
      },
    ])
    const { wrapper } = await mountLogs(api)

    await wrapper.get('[data-test="logs-load-more"]').trigger('click')
    await flushPromises()

    expect(wrapper.findAll(`[data-test="log-row-${firstRequestID}"]`)).toHaveLength(1)
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    expect(wrapper.text()).toContain('unique-next-model')
  })

  it('labels a deleted historical AccessKey by numeric ID', async () => {
    const api = new LogsApi([
      {
        items: [
          logFixture({
            access_key: { id: 99, name: null, deleted: true },
          }),
        ],
        next_cursor: null,
      },
    ])

    const { wrapper } = await mountLogs(api, '/monitor?tab=logs', 'en-US')

    expect(wrapper.get(`[data-test="log-row-${firstRequestID}"]`).text()).toContain('Deleted · #99')
  })

  it('drives the detail drawer from the canonical URL and restores trigger focus on close', async () => {
    const api = new LogsApi()
    const { queryClient, router, wrapper } = await mountLogs(api, '/monitor?tab=logs', 'en-US')
    const trigger = wrapper.get<HTMLButtonElement>(`[data-test="log-details-${firstRequestID}"]`)
    trigger.element.focus()

    await trigger.trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query).toEqual({
      tab: 'logs',
      selected_request_id: firstRequestID,
    })
    expect(api.requests.filter(({ path }) => path.startsWith('/api/logs'))).toHaveLength(1)
    expect(JSON.stringify(queryClient.getQueryCache().getAll())).not.toContain(
      'selected_request_id',
    )
    expect(document.body.textContent).toContain('Request log details')
    const close = document.body.querySelector<HTMLButtonElement>(
      'button[aria-label="Close request log details"]',
    )
    if (!close) throw new Error('Missing drawer close button')
    close.click()
    await flushPromises()
    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=logs')
    expect(document.body.querySelector('button[aria-label="Close request log details"]')).toBeNull()
    expect(document.activeElement).toBe(
      wrapper.get(`[data-test="log-details-${firstRequestID}"]`).element,
    )
  })

  it('restores a deep-linked selected request and its applied filters through browser history', async () => {
    const selectedPath = `/monitor?tab=logs&status=error&selected_request_id=${firstRequestID}`
    const { router, wrapper } = await mountLogs(
      new LogsApi([{ items: [logFixture({ status: 'error' })], next_cursor: null }]),
      selectedPath,
      'en-US',
    )

    expect(document.body.textContent).toContain('Request log details')
    const inspectorLink = document.body.querySelector<HTMLAnchorElement>(
      '[data-test="log-inspector-link"]',
    )
    if (!inspectorLink) throw new Error('Missing Inspector link')
    inspectorLink.click()
    await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('inspector')

    router.back()
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe(selectedPath)
    expect(wrapper.get<HTMLSelectElement>('[data-test="logs-status"]').element.value).toBe('error')
    expect(document.body.textContent).toContain('Request log details')
  })
})
