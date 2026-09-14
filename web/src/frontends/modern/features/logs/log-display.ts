import { numberFormatter, dateFormatter } from '@modern/components/ui/intl-formatters'
import type { LogEntry } from '@modern/api/logs'
import type { SemanticTone } from '@modern/components/ui'

export const logStatusTone: Record<LogEntry['status'], SemanticTone> = {
  success: 'success',
  error: 'danger',
  incomplete: 'warning',
  canceled: 'neutral',
}
export function logModelMismatch(row: LogEntry): boolean {
  return (
    row.model_consistency === 'mismatch' &&
    Boolean(row.upstream_model) &&
    Boolean(row.upstream_reported_model)
  )
}
export function logCanMergeError(row: LogEntry): boolean {
  // 中断或失败仍可能产生实际消耗；真实的零用量/零费用也不能当成缺失。
  return (
    row.status !== 'success' &&
    Boolean(row.error_code.trim() || row.error_summary.trim()) &&
    (row.usage_state === 'missing' || row.usage_state === 'not_applicable') &&
    row.cost_state !== 'priced' &&
    (row.pricing_completeness === 'unavailable' || row.pricing_completeness === 'not_applicable') &&
    [
      row.input_tokens,
      row.output_tokens,
      row.cache_read_tokens,
      row.cache_write_5m_tokens,
      row.cache_write_1h_tokens,
      row.cache_write_unknown_tokens,
      row.estimated_cost_nano_usd,
    ].every((value) => BigInt(value) === 0n)
  )
}
export function logNumber(value: string | number, locale: string, compact = false): string {
  return numberFormatter(
    locale,
    compact ? { notation: 'compact', maximumFractionDigits: 1 } : {},
  ).format(typeof value === 'string' ? BigInt(value) : value)
}
export function logMoney(value: string, locale: string): string {
  const amount = BigInt(value)
  const formatter = numberFormatter(locale, {
    style: 'currency',
    currency: 'USD',
    currencyDisplay: 'narrowSymbol',
    maximumFractionDigits: 6,
  })
  if (amount > 0n && amount < 1000n) return '<' + formatter.format(0.000001)
  return formatter.format(Number((amount + 500n) / 1000n) / 1000000)
}
export function exactLogMoney(value: string): string {
  const padded = value.padStart(10, '0')
  const fraction = padded.slice(-9).replace(/0+$/, '')
  return '$' + padded.slice(0, -9) + (fraction ? '.' + fraction : '')
}
export function logDuration(ms: number | null, locale: string): string {
  if (ms === null) return '—'
  return (
    numberFormatter(locale, {
      minimumFractionDigits: ms < 1000 ? 0 : 1,
      maximumFractionDigits: ms < 1000 ? 0 : 1,
    }).format(ms < 1000 ? ms : ms / 1000) + (ms < 1000 ? ' ms' : ' s')
  )
}
export function logTime(ms: number, locale: string, full = false): string {
  return dateFormatter(locale, {
    ...(full ? { year: 'numeric' as const } : {}),
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(ms)
}
export function logCacheWrites(row: LogEntry): string {
  return String(
    BigInt(row.cache_write_5m_tokens) +
      BigInt(row.cache_write_1h_tokens) +
      BigInt(row.cache_write_unknown_tokens),
  )
}
export function logCacheRate(row: LogEntry): number | null {
  const input = BigInt(row.input_tokens)
  return input === 0n
    ? null
    : Math.min(100, Number((BigInt(row.cache_read_tokens) * 1000n) / input) / 10)
}
export function logHasUsage(row: LogEntry): boolean {
  return row.usage_state === 'complete' || row.usage_state === 'partial'
}
