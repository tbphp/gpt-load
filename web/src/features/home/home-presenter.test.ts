import type { GroupSummary } from '@/api/control/types'
import type { RuntimeHealthDto } from '@/app/resources/health'
import type { UsageReportDto } from '@/app/resources/usage'

import {
  failureLogsLocation,
  presentHome,
  presentHomeHealth,
  presentHomeInventory,
  presentHomeUsage,
  problemKeysLocation,
  usageBreakdownLocation,
} from './home-presenter'
import presenterSource from './home-presenter?raw'

function group(id: number, name = `Group ${id}`): GroupSummary {
  return {
    id,
    name,
    upstream_url: 'https://example.test',
    protocols: ['openai-chat-completions'],
    models: [{ id: 'model', alias: '' }],
    enabled: true,
    key_count: 1,
  }
}

const requestLog: RuntimeHealthDto['request_log'] = {
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

function health(overrides: Partial<RuntimeHealthDto> = {}): RuntimeHealthDto {
  return {
    observed_at: '2026-07-29T06:32:07Z',
    snapshot_revision: 28,
    stats_window_seconds: 300,
    counts: { total: 3, available: 3, cooldown: 0, blacklisted: 0, disabled: 0 },
    groups: [],
    cooldown_keys: [],
    blacklisted_keys: [],
    request_log: requestLog,
    ...overrides,
  }
}

function problemKey(keyID: number, groupID: number, status: 'cooldown' | 'blacklisted') {
  const cooldown = status === 'cooldown'
  return {
    key_id: keyID,
    group_id: groupID,
    group_name: `Group ${groupID}`,
    ...(cooldown ? { cooldown_until: '2026-07-29T06:36:00Z' } : {}),
    failure_count: 6,
    recent_success_count: 0,
    recent_failure_count: 6,
    consecutive_failure_count: 6,
    weight_manual: null,
    weight_auto: cooldown ? 40 : 0,
    recovery: cooldown
      ? {
          automatic: true,
          mode: 'cooldown_expiry',
          at: '2026-07-29T06:36:00Z',
        }
      : { automatic: true, mode: 'validation_probe', at: null },
    mask: `key${keyID}****safe`,
    last_failure_category: cooldown ? ('rate_limited' as const) : ('invalid_key' as const),
    last_status_code: cooldown ? 429 : 401,
  }
}

const aggregate = {
  request_count: 20,
  success_count: 15,
  failure_count: 5,
  uncached_input_tokens: 100,
  cache_read_tokens: 20,
  cache_write_5m_tokens: 0,
  cache_write_1h_tokens: 0,
  output_tokens: 40,
  total_tokens: 160,
  estimated_cost_usd: 2,
  usage_missing_count: 0,
  partial_count: 0,
  unpriced_request_count: 0,
}

function usage(overrides: Partial<UsageReportDto> = {}): UsageReportDto {
  return {
    range: '24h',
    granularity: 'hour',
    timezone: 'UTC',
    from: '2026-07-28T07:00:00Z',
    to: '2026-07-29T07:00:00Z',
    observed_at: '2026-07-29T06:00:00Z',
    summary: aggregate,
    series: [],
    breakdown: [],
    breakdown_truncated: false,
    breakdown_order: 'cost',
    breakdown_group_count: 0,
    collection_health: {
      scope: 'current_process',
      dropped_total: 0,
      write_failure_total: 0,
      last_write_failure_at: null,
    },
    ...overrides,
  }
}

describe('home presenter query states', () => {
  it('distinguishes a confirmed empty inventory from loading, error and cached stale data', () => {
    expect(presentHomeInventory({ status: 'loading' })).toEqual({ kind: 'loading' })
    expect(presentHomeInventory({ status: 'success', data: [] })).toEqual({ kind: 'empty' })
    expect(
      presentHomeInventory({
        status: 'error',
        failedAt: '2026-07-29T06:33:00Z',
      }),
    ).toEqual({ kind: 'error', retryable: true })
    expect(
      presentHomeInventory({
        status: 'error',
        data: [group(7)],
        failedAt: '2026-07-29T06:33:00Z',
      }),
    ).toEqual({ kind: 'stale', groups: [group(7)] })
  })

  it('presents normal, problem, unknown and stale health without conflating network failure', () => {
    const normal = health()
    const problem = health({
      counts: { total: 3, available: 1, cooldown: 1, blacklisted: 1, disabled: 0 },
      cooldown_keys: [problemKey(11, 7, 'cooldown')],
      blacklisted_keys: [problemKey(12, 8, 'blacklisted')],
    })

    expect(presentHomeHealth({ status: 'success', data: normal })).toEqual({
      kind: 'normal',
      health: normal,
    })
    expect(presentHomeHealth({ status: 'success', data: problem })).toMatchObject({
      kind: 'problem',
      health: problem,
      groups: [
        { groupId: 7, cooldownKeys: [{ key_id: 11 }], blacklistedKeys: [] },
        { groupId: 8, cooldownKeys: [], blacklistedKeys: [{ key_id: 12 }] },
      ],
    })
    expect(presentHomeHealth({ status: 'error', failedAt: '2026-07-29T06:34:00Z' })).toEqual({
      kind: 'unknown',
      retryable: true,
    })
    expect(
      presentHomeHealth({
        status: 'error',
        data: normal,
        failedAt: '2026-07-29T06:34:00Z',
      }),
    ).toEqual({
      kind: 'stale',
      health: normal,
      failedAt: '2026-07-29T06:34:00Z',
    })
  })

  it('presents usage data, zero usage, error and cached stale data independently', () => {
    const report = usage()
    const empty = usage({
      summary: {
        ...aggregate,
        request_count: 0,
        success_count: 0,
        failure_count: 0,
      },
    })

    expect(presentHomeUsage({ status: 'success', data: report })).toEqual({
      kind: 'data',
      report,
    })
    expect(presentHomeUsage({ status: 'success', data: empty })).toEqual({
      kind: 'empty',
      report: empty,
    })
    expect(presentHomeUsage({ status: 'error', failedAt: '2026-07-29T06:35:00Z' })).toEqual({
      kind: 'error',
      retryable: true,
    })
    expect(
      presentHomeUsage({
        status: 'error',
        data: report,
        failedAt: '2026-07-29T06:35:00Z',
      }),
    ).toEqual({ kind: 'stale', report })
  })

  it('keeps health and usage failures independent and gates pipeline warnings narrowly', () => {
    const report = usage()
    const unhealthyPipeline = health({
      request_log: {
        ...requestLog,
        dropped_total: 2,
        write_failure_total: 1,
      },
    })
    const healthFailure = presentHome({
      inventory: { status: 'success', data: [group(7)] },
      health: { status: 'error', failedAt: '2026-07-29T06:35:00Z' },
      usage: { status: 'success', data: report },
    })
    expect(healthFailure.health.kind).toBe('unknown')
    expect(healthFailure.usage.kind).toBe('data')

    const usageFailure = presentHome({
      inventory: { status: 'success', data: [group(7)] },
      health: { status: 'success', data: unhealthyPipeline },
      usage: { status: 'error', failedAt: '2026-07-29T06:35:00Z' },
    })
    expect(usageFailure.health.kind).toBe('normal')
    expect(usageFailure.usage.kind).toBe('error')
    expect(usageFailure.pipelineWarning).toEqual({
      droppedTotal: 2,
      writeFailureTotal: 1,
    })

    const retentionOnly = presentHome({
      inventory: { status: 'success', data: [group(7)] },
      health: {
        status: 'success',
        data: health({
          request_log: { ...requestLog, retention_delete_failure_total: 4 },
        }),
      },
      usage: { status: 'success', data: report },
    })
    expect(retentionOnly.pipelineWarning).toBeNull()
  })
})

describe('home presenter problem groups and usage figures', () => {
  it('groups problem keys by ascending Group ID while preserving status partitions and key order', () => {
    const report = health({
      counts: { total: 4, available: 0, cooldown: 2, blacklisted: 2, disabled: 0 },
      cooldown_keys: [problemKey(30, 9, 'cooldown'), problemKey(10, 3, 'cooldown')],
      blacklisted_keys: [problemKey(31, 9, 'blacklisted'), problemKey(11, 3, 'blacklisted')],
    })
    const result = presentHomeHealth({ status: 'success', data: report })

    expect(result.kind).toBe('problem')
    if (result.kind !== 'problem') throw new Error('expected problem health')
    expect(result.groups.map(({ groupId }) => groupId)).toEqual([3, 9])
    expect(result.groups[0]?.cooldownKeys.map(({ key_id }) => key_id)).toEqual([10])
    expect(result.groups[0]?.blacklistedKeys.map(({ key_id }) => key_id)).toEqual([11])
    expect(result.groups[1]?.cooldownKeys.map(({ key_id }) => key_id)).toEqual([30])
    expect(result.groups[1]?.blacklistedKeys.map(({ key_id }) => key_id)).toEqual([31])
  })

  it('computes success rate only for nonzero requests and keeps the backend cost Top 5 order', () => {
    const breakdown = Array.from({ length: 7 }, (_, index) => ({
      ...aggregate,
      group_id: index + 1,
      model: `model-${index + 1}`,
      estimated_cost_usd: 7 - index,
    }))
    const presented = presentHome({
      inventory: { status: 'success', data: [group(1)] },
      health: { status: 'success', data: health() },
      usage: { status: 'success', data: usage({ breakdown }) },
    })

    expect(presented.successRate).toBe(75)
    expect(presented.costRanking.map(({ model }) => model)).toEqual([
      'model-1',
      'model-2',
      'model-3',
      'model-4',
      'model-5',
    ])

    const zero = presentHome({
      inventory: { status: 'success', data: [group(1)] },
      health: { status: 'success', data: health() },
      usage: {
        status: 'success',
        data: usage({
          summary: {
            ...aggregate,
            request_count: 0,
            success_count: 0,
            failure_count: 0,
          },
        }),
      },
    })
    expect(zero.successRate).toBeNull()
  })
})

describe('home presenter deep links', () => {
  it('builds structured problem, failure-log and usage-breakdown locations', () => {
    const report = usage()
    expect(problemKeysLocation(7)).toEqual({
      name: 'group-detail',
      params: { id: 7 },
      query: { tab: 'keys', key_state: 'problem' },
    })
    expect(failureLogsLocation(report)).toEqual({
      name: 'monitor',
      query: {
        tab: 'logs',
        status: 'error',
        from: report.from,
        to: report.to,
      },
    })
    expect(usageBreakdownLocation('30d', 7, 'model / needs encoding')).toEqual({
      name: 'monitor',
      query: {
        tab: 'usage',
        range: '30d',
        group_id: 7,
        model: 'model / needs encoding',
      },
    })
  })

  it('does not own query, transport, browser, formatting or manual URL encoding', () => {
    expect(presenterSource).not.toMatch(/useQuery|ApiClient|window\.|encodeURIComponent|Intl\./)
  })
})
