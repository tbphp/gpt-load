import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import {
  dateRangeFor,
  dateRangePresets,
  parseLocalDateTime,
  type DateRangePreset,
} from '@modern/components/ui/date-time'

export interface TimeRangeState {
  preset?: DateRangePreset
  from_ms?: string
  to_ms?: string
}
export function readTimeRange(
  query: LocationQuery,
  fallback: DateRangePreset = '24h',
): TimeRangeState {
  const preset = dateRangePresets.find((value) => value === query.preset)
  if (preset) return { preset }
  const from = query.from_ms
  const to = query.to_ms
  if (
    typeof from === 'string' &&
    typeof to === 'string' &&
    /^\d+$/.test(from) &&
    /^\d+$/.test(to) &&
    Number.isSafeInteger(Number(from)) &&
    Number.isSafeInteger(Number(to)) &&
    Number(from) < Number(to) &&
    Number.isFinite(new Date(Number(from)).getTime()) &&
    Number.isFinite(new Date(Number(to)).getTime())
  ) {
    return { from_ms: from, to_ms: to }
  }
  return { preset: fallback }
}
export function timeRangeQuery(range: TimeRangeState): LocationQueryRaw {
  return range.preset ? { preset: range.preset } : { from_ms: range.from_ms, to_ms: range.to_ms }
}
export function resolveTimeRange(range: TimeRangeState): { from_ms: string; to_ms: string } {
  if (!range.preset && range.from_ms && range.to_ms)
    return { from_ms: range.from_ms, to_ms: range.to_ms }
  const resolved = dateRangeFor(range.preset ?? '24h')
  return {
    from_ms: String(parseLocalDateTime(resolved.from)!.getTime()),
    to_ms: String(parseLocalDateTime(resolved.to)!.getTime()),
  }
}
