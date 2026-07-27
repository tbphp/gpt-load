import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { getUsageReport, projectUsageReport } from './usage'

const aggregate = {
  request_count: 8,
  success_count: 6,
  failure_count: 2,
  uncached_input_tokens: 100,
  cache_read_tokens: 20,
  cache_write_5m_tokens: 3,
  cache_write_1h_tokens: 4,
  output_tokens: 50,
  total_tokens: 177,
  estimated_cost_usd: 0,
  usage_missing_count: 1,
  partial_count: 2,
  unpriced_request_count: 3,
} as const

const collectionHealth = {
  scope: 'current_process',
  dropped_total: 2,
  write_failure_total: 1,
  last_write_failure_at: null,
} as const

const report = {
  range: '24h',
  granularity: 'hour',
  timezone: 'UTC',
  from: '2026-07-26T13:00:00Z',
  to: '2026-07-27T13:00:00Z',
  observed_at: '2026-07-27T12:21:48Z',
  summary: aggregate,
  series: [
    {
      bucket_start: '2026-07-26T13:00:00Z',
      bucket_end: '2026-07-26T14:00:00Z',
      ...aggregate,
    },
  ],
  breakdown: [{ group_id: 7, model: 'gpt-5.6', ...aggregate }],
  breakdown_truncated: false,
  collection_health: collectionHealth,
} as const

describe('Usage control API', () => {
  it('projects every DTO field and serializes canonical range/group/model filters', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue(report)

    await expect(
      getUsageReport(
        { request: request as ApiClient['request'] },
        { range: '30d', group_id: 7, model: 'gpt-5.6' },
        signal,
      ),
    ).resolves.toEqual(report)
    expect(request).toHaveBeenCalledWith('/api/usage?range=30d&group_id=7&model=gpt-5.6', {
      method: 'GET',
      signal,
    })
  })

  it('omits absent optional filters while retaining the required range', async () => {
    const request = vi.fn().mockResolvedValue(report)

    await getUsageReport({ request: request as ApiClient['request'] }, { range: '24h' })

    expect(request).toHaveBeenCalledWith('/api/usage?range=24h', {
      method: 'GET',
      signal: undefined,
    })
  })

  it('accepts the current partial bucket because the response window ends at its aligned boundary', () => {
    const currentBucketReport = {
      ...report,
      series: [
        {
          ...report.series[0],
          bucket_start: '2026-07-27T12:00:00Z',
          bucket_end: '2026-07-27T13:00:00Z',
        },
      ],
    }

    expect(projectUsageReport(currentBucketReport)).toEqual(currentBucketReport)
  })

  it('accepts an exact UTC day bucket inside the aligned 30-day window', () => {
    const dailyReport = {
      ...report,
      range: '30d' as const,
      granularity: 'day' as const,
      from: '2026-06-28T00:00:00Z',
      to: '2026-07-28T00:00:00Z',
      series: [
        {
          ...report.series[0],
          bucket_start: '2026-07-27T00:00:00Z',
          bucket_end: '2026-07-28T00:00:00Z',
        },
      ],
    }

    expect(projectUsageReport(dailyReport)).toEqual(dailyReport)
  })

  it.each([
    ['non-object report', null],
    ['missing aggregate field', { ...report, summary: { ...aggregate, total_tokens: undefined } }],
    ['negative count', { ...report, summary: { ...aggregate, request_count: -1 } }],
    [
      'unsafe token',
      { ...report, summary: { ...aggregate, output_tokens: Number.MAX_SAFE_INTEGER + 1 } },
    ],
    ['negative cost', { ...report, summary: { ...aggregate, estimated_cost_usd: -0.01 } }],
    [
      'non-finite cost',
      { ...report, summary: { ...aggregate, estimated_cost_usd: Number.POSITIVE_INFINITY } },
    ],
    ['bad observed timestamp', { ...report, observed_at: 'tomorrow' }],
    ['bad range timestamp', { ...report, from: '2026-02-30T00:00:00Z' }],
    ['unknown range', { ...report, range: '7d' }],
    ['unknown granularity', { ...report, granularity: 'week' }],
    ['wrong timezone', { ...report, timezone: 'Asia/Shanghai' }],
    ['range/granularity mismatch', { ...report, range: '30d' }],
    [
      'out-of-order buckets',
      {
        ...report,
        series: [
          {
            ...report.series[0],
            bucket_start: '2026-07-26T14:00:00Z',
            bucket_end: '2026-07-26T15:00:00Z',
          },
          report.series[0],
        ],
      },
    ],
    [
      'rolling response window without UTC-aligned boundaries',
      {
        ...report,
        from: '2026-07-26T12:21:48Z',
        to: '2026-07-27T12:21:48Z',
        series: [],
      },
    ],
    [
      'aligned response window with the wrong bucket count',
      {
        ...report,
        from: '2026-07-26T14:00:00Z',
        series: [],
      },
    ],
    [
      'misaligned hour bucket',
      {
        ...report,
        series: [
          {
            ...report.series[0],
            bucket_start: '2026-07-27T11:30:00Z',
            bucket_end: '2026-07-27T12:30:00Z',
          },
        ],
      },
    ],
    [
      'wrong hour bucket length',
      {
        ...report,
        series: [
          {
            ...report.series[0],
            bucket_start: '2026-07-27T12:00:00Z',
            bucket_end: '2026-07-27T12:30:00Z',
          },
        ],
      },
    ],
    [
      'far-future bucket end',
      {
        ...report,
        series: [
          {
            ...report.series[0],
            bucket_start: '2026-07-27T13:00:00Z',
            bucket_end: '2026-07-27T14:00:00Z',
          },
        ],
      },
    ],
    [
      'aligned non-current bucket outside the response window',
      {
        ...report,
        series: [
          {
            ...report.series[0],
            bucket_start: '2026-07-26T12:00:00Z',
            bucket_end: '2026-07-26T13:00:00Z',
          },
        ],
      },
    ],
    [
      'wrong UTC day bucket length',
      {
        ...report,
        observed_at: '2026-07-27T12:21:48Z',
        range: '30d',
        granularity: 'day',
        from: '2026-06-28T00:00:00Z',
        to: '2026-07-28T00:00:00Z',
        series: [
          {
            ...report.series[0],
            bucket_start: '2026-07-27T00:00:00Z',
            bucket_end: '2026-07-27T23:00:00Z',
          },
        ],
      },
    ],
    [
      'unsafe breakdown Group',
      {
        ...report,
        breakdown: [{ ...report.breakdown[0], group_id: Number.MAX_SAFE_INTEGER + 1 }],
      },
    ],
    [
      'malformed collection-health scope',
      { ...report, collection_health: { ...collectionHealth, scope: 'all_time' } },
    ],
    [
      'malformed collection-health counter',
      { ...report, collection_health: { ...collectionHealth, dropped_total: -1 } },
    ],
    [
      'malformed collection-health timestamp',
      {
        ...report,
        collection_health: { ...collectionHealth, last_write_failure_at: 'tomorrow' },
      },
    ],
    [
      'legacy private response shape',
      {
        observed_at: report.observed_at,
        range: { from: report.from, to: report.to, granularity: report.granularity },
        filters: { group_id: 7, model: 'gpt-5.6' },
        summary: aggregate,
        series: report.series,
        breakdown: report.breakdown,
        breakdown_truncated: false,
        request_log: {},
      },
    ],
  ])('rejects %s', (_name, value) => {
    expect(() => projectUsageReport(value)).toThrow(InvalidResponseError)
  })
})
