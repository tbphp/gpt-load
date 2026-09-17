import type { LocationQuery } from 'vue-router'
import { usageFilterKeys, usageMetrics, type UsageFilters } from '@modern/api/usage'
import { readTimeRange, timeRangeQuery, type TimeRangeState } from '@modern/app/time-range'
export const trendMetrics = ['requests', 'tokens', 'cache', 'cost'] as const
export type TrendMetric = (typeof trendMetrics)[number]
export const usageStateKeys = [
  ...usageFilterKeys,
  'model',
  'preset',
  'from_ms',
  'to_ms',
  'trend',
  'metric',
] as const
export function validUsageFilter(key: string, value: string): boolean {
  if (key.endsWith('_id') && key !== 'channel_id')
    return /^[1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value))
  if (key === 'channel_id') return /^[a-z][a-z0-9_]{0,99}$/.test(value)
  return new TextEncoder().encode(value).length <= 255 && !/[\u0000-\u001f\u007f]/u.test(value)
}
export function parseUsageState(query: LocationQuery, admin: boolean) {
  const filters: UsageFilters = {}
  for (const key of usageFilterKeys) {
    const value = query[key] ?? (key === 'upstream_model' ? query.model : undefined)
    if (
      typeof value === 'string' &&
      value.trim() &&
      (admin || key === 'upstream_model') &&
      validUsageFilter(key, value.trim())
    )
      filters[key] = value.trim()
  }
  return {
    filters,
    range: readTimeRange(query) as TimeRangeState,
    trend: trendMetrics.find((value) => value === query.trend) ?? 'requests',
    metric: usageMetrics.find((value) => value === query.metric) ?? 'requests',
  }
}
export type UsageState = ReturnType<typeof parseUsageState>
export function serializeUsageState(state: UsageState) {
  return {
    ...state.filters,
    ...timeRangeQuery(state.range),
    trend: state.trend,
    metric: state.metric,
  }
}
