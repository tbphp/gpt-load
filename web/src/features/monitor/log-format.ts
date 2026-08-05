import type { RequestLogItemDto } from '@/app/resources/request-logs'

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
