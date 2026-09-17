import type { UsageAggregate, UsageItem, UsageMetric, UsageReport } from '@modern/api/usage'
import { formatCompactNumber, formatNanoUSD } from '@modern/components/ui/format'
import { numberFormatter } from '@modern/components/ui/intl-formatters'
import type { TrendMetric } from './usage-state'

export function inputTokens(row: UsageAggregate): number {
  return row.total_tokens - row.output_tokens
}
export function cacheRate(row: UsageAggregate): number | null {
  return inputTokens(row) ? (row.cache_read_tokens / inputTokens(row)) * 100 : null
}
export function successRate(row: UsageAggregate): number | null {
  return row.request_count ? (row.success_count / row.request_count) * 100 : null
}
export function percentage(value: number | null, locale: string): string {
  return value === null
    ? '—'
    : numberFormatter(locale, { maximumFractionDigits: 1 }).format(value) + '%'
}
export function distributionValue(row: UsageItem, metric: UsageMetric): number {
  return metric === 'cost'
    ? Number(row.estimated_cost_nano_usd) / 1e9
    : metric === 'tokens'
      ? row.total_tokens
      : row.request_count
}
export function formatUsageCost(value: string, locale: string): string {
  return formatNanoUSD(value, locale, 'narrowSymbol', 2)
}
export function amount(row: UsageItem, metric: UsageMetric, locale: string): string {
  return metric === 'cost'
    ? formatUsageCost(row.estimated_cost_nano_usd, locale)
    : formatCompactNumber(distributionValue(row, metric), locale)
}
export function metricValue(row: UsageAggregate, metric: TrendMetric): number | null {
  if (metric === 'cache') return cacheRate(row)
  if (
    metric === 'cost' &&
    row.estimated_cost_nano_usd === '0' &&
    (row.unpriced_request_count > 0 || row.pricing_partial_count > 0)
  )
    return null
  if (
    metric === 'tokens' &&
    !row.total_tokens &&
    (row.usage_missing_count > 0 || row.partial_count > 0)
  )
    return null
  return distributionValue(row, metric)
}
export function chartPoints(report: UsageReport, metric: TrendMetric) {
  const buckets = new Map(report.series.map((row) => [row.bucket_start_ms, row]))
  const points: { from: number; to: number; value: number | null }[] = []
  for (
    let start = Math.floor(report.from_ms / report.bucket_width_ms) * report.bucket_width_ms;
    start < report.to_ms;
    start += report.bucket_width_ms
  ) {
    const from = Math.max(start, report.from_ms)
    const row = buckets.get(from)
    points.push({
      from,
      to: Math.min(start + report.bucket_width_ms, report.to_ms),
      value: row ? metricValue(row, metric) : metric === 'cache' ? null : 0,
    })
  }
  return points
}
