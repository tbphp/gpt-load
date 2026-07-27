const minimumVisibleEstimatedUSD = 0.00000001

export function formatEstimatedUSD(value: number, locale: string): string {
  if (value === 0) return '$0.00'
  if (value < minimumVisibleEstimatedUSD) return '<$0.00000001'

  return `$${new Intl.NumberFormat(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 8,
  }).format(value)}`
}
