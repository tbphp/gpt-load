export function formatCompactNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(
    value,
  )
}

export function formatNanoUSD(value: string, locale: string): string {
  const amount = BigInt(value)
  const formatter = new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: 3,
  })
  if (amount > 0n && amount < 1_000_000n) return `<${formatter.format(0.001)}`
  // 先用整数舍入到毫美元，再转换到展示用 Number，避免原始纳美元溢出安全整数。
  return formatter.format(Number((amount + 500_000n) / 1_000_000n) / 1000)
}
