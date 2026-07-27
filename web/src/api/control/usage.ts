import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

export interface UsageFilters {
  range: '24h' | '30d'
  group_id?: number
  model?: string
}

export interface UsageAggregateDto {
  request_count: number
  success_count: number
  failure_count: number
  uncached_input_tokens: number
  cache_read_tokens: number
  cache_write_5m_tokens: number
  cache_write_1h_tokens: number
  output_tokens: number
  total_tokens: number
  estimated_cost_usd: number
  usage_missing_count: number
  partial_count: number
  unpriced_request_count: number
}

export interface UsageReportDto {
  range: '24h' | '30d'
  granularity: 'hour' | 'day'
  timezone: 'UTC'
  from: string
  to: string
  observed_at: string
  summary: UsageAggregateDto
  series: Array<UsageAggregateDto & { bucket_start: string; bucket_end: string }>
  breakdown: Array<UsageAggregateDto & { group_id: number; model: string }>
  breakdown_truncated: boolean
  collection_health: {
    scope: 'current_process'
    dropped_total: number
    write_failure_total: number
    last_write_failure_at: string | null
  }
}

const aggregateKeys = [
  'request_count',
  'success_count',
  'failure_count',
  'uncached_input_tokens',
  'cache_read_tokens',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'output_tokens',
  'total_tokens',
  'usage_missing_count',
  'partial_count',
  'unpriced_request_count',
] as const

const hourMs = 60 * 60 * 1000
const dayMs = 24 * hourMs

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isSafeNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function isRFC3339Timestamp(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$/.exec(
      value,
    )
  if (match === null) return false
  const [year, month, day, hour, minute, second] = match.slice(1, 7).map(Number)
  const [offsetHour, offsetMinute] = match.slice(7, 9).map(Number)
  if (
    month < 1 ||
    month > 12 ||
    hour > 23 ||
    minute > 59 ||
    second > 59 ||
    (match[7] !== undefined && (offsetHour > 23 || offsetMinute > 59))
  ) {
    return false
  }
  const daysInMonth = [
    31,
    year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ]
  return day >= 1 && day <= daysInMonth[month - 1]
}

function timestamp(value: unknown): string {
  if (!isRFC3339Timestamp(value)) throw new InvalidResponseError()
  return value
}

function timestampMs(value: string): number {
  const result = Date.parse(value)
  if (!Number.isFinite(result)) throw new InvalidResponseError()
  return result
}

function isUTCAligned(timestampValue: number, granularity: 'hour' | 'day'): boolean {
  const date = new Date(timestampValue)
  return (
    date.getUTCMinutes() === 0 &&
    date.getUTCSeconds() === 0 &&
    date.getUTCMilliseconds() === 0 &&
    (granularity === 'hour' || date.getUTCHours() === 0)
  )
}

function projectAggregate(value: unknown): UsageAggregateDto {
  if (!isRecord(value)) throw new InvalidResponseError()
  for (const key of aggregateKeys) {
    if (!isSafeNonNegativeInteger(value[key])) throw new InvalidResponseError()
  }
  if (
    typeof value.estimated_cost_usd !== 'number' ||
    !Number.isFinite(value.estimated_cost_usd) ||
    value.estimated_cost_usd < 0
  ) {
    throw new InvalidResponseError()
  }
  return value as unknown as UsageAggregateDto
}

function projectCollectionHealth(value: unknown): UsageReportDto['collection_health'] {
  if (
    !isRecord(value) ||
    value.scope !== 'current_process' ||
    !isSafeNonNegativeInteger(value.dropped_total) ||
    !isSafeNonNegativeInteger(value.write_failure_total) ||
    (value.last_write_failure_at !== null && !isRFC3339Timestamp(value.last_write_failure_at))
  ) {
    throw new InvalidResponseError()
  }
  return {
    scope: 'current_process',
    dropped_total: value.dropped_total,
    write_failure_total: value.write_failure_total,
    last_write_failure_at: value.last_write_failure_at,
  }
}

export function projectUsageReport(value: unknown): UsageReportDto {
  if (
    !isRecord(value) ||
    (value.range !== '24h' && value.range !== '30d') ||
    (value.granularity !== 'hour' && value.granularity !== 'day') ||
    value.timezone !== 'UTC' ||
    (value.range === '24h' && value.granularity !== 'hour') ||
    (value.range === '30d' && value.granularity !== 'day')
  ) {
    throw new InvalidResponseError()
  }
  const observedAt = timestamp(value.observed_at)
  const rangeFrom = timestamp(value.from)
  const rangeTo = timestamp(value.to)
  const range = value.range
  const granularity = value.granularity
  const observedAtMs = timestampMs(observedAt)
  const rangeFromMs = timestampMs(rangeFrom)
  const rangeToMs = timestampMs(rangeTo)
  const bucketDurationMs = granularity === 'hour' ? hourMs : dayMs
  const bucketCount = range === '24h' ? 24 : 30
  if (
    !isUTCAligned(rangeFromMs, granularity) ||
    !isUTCAligned(rangeToMs, granularity) ||
    rangeToMs - rangeFromMs !== bucketDurationMs * bucketCount ||
    observedAtMs < rangeToMs - bucketDurationMs ||
    observedAtMs >= rangeToMs
  ) {
    throw new InvalidResponseError()
  }
  if (
    !Array.isArray(value.series) ||
    !Array.isArray(value.breakdown) ||
    typeof value.breakdown_truncated !== 'boolean'
  ) {
    throw new InvalidResponseError()
  }

  let previousBucketEnd = rangeFromMs
  const series = value.series.map((item) => {
    if (!isRecord(item)) throw new InvalidResponseError()
    const bucketStart = timestamp(item.bucket_start)
    const bucketEnd = timestamp(item.bucket_end)
    const start = timestampMs(bucketStart)
    const end = timestampMs(bucketEnd)
    if (
      !isUTCAligned(start, granularity) ||
      !isUTCAligned(end, granularity) ||
      end - start !== bucketDurationMs ||
      start < rangeFromMs ||
      end > rangeToMs ||
      start < previousBucketEnd
    ) {
      throw new InvalidResponseError()
    }
    previousBucketEnd = end
    return { ...projectAggregate(item), bucket_start: bucketStart, bucket_end: bucketEnd }
  })
  const breakdown = value.breakdown.map((item) => {
    if (
      !isRecord(item) ||
      !isSafeNonNegativeInteger(item.group_id) ||
      item.group_id === 0 ||
      typeof item.model !== 'string' ||
      item.model === ''
    ) {
      throw new InvalidResponseError()
    }
    return { ...projectAggregate(item), group_id: item.group_id, model: item.model }
  })
  return {
    range,
    granularity,
    timezone: 'UTC',
    from: rangeFrom,
    to: rangeTo,
    observed_at: observedAt,
    summary: projectAggregate(value.summary),
    series,
    breakdown,
    breakdown_truncated: value.breakdown_truncated,
    collection_health: projectCollectionHealth(value.collection_health),
  }
}

export async function getUsageReport(
  client: ApiClient,
  filters: UsageFilters,
  signal?: AbortSignal,
): Promise<UsageReportDto> {
  const params = new URLSearchParams([['range', filters.range]])
  if (filters.group_id !== undefined) params.append('group_id', String(filters.group_id))
  if (filters.model !== undefined) params.append('model', filters.model)
  return projectUsageReport(
    await client.request<unknown>(`/api/usage?${params.toString()}`, { method: 'GET', signal }),
  )
}
