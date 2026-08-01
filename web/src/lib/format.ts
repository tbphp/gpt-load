import { currentTimeZone } from './time'

export { formatMaskedAccessKey } from './access-key-mask'

const NANO_USD_PER_USD = 1_000_000_000n
const NANO_USD_PER_MICRO_USD = 1_000n

export function formatLocalInstant(
  ms: number,
  locale: string,
  options: Intl.DateTimeFormatOptions = {},
): string {
  const date = validDate(ms)
  if (!date) return '—'
  const timeZone = options.timeZone ?? currentTimeZone()
  const hasCustomShape = Object.keys(options).some((key) => key !== 'timeZone')
  if (!hasCustomShape) {
    return formatZonedDateTime(date, timeZone, false)
  }
  try {
    return new Intl.DateTimeFormat(locale, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      timeZone,
      timeZoneName: 'short',
      hourCycle: 'h23',
      ...options,
    }).format(date)
  } catch {
    return date.toISOString()
  }
}

export function formatISOInstant(ms: number): string | undefined {
  if (!Number.isSafeInteger(ms) || ms < 0) return undefined
  return validDate(ms)?.toISOString()
}

export function formatLocalTime(ms: number, locale: string): string {
  void locale
  const date = validDate(ms)
  if (!date) return '—'
  return formatZonedDateTime(date, currentTimeZone(), true).slice(11)
}

export function formatLocalTimeRange(startMs: number, endMs: number, locale: string): string {
  void locale
  const start = validDate(startMs)
  const end = validDate(endMs)
  if (!start || !end || end.getTime() <= start.getTime()) return '—'
  const timeZone = currentTimeZone()
  return `${formatZonedDateTime(start, timeZone, false)} – ${formatZonedDateTime(
    end,
    timeZone,
    false,
  )}`
}

export function formatDuration(startedAtMs: number, nowMs: number, locale: string): string {
  void locale
  if (!Number.isSafeInteger(startedAtMs) || !Number.isSafeInteger(nowMs)) {
    return '—'
  }

  const elapsedMinutes = Math.max(0, Math.floor((nowMs - startedAtMs) / 60_000))
  const days = Math.floor(elapsedMinutes / 1_440)
  const hours = Math.floor((elapsedMinutes % 1_440) / 60)
  const minutes = elapsedMinutes % 60

  if (days > 0) {
    return `${days}d ${String(hours).padStart(2, '0')}h`
  }
  if (hours > 0) {
    return `${hours}h ${String(minutes).padStart(2, '0')}m`
  }
  return `${minutes}m`
}

export function formatInteger(value: number, locale: string): string {
  if (!Number.isSafeInteger(value)) return '—'
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value)
}

export function formatPercent(success: number, total: number, locale: string): string {
  if (!Number.isSafeInteger(success) || !Number.isSafeInteger(total) || total <= 0) {
    return new Intl.NumberFormat(locale, {
      style: 'percent',
      maximumFractionDigits: 1,
    }).format(0)
  }
  const ratio = Math.min(Math.max(success, 0), total) / total
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 1,
  }).format(ratio)
}

export function formatTokens(value: number, locale: string): string {
  if (!Number.isSafeInteger(value) || value < 0) return '—'
  if (value < 1_000) return formatInteger(value, locale)

  const units = [
    { threshold: 1_000_000_000_000, suffix: 'T' },
    { threshold: 1_000_000_000, suffix: 'B' },
    { threshold: 1_000_000, suffix: 'M' },
    { threshold: 1_000, suffix: 'K' },
  ] as const
  const unit = units.find((candidate) => value >= candidate.threshold) ?? units[units.length - 1]
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(
    value / unit.threshold,
  )}${unit.suffix}`
}

export function formatEstimatedCost(nanoUSD: string, locale: string): string {
  if (!/^(?:0|[1-9]\d*)$/u.test(nanoUSD)) return '—'
  const value = BigInt(nanoUSD)
  if (value === 0n) return formatCurrency(0n, '00', locale)
  if (value < NANO_USD_PER_MICRO_USD) return `<${formatCurrency(0n, '000001', locale)}`

  const scale = value >= NANO_USD_PER_USD ? 2 : 6
  const rounded = roundNanoUSD(value, scale)
  const divisor = 10n ** BigInt(scale)
  const whole = rounded / divisor
  const rawFraction = (rounded % divisor).toString().padStart(scale, '0')
  const minimumFractionDigits = 2
  const fraction =
    scale === minimumFractionDigits
      ? rawFraction
      : rawFraction.replace(/0+$/u, '').padEnd(minimumFractionDigits, '0')

  return formatCurrency(whole, fraction, locale)
}

function validDate(ms: number): Date | null {
  if (!Number.isSafeInteger(ms)) return null
  const date = new Date(ms)
  return Number.isNaN(date.getTime()) ? null : date
}

function formatZonedDateTime(date: Date, timeZone: string, includeSeconds: boolean): string {
  try {
    const parts = new Intl.DateTimeFormat('en-CA-u-nu-latn', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: includeSeconds ? '2-digit' : undefined,
      timeZone,
      hourCycle: 'h23',
    }).formatToParts(date)
    const values = new Map(parts.map((part) => [part.type, part.value]))
    const year = values.get('year')
    const month = values.get('month')
    const day = values.get('day')
    const hour = values.get('hour')
    const minute = values.get('minute')
    const second = values.get('second')
    if (!year || !month || !day || !hour || !minute || (includeSeconds && !second)) {
      return fallbackDateTime(date, includeSeconds)
    }
    return `${year}-${month}-${day} ${hour}:${minute}${includeSeconds ? `:${second}` : ''}`
  } catch {
    return fallbackDateTime(date, includeSeconds)
  }
}

function fallbackDateTime(date: Date, includeSeconds: boolean): string {
  return date
    .toISOString()
    .replace('T', ' ')
    .slice(0, includeSeconds ? 19 : 16)
}

function roundNanoUSD(value: bigint, scale: number): bigint {
  const precision = 10n ** BigInt(9 - scale)
  return (value + precision / 2n) / precision
}

function formatCurrency(whole: bigint, fraction: string, locale: string): string {
  const fractionDigits = fraction.length
  const formatter = new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: 'USD',
    currencyDisplay: 'narrowSymbol',
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  })
  const groupedWhole = new Intl.NumberFormat(locale, {
    useGrouping: true,
    maximumFractionDigits: 0,
  }).format(whole)
  let integerWritten = false

  return formatter
    .formatToParts(0)
    .map((part) => {
      if (part.type === 'integer') {
        if (integerWritten) return ''
        integerWritten = true
        return groupedWhole
      }
      if (part.type === 'group') return ''
      if (part.type === 'fraction') return fraction
      return part.value
    })
    .join('')
}
