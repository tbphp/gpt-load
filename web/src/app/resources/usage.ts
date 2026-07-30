import { queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEpochMilliseconds,
  projectEnum,
  projectNonNegativeInt64String,
  projectNullableEpochMilliseconds,
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
  estimated_cost_nano_usd: string
  usage_missing_count: number
  partial_count: number
  unpriced_request_count: number
}

export interface UsageReportDto {
  range: '24h' | '30d'
  granularity: 'hour' | 'day'
  from_ms: number
  to_ms: number
  observed_at_ms: number
  summary: UsageAggregateDto
  series: Array<UsageAggregateDto & { bucket_start_ms: number; bucket_end_ms: number }>
  breakdown: Array<UsageAggregateDto & { group_id: number; model: string }>
  breakdown_truncated: boolean
  breakdown_order: UsageBreakdownOrder
  breakdown_group_count: number
  collection_health: {
    scope: 'current_process'
    dropped_total: number
    write_failure_total: number
    last_write_failure_at_ms: number | null
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
const aggregateFields = [...aggregateKeys, 'estimated_cost_nano_usd'] as const
const reportFields = [
  'range',
  'granularity',
  'from_ms',
  'to_ms',
  'observed_at_ms',
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

function isUTCAligned(timestampValue: number, granularity: 'hour' | 'day'): boolean {
  return timestampValue % (granularity === 'hour' ? hourMs : dayMs) === 0
}

export function projectUsageAggregate(value: unknown): UsageAggregateDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, aggregateFields)
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
    estimated_cost_nano_usd: projectNonNegativeInt64String(record.estimated_cost_nano_usd),
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
    'last_write_failure_at_ms',
  ])
  return {
    scope: projectEnum(record.scope, ['current_process'] as const),
    dropped_total: projectSafeInteger(record.dropped_total, { minimum: 0 }),
    write_failure_total: projectSafeInteger(record.write_failure_total, { minimum: 0 }),
    last_write_failure_at_ms: projectNullableEpochMilliseconds(record.last_write_failure_at_ms),
  }
}

export function projectUsageReport(value: unknown): UsageReportDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, reportFields)
  const range = projectEnum(record.range, ['24h', '30d'] as const)
  const granularity = projectEnum(record.granularity, ['hour', 'day'] as const)
  if ((range === '24h' && granularity !== 'hour') || (range === '30d' && granularity !== 'day')) {
    invalidResponse()
  }
  const observedAtMS = projectEpochMilliseconds(record.observed_at_ms)
  const rangeFromMS = projectEpochMilliseconds(record.from_ms)
  const rangeToMS = projectEpochMilliseconds(record.to_ms)
  const bucketDurationMs = granularity === 'hour' ? hourMs : dayMs
  const bucketCount = range === '24h' ? 24 : 30
  if (
    !isUTCAligned(rangeFromMS, granularity) ||
    !isUTCAligned(rangeToMS, granularity) ||
    rangeToMS - rangeFromMS !== bucketDurationMs * bucketCount ||
    observedAtMS < rangeToMS - bucketDurationMs ||
    observedAtMS >= rangeToMS
  ) {
    invalidResponse()
  }

  let previousBucketEndMS = rangeFromMS
  const series = projectArray(record.series, (value) => {
    const item = projectRecord(value)
    assertNoSecretLikeFields(item, [
      ...aggregateKeys,
      'estimated_cost_nano_usd',
      'bucket_start_ms',
      'bucket_end_ms',
    ])
    const bucketStartMS = projectEpochMilliseconds(item.bucket_start_ms)
    const bucketEndMS = projectEpochMilliseconds(item.bucket_end_ms)
    if (
      !isUTCAligned(bucketStartMS, granularity) ||
      !isUTCAligned(bucketEndMS, granularity) ||
      bucketEndMS - bucketStartMS !== bucketDurationMs ||
      bucketStartMS < rangeFromMS ||
      bucketEndMS > rangeToMS ||
      bucketStartMS < previousBucketEndMS
    ) {
      invalidResponse()
    }
    previousBucketEndMS = bucketEndMS
    return {
      ...projectUsageAggregate(
        Object.fromEntries(aggregateFields.map((field) => [field, item[field]])),
      ),
      bucket_start_ms: bucketStartMS,
      bucket_end_ms: bucketEndMS,
    }
  })
  const breakdown = projectArray(record.breakdown, (value) => {
    const item = projectRecord(value)
    assertNoSecretLikeFields(item, [
      ...aggregateKeys,
      'estimated_cost_nano_usd',
      'group_id',
      'model',
    ])
    const model = projectString(item.model, { allowEmpty: true })
    if (model !== model.trim()) invalidResponse()
    return {
      ...projectUsageAggregate(
        Object.fromEntries(aggregateFields.map((field) => [field, item[field]])),
      ),
      group_id: projectSafeInteger(item.group_id, { minimum: 0 }),
      model,
    }
  })
  return {
    range,
    granularity,
    from_ms: rangeFromMS,
    to_ms: rangeToMS,
    observed_at_ms: observedAtMS,
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
