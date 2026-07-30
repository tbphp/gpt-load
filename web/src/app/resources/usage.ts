import { queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectFiniteNumber,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type UsageBreakdownOrder = 'requests' | 'cost'

export interface UsageFilters {
  range: '24h' | '30d'
  breakdown_order?: UsageBreakdownOrder
  group_id?: number
  model?: string
}

type NormalizedUsageFilters = UsageFilters & {
  breakdown_order: UsageBreakdownOrder
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
  breakdown_order: UsageBreakdownOrder
  breakdown_group_count: number
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
const reportFields = [
  'range',
  'granularity',
  'timezone',
  'from',
  'to',
  'observed_at',
  'summary',
  'series',
  'breakdown',
  'breakdown_truncated',
  'breakdown_order',
  'breakdown_group_count',
  'collection_health',
] as const
const hourMs = 60 * 60 * 1000
const dayMs = 24 * hourMs

function invalidResponse(): never {
  throw new InvalidResponseError()
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
  if (!isRFC3339Timestamp(value)) invalidResponse()
  return value
}

function timestampMs(value: string): number {
  const result = Date.parse(value)
  if (!Number.isFinite(result)) invalidResponse()
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

export function projectUsageAggregate(value: unknown): UsageAggregateDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [...aggregateKeys, 'estimated_cost_usd'])
  const result: UsageAggregateDto = {
    request_count: projectSafeInteger(record.request_count, { minimum: 0 }),
    success_count: projectSafeInteger(record.success_count, { minimum: 0 }),
    failure_count: projectSafeInteger(record.failure_count, { minimum: 0 }),
    uncached_input_tokens: projectSafeInteger(record.uncached_input_tokens, { minimum: 0 }),
    cache_read_tokens: projectSafeInteger(record.cache_read_tokens, { minimum: 0 }),
    cache_write_5m_tokens: projectSafeInteger(record.cache_write_5m_tokens, { minimum: 0 }),
    cache_write_1h_tokens: projectSafeInteger(record.cache_write_1h_tokens, { minimum: 0 }),
    output_tokens: projectSafeInteger(record.output_tokens, { minimum: 0 }),
    total_tokens: projectSafeInteger(record.total_tokens, { minimum: 0 }),
    estimated_cost_usd: projectFiniteNumber(record.estimated_cost_usd, { minimum: 0 }),
    usage_missing_count: projectSafeInteger(record.usage_missing_count, { minimum: 0 }),
    partial_count: projectSafeInteger(record.partial_count, { minimum: 0 }),
    unpriced_request_count: projectSafeInteger(record.unpriced_request_count, { minimum: 0 }),
  }
  if (
    result.success_count + result.failure_count !== result.request_count ||
    result.total_tokens !==
      result.uncached_input_tokens +
        result.cache_read_tokens +
        result.cache_write_5m_tokens +
        result.cache_write_1h_tokens +
        result.output_tokens ||
    result.usage_missing_count > result.request_count ||
    result.partial_count > result.request_count ||
    result.unpriced_request_count > result.request_count
  ) {
    invalidResponse()
  }
  return result
}

function projectCollectionHealth(value: unknown): UsageReportDto['collection_health'] {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'scope',
    'dropped_total',
    'write_failure_total',
    'last_write_failure_at',
  ])
  return {
    scope: projectEnum(record.scope, ['current_process'] as const),
    dropped_total: projectSafeInteger(record.dropped_total, { minimum: 0 }),
    write_failure_total: projectSafeInteger(record.write_failure_total, { minimum: 0 }),
    last_write_failure_at:
      record.last_write_failure_at === null ? null : timestamp(record.last_write_failure_at),
  }
}

export function projectUsageReport(value: unknown): UsageReportDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, reportFields)
  const range = projectEnum(record.range, ['24h', '30d'] as const)
  const granularity = projectEnum(record.granularity, ['hour', 'day'] as const)
  if (
    projectEnum(record.timezone, ['UTC'] as const) !== 'UTC' ||
    (range === '24h' && granularity !== 'hour') ||
    (range === '30d' && granularity !== 'day')
  ) {
    invalidResponse()
  }
  const observedAt = timestamp(record.observed_at)
  const rangeFrom = timestamp(record.from)
  const rangeTo = timestamp(record.to)
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
    invalidResponse()
  }

  let previousBucketEnd = rangeFromMs
  const series = projectArray(record.series, (value) => {
    const item = projectRecord(value)
    assertNoSecretLikeFields(item, [
      ...aggregateKeys,
      'estimated_cost_usd',
      'bucket_start',
      'bucket_end',
    ])
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
      invalidResponse()
    }
    previousBucketEnd = end
    return {
      ...projectUsageAggregate(item),
      bucket_start: bucketStart,
      bucket_end: bucketEnd,
    }
  })
  const breakdown = projectArray(record.breakdown, (value) => {
    const item = projectRecord(value)
    assertNoSecretLikeFields(item, [...aggregateKeys, 'estimated_cost_usd', 'group_id', 'model'])
    const model = projectString(item.model)
    if (model.trim().length === 0 || model !== model.trim()) invalidResponse()
    return {
      ...projectUsageAggregate(item),
      group_id: projectSafeInteger(item.group_id, { minimum: 1 }),
      model,
    }
  })
  return {
    range,
    granularity,
    timezone: 'UTC',
    from: rangeFrom,
    to: rangeTo,
    observed_at: observedAt,
    summary: projectUsageAggregate(record.summary),
    series,
    breakdown,
    breakdown_truncated: projectBoolean(record.breakdown_truncated),
    breakdown_order: projectEnum(record.breakdown_order, ['requests', 'cost'] as const),
    breakdown_group_count: projectSafeInteger(record.breakdown_group_count, { minimum: 0 }),
    collection_health: projectCollectionHealth(record.collection_health),
  }
}

export function normalizeUsageFilters(filters: UsageFilters): NormalizedUsageFilters {
  const breakdownOrder = filters.breakdown_order ?? 'requests'
  if (breakdownOrder !== 'requests' && breakdownOrder !== 'cost') invalidResponse()
  const result: NormalizedUsageFilters = {
    range: filters.range,
    breakdown_order: breakdownOrder,
  }
  if (filters.group_id !== undefined) result.group_id = filters.group_id
  if (filters.model !== undefined) result.model = filters.model
  return result
}

export function usageQueryIdentity(filters: UsageFilters) {
  return controlQueryKeys.usage.report(normalizeUsageFilters(filters))
}

export async function getUsageReport(
  client: ApiClient,
  filters: UsageFilters,
  signal?: AbortSignal,
): Promise<UsageReportDto> {
  const normalized = normalizeUsageFilters(filters)
  const params = new URLSearchParams([['range', normalized.range]])
  if (filters.breakdown_order !== undefined) {
    params.append('breakdown_order', normalized.breakdown_order)
  }
  if (normalized.group_id !== undefined) params.append('group_id', String(normalized.group_id))
  if (normalized.model !== undefined) params.append('model', normalized.model)
  const report = projectUsageReport(
    await client.request(`/api/usage?${params.toString()}`, { method: 'GET', signal }),
  )
  if (report.breakdown_order !== normalized.breakdown_order) invalidResponse()
  return report
}

export function usageQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<UsageFilters>,
  intervalMs?: number,
) {
  return queryOptions({
    queryKey: computed(() => usageQueryIdentity(toValue(filters))),
    queryFn: ({ signal }) => getUsageReport(client, toValue(filters), signal),
    ...(intervalMs !== undefined
      ? {
          refetchInterval: intervalMs,
          refetchIntervalInBackground: false,
          refetchOnWindowFocus: false,
        }
      : {}),
  })
}
