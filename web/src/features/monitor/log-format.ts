import type { RequestLogItemDto } from '@/app/resources/request-logs'

export type RequestLogUsageDisplayState = 'reported' | 'missing' | 'not_applicable'
export type RequestLogCostDisplayState = 'complete' | 'partial' | 'unpriced' | 'not_applicable'

export function requestLogUsageDisplayState(log: RequestLogItemDto): RequestLogUsageDisplayState {
  if (log.usage_state === 'missing') return 'missing'
  if (log.usage_state === 'not_applicable') return 'not_applicable'
  return 'reported'
}

export function requestLogCostDisplayState(log: RequestLogItemDto): RequestLogCostDisplayState {
  if (log.cost_state === 'not_applicable') return 'not_applicable'
  if (log.cost_state === 'unpriced') return 'unpriced'
  return log.usage_state === 'partial' || log.pricing_completeness === 'partial'
    ? 'partial'
    : 'complete'
}

export function formatLogDuration(milliseconds: number): string {
  if (!Number.isSafeInteger(milliseconds) || milliseconds < 0) return '—'
  if (milliseconds < 1_000) return `${milliseconds}ms`
  if (milliseconds < 60_000) {
    const seconds = milliseconds / 1_000
    return `${seconds.toFixed(seconds < 10 ? 2 : 1).replace(/\.0+$/u, '')}s`
  }
  const totalSeconds = Math.round(milliseconds / 1_000)
  const hours = Math.floor(totalSeconds / 3_600)
  const minutes = Math.floor((totalSeconds % 3_600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) {
    return `${hours}h${String(minutes).padStart(2, '0')}m${String(seconds).padStart(2, '0')}s`
  }
  return `${minutes}m${String(seconds).padStart(2, '0')}s`
}

export function formatLogTokenCount(value: string, locale: string): string {
  if (!/^(?:0|[1-9]\d*)$/u.test(value)) return '—'
  try {
    return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(BigInt(value))
  } catch {
    return value
  }
}

export function formatLogReasoning(
  log: RequestLogItemDto,
  locale: string,
  dynamicLabel = 'auto',
): string {
  const value = log.reasoning
  if (value === null) return ''
  const details: string[] = []
  if (
    value.mode !== null &&
    (value.mode !== 'enabled' || (value.effort === null && value.budget_tokens === null))
  ) {
    details.push(value.mode)
  }
  if (value.effort !== null) details.push(value.effort)
  if (value.budget_tokens !== null && value.budget_tokens !== '0') {
    if (value.budget_tokens === '-1') {
      if (value.effort === null) details.push(dynamicLabel)
    } else {
      details.push(formatReasoningBudgetCompact(value.budget_tokens, locale))
    }
  }
  return details.join(' / ')
}

export function formatLogReasoningBudget(value: string, locale: string): string {
  return formatSignedInteger(value, locale)
}

export function reasoningBudgetSemantic(value: string): 'disabled' | 'dynamic' | null {
  if (value === '0') return 'disabled'
  if (value === '-1') return 'dynamic'
  return null
}

function formatReasoningBudgetCompact(value: string, locale: string): string {
  if (!/^(?:0|[1-9]\d*)$/u.test(value)) return '—'
  try {
    const amount = BigInt(value)
    if (amount < 1_000n) return formatSignedInteger(value, locale)
    if (amount < 1_000_000n) return `${amount / 1_000n}K`
    if (amount < 1_000_000_000n) return `${amount / 1_000_000n}M`
    return `${amount / 1_000_000_000n}B`
  } catch {
    return value
  }
}

function formatSignedInteger(value: string, locale: string): string {
  if (!/^(?:0|-?[1-9]\d*)$/u.test(value)) return '—'
  try {
    return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(BigInt(value))
  } catch {
    return value
  }
}

export function formatLogOutputRate(log: RequestLogItemDto, locale: string): string {
  if (!log.stream || log.first_response_ms === null || log.duration_ms <= log.first_response_ms) {
    return '—'
  }
  const output = Number(log.output_tokens)
  if (!Number.isSafeInteger(output) || output <= 0) return '—'
  const rate = output / ((log.duration_ms - log.first_response_ms) / 1_000)
  if (!Number.isFinite(rate)) return '—'
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(rate)} t/s`
}

export function hasRequestLogCache(log: RequestLogItemDto): boolean {
  return [
    log.cache_read_tokens,
    log.cache_write_5m_tokens,
    log.cache_write_1h_tokens,
    log.cache_write_unknown_tokens,
  ].some((value) => value !== '0')
}
