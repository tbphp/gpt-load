import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { integer, list, oneOf, record, text } from './response'

export const usageDimensions = ['model', 'group', 'access_key'] as const
export const usageMetrics = ['requests', 'tokens', 'cost'] as const
export type UsageDimension = (typeof usageDimensions)[number]
export type UsageMetric = (typeof usageMetrics)[number]
export const usageFilterKeys = [
  'upstream_model',
  'group_id',
  'channel_id',
  'access_key_id',
  'credential_id',
] as const
export type UsageFilterKey = (typeof usageFilterKeys)[number]
export type UsageFilters = Partial<Record<UsageFilterKey, string>>
const counts = [
  'request_count',
  'success_count',
  'failure_count',
  'uncached_input_tokens',
  'cache_read_tokens',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'cache_write_unknown_tokens',
  'output_tokens',
  'total_tokens',
  'usage_missing_count',
  'partial_count',
  'unpriced_request_count',
  'pricing_partial_count',
] as const
export type UsageAggregate = Record<(typeof counts)[number], number> & {
  estimated_cost_nano_usd: string
}
export interface UsageItem {
  group_id?: number
  access_key_id?: number
  model?: string
  request_count: number
  total_tokens: number
  estimated_cost_nano_usd: string
}
export interface UsageDistribution {
  items: UsageItem[]
  other: UsageItem | null
}
export interface UsageReport {
  from_ms: number
  to_ms: number
  observed_at_ms: number
  bucket_width_ms: number
  summary: UsageAggregate
  series: (UsageAggregate & { bucket_start_ms: number; bucket_end_ms: number })[]
  distributions: Partial<Record<UsageDimension, Record<UsageMetric, UsageDistribution>>>
  collectionIncomplete: boolean
}
function money(value: unknown): string {
  const amount = text(value)
  if (!/^(0|[1-9]\d*)$/.test(amount)) throw new InvalidResponseError()
  return amount
}
function aggregate(value: unknown): UsageAggregate {
  const row = record(value)
  const result = Object.fromEntries(counts.map((key) => [key, integer(row[key])])) as Record<
    (typeof counts)[number],
    number
  >
  if (
    result.success_count + result.failure_count !== result.request_count ||
    result.total_tokens !==
      result.uncached_input_tokens +
        result.cache_read_tokens +
        result.cache_write_5m_tokens +
        result.cache_write_1h_tokens +
        result.cache_write_unknown_tokens +
        result.output_tokens
  )
    throw new InvalidResponseError()
  return { ...result, estimated_cost_nano_usd: money(row.estimated_cost_nano_usd) }
}
function item(value: unknown, dimension?: UsageDimension): UsageItem {
  const row = record(value)
  return {
    request_count: integer(row.request_count),
    total_tokens: integer(row.total_tokens),
    estimated_cost_nano_usd: money(row.estimated_cost_nano_usd),
    ...(dimension === 'model' ? { model: text(row.model) } : {}),
    ...(dimension === 'group' ? { group_id: integer(row.group_id, 1) } : {}),
    ...(dimension === 'access_key' ? { access_key_id: integer(row.access_key_id, 1) } : {}),
  }
}
export async function getUsage(
  client: ApiClient,
  filters: UsageFilters & { from_ms: string; to_ms: string },
  signal: AbortSignal,
): Promise<UsageReport> {
  const params = new URLSearchParams({ from_ms: filters.from_ms, to_ms: filters.to_ms })
  for (const key of usageFilterKeys) if (filters[key]) params.set(key, filters[key]!)
  const row = record(await client.request(`/api/usage?${params}`, { signal }))
  const from = integer(row.from_ms),
    to = integer(row.to_ms)
  const width = integer(row.bucket_width_ms, 300000)
  if (from !== Number(filters.from_ms) || to !== Number(filters.to_ms) || to <= from)
    throw new InvalidResponseError()
  let previousEnd = from
  const series = list(row.series).map((value) => {
    const point = record(value)
    const start = integer(point.bucket_start_ms),
      end = integer(point.bucket_end_ms)
    if (
      start < previousEnd ||
      end <= start ||
      end > to ||
      start !== Math.max(from, Math.floor(start / width) * width)
    )
      throw new InvalidResponseError()
    previousEnd = end
    return { ...aggregate(point), bucket_start_ms: start, bucket_end_ms: end }
  })
  const source = record(row.distributions)
  const distributions: UsageReport['distributions'] = {}
  for (const dimension of usageDimensions) {
    if (source[dimension] === undefined && dimension !== 'model') continue
    const metrics = record(source[dimension])
    distributions[dimension] = Object.fromEntries(
      usageMetrics.map((metric) => {
        const distribution = record(metrics[metric])
        if (
          oneOf(distribution.dimension, usageDimensions) !== dimension ||
          oneOf(distribution.metric, usageMetrics) !== metric
        )
          throw new InvalidResponseError()
        return [
          metric,
          {
            items: list(distribution.items).map((value) => item(value, dimension)),
            other: distribution.other === null ? null : item(distribution.other),
          },
        ]
      }),
    ) as Record<UsageMetric, UsageDistribution>
  }
  const health = record(row.collection_health)
  return {
    from_ms: from,
    to_ms: to,
    observed_at_ms: integer(row.observed_at_ms),
    bucket_width_ms: width,
    summary: aggregate(row.summary),
    series,
    distributions,
    collectionIncomplete:
      integer(health.dropped_total) > 0 || integer(health.write_failure_total) > 0,
  }
}
