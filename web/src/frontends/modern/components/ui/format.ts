import { numberFormatter } from '@modern/components/ui/intl-formatters'
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
