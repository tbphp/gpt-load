import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import type { RequestLogHealthDto } from './types'

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
  observed_at: string
  range: { from: string; to: string; granularity: 'hour' | 'day' }
  filters: { group_id: number | null; model: string }
  summary: UsageAggregateDto
  series: Array<UsageAggregateDto & { bucket_start: string; bucket_end: string }>
  breakdown: Array<UsageAggregateDto & { group_id: number; model: string }>
  breakdown_truncated: boolean
  request_log: RequestLogHealthDto
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

const requestLogCountKeys = [
  'enqueued_total',
  'persisted_total',
  'dropped_not_running_total',
  'dropped_queue_full_total',
  'dropped_stopping_total',
  'dropped_persist_failed_total',
  'dropped_shutdown_total',
  'dropped_total',
  'write_failure_total',
  'retention_delete_failure_total',
  'queue_depth',
  'queue_capacity',
] as const

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

function projectRequestLog(value: unknown): RequestLogHealthDto {
  if (!isRecord(value)) throw new InvalidResponseError()
  for (const key of requestLogCountKeys) {
    if (!isSafeNonNegativeInteger(value[key])) throw new InvalidResponseError()
  }
  for (const key of ['last_write_failure_at', 'last_retention_failure_at'] as const) {
    if (value[key] !== null && !isRFC3339Timestamp(value[key])) throw new InvalidResponseError()
  }
  return value as unknown as RequestLogHealthDto
}

export function projectUsageReport(value: unknown): UsageReportDto {
  if (!isRecord(value) || !isRecord(value.range) || !isRecord(value.filters)) {
    throw new InvalidResponseError()
  }
  const observedAt = timestamp(value.observed_at)
  const rangeFrom = timestamp(value.range.from)
  const rangeTo = timestamp(value.range.to)
  if (
    (value.range.granularity !== 'hour' && value.range.granularity !== 'day') ||
    timestampMs(rangeFrom) >= timestampMs(rangeTo) ||
    timestampMs(observedAt) < timestampMs(rangeTo)
  ) {
    throw new InvalidResponseError()
  }
  const filters = value.filters
  if (
    (filters.group_id !== null && !isSafeNonNegativeInteger(filters.group_id)) ||
    (filters.group_id !== null && filters.group_id === 0) ||
    typeof filters.model !== 'string' ||
    !Array.isArray(value.series) ||
    !Array.isArray(value.breakdown) ||
    typeof value.breakdown_truncated !== 'boolean'
  ) {
    throw new InvalidResponseError()
  }

  let previousBucketEnd = timestampMs(rangeFrom)
  const series = value.series.map((item) => {
    if (!isRecord(item)) throw new InvalidResponseError()
    const bucketStart = timestamp(item.bucket_start)
    const bucketEnd = timestamp(item.bucket_end)
    const start = timestampMs(bucketStart)
    const end = timestampMs(bucketEnd)
    if (start < previousBucketEnd || start >= end || end > timestampMs(rangeTo)) {
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
    if (
      (filters.group_id !== null && item.group_id !== filters.group_id) ||
      (filters.model !== '' && item.model !== filters.model)
    ) {
      throw new InvalidResponseError()
    }
    return { ...projectAggregate(item), group_id: item.group_id, model: item.model }
  })
  return {
    observed_at: observedAt,
    range: { from: rangeFrom, to: rangeTo, granularity: value.range.granularity },
    filters: { group_id: filters.group_id, model: filters.model },
    summary: projectAggregate(value.summary),
    series,
    breakdown,
    breakdown_truncated: value.breakdown_truncated,
    request_log: projectRequestLog(value.request_log),
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
