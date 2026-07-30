export function formatCompactMetric(value: number, locale: string): string {
  let divisor = 1
  let suffix = ''

  if (value >= 1_000_000_000) {
    divisor = 1_000_000_000
    suffix = 'B'
  } else if (value >= 100_000) {
    divisor = 1_000_000
    suffix = 'M'
  } else if (value >= 1_000) {
    divisor = 1_000
    suffix = 'K'
  }

  return `${new Intl.NumberFormat(locale, {
    maximumFractionDigits: divisor === 1 ? 0 : 1,
  }).format(value / divisor)}${suffix}`
}
