import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import { getUsageReport, projectUsageReport, usageQueryIdentity } from './usage'

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
  estimated_cost_usd: 0.125,
  usage_missing_count: 1,
  partial_count: 2,
  unpriced_request_count: 3,
}

const report = {
  range: '24h',
  granularity: 'hour',
  timezone: 'UTC',
  from: '2026-07-28T02:00:00Z',
  to: '2026-07-29T02:00:00Z',
  observed_at: '2026-07-29T01:02:03Z',
  summary: aggregate,
  series: [
    {
      ...aggregate,
      bucket_start: '2026-07-29T01:00:00Z',
      bucket_end: '2026-07-29T02:00:00Z',
    },
  ],
  breakdown: [{ ...aggregate, group_id: 7, model: 'gpt-upstream' }],
  breakdown_truncated: false,
  breakdown_order: 'cost',
  breakdown_group_count: 14,
  collection_health: {
    scope: 'current_process',
    dropped_total: 0,
    write_failure_total: 0,
    last_write_failure_at: null,
  },
}

describe('Usage resource', () => {
  it('projects the complete aggregation report', () => {
    expect(projectUsageReport(report)).toEqual(report)
  })

  it.each([
    { ...report, summary: { ...aggregate, total_tokens: 176 } },
    { ...report, summary: { ...aggregate, success_count: 7 } },
    { ...report, summary: { ...aggregate, estimated_cost_usd: Number.NaN } },
    { ...report, breakdown: [{ ...report.breakdown[0], group_id: 0 }] },
    { ...report, breakdown_order: 'unknown' },
    { ...report, breakdown_group_count: -1 },
    { ...report, breakdown_group_count: Number.MAX_SAFE_INTEGER + 1 },
    { ...report, breakdown_order: undefined },
    { ...report, breakdown_group_count: undefined },
    { ...report, billing_token: 'plaintext' },
    {
      ...report,
      collection_health: { ...report.collection_health, secret_key: 'plaintext' },
    },
  ])('rejects an unsafe usage field %#j', (unsafe) => {
    expect(() => projectUsageReport(unsafe)).toThrow(InvalidResponseError)
  })

  it('normalizes query identity and serializes only supported filters', async () => {
    const filters = {
      range: '24h' as const,
      breakdown_order: 'cost' as const,
      model: 'gpt-upstream',
      selected_request_id: 'ignored',
    }
    expect(usageQueryIdentity(filters)).toEqual(
      controlQueryKeys.usage.report({
        range: '24h',
        breakdown_order: 'cost',
        model: 'gpt-upstream',
      }),
    )
    expect(usageQueryIdentity({ range: '24h' })).toEqual(
      controlQueryKeys.usage.report({ range: '24h', breakdown_order: 'requests' }),
    )
    expect(usageQueryIdentity({ range: '24h', breakdown_order: 'cost' })).not.toEqual(
      usageQueryIdentity({ range: '24h', breakdown_order: 'requests' }),
    )

    const request = vi.fn().mockResolvedValue(report) as ApiClient['request']
    await getUsageReport({ request }, filters)
    expect(request).toHaveBeenCalledWith(
      '/api/usage?range=24h&breakdown_order=cost&model=gpt-upstream',
      {
        method: 'GET',
        signal: undefined,
      },
    )
  })

  it('rejects a response whose order does not match the normalized query', async () => {
    const request = vi.fn().mockResolvedValue(report) as ApiClient['request']
    await expect(getUsageReport({ request }, { range: '24h' })).rejects.toThrow(
      InvalidResponseError,
    )
  })
})
