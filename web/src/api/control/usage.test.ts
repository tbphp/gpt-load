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

const requestLog = {
  enqueued_total: 12,
  persisted_total: 10,
  dropped_not_running_total: 1,
  dropped_queue_full_total: 0,
  dropped_stopping_total: 0,
  dropped_persist_failed_total: 1,
  dropped_shutdown_total: 0,
  dropped_total: 2,
  write_failure_total: 1,
  retention_delete_failure_total: 0,
  queue_depth: 2,
  queue_capacity: 256,
  last_write_failure_at: null,
  last_retention_failure_at: '2026-07-27T10:00:00Z',
} as const

const report = {
  observed_at: '2026-07-27T12:00:00Z',
  range: {
    from: '2026-07-26T12:00:00Z',
    to: '2026-07-27T12:00:00Z',
    granularity: 'hour',
  },
  filters: { group_id: 7, model: 'gpt-5.6' },
  summary: aggregate,
  series: [
    {
      bucket_start: '2026-07-26T12:00:00Z',
      bucket_end: '2026-07-26T13:00:00Z',
      ...aggregate,
    },
  ],
  breakdown: [{ group_id: 7, model: 'gpt-5.6', ...aggregate }],
  breakdown_truncated: false,
  request_log: requestLog,
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
    [
      'bad range timestamp',
      { ...report, range: { ...report.range, from: '2026-02-30T00:00:00Z' } },
    ],
    ['unknown granularity', { ...report, range: { ...report.range, granularity: 'week' } }],
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
      'bucket outside range',
      { ...report, series: [{ ...report.series[0], bucket_start: '2026-07-25T12:00:00Z' }] },
    ],
    [
      'breakdown outside filtered source scope',
      { ...report, breakdown: [{ ...report.breakdown[0], group_id: 8 }] },
    ],
    [
      'malformed request-log counter',
      { ...report, request_log: { ...requestLog, queue_depth: -1 } },
    ],
  ])('rejects %s', (_name, value) => {
    expect(() => projectUsageReport(value)).toThrow(InvalidResponseError)
  })
})
