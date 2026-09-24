import { numberFormatter, relativeTimeFormatter } from '@modern/components/ui/intl-formatters'
export function formatCompactNumber(value: number, locale: string): string {
  void locale
  return numberFormatter('en-US', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

export function formatRemainingDuration(milliseconds: number, locale: string): string {
  void locale
  const minutes = Math.max(0, Math.ceil(milliseconds / 60000))
  const hours = Math.floor(minutes / 60)
  const duration =
    hours >= 24
      ? ([
          [Math.floor(hours / 24), 'day'],
          [hours % 24, 'hour'],
        ] as const)
      : hours > 0
        ? ([
            [hours, 'hour'],
            [minutes % 60, 'minute'],
          ] as const)
        : ([[minutes, 'minute']] as const)
  return duration
    .filter(([value]) => value > 0 || minutes === 0)
    .map(([value, unit]) => `${value}${{ day: 'd', hour: 'h', minute: 'm' }[unit]}`)
    .join(' ')
}

export function formatRelativeInstant(value: number, now: number, locale: string): string {
  if (!Number.isSafeInteger(value) || !Number.isSafeInteger(now)) return '—'
  const seconds = Math.max(0, Math.floor((now - value) / 1000))
  const [amount, unit]: [number, Intl.RelativeTimeFormatUnit] =
    seconds < 60
      ? [seconds, 'second']
      : seconds < 3600
        ? [Math.floor(seconds / 60), 'minute']
        : seconds < 86400
          ? [Math.floor(seconds / 3600), 'hour']
          : seconds < 2592000
            ? [Math.floor(seconds / 86400), 'day']
            : seconds < 31536000
              ? [Math.floor(seconds / 2592000), 'month']
              : [Math.floor(seconds / 31536000), 'year']
  return relativeTimeFormatter(locale).format(-amount, unit)
}

export function formatNanoUSD(
  value: string,
  locale: string,
  currencyDisplay: 'symbol' | 'narrowSymbol' = 'symbol',
  fractionDigits: 2 | 3 = 3,
): string {
  const amount = BigInt(value)
  const scale = 10n ** BigInt(9 - fractionDigits)
  const unit = 10 ** fractionDigits
  const formatter = numberFormatter(locale, {
    style: 'currency',
    currency: 'USD',
    currencyDisplay,
    minimumFractionDigits: 2,
    maximumFractionDigits: fractionDigits,
  })
  if (amount > 0n && amount < scale) return `<${formatter.format(1 / unit)}`
  // 先按展示精度完成整数舍入，再转换到展示用 Number，避免原始纳美元直接丢失精度。
  return formatter.format(Number((amount + scale / 2n) / scale) / unit)
}
