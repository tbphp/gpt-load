import { numberFormatter } from '@modern/components/ui/intl-formatters'
export function formatCompactNumber(value: number, locale: string): string {
  return numberFormatter(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(value)
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
