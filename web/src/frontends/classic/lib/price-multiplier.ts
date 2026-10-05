export function normalizePriceMultiplier(value: string): string {
  if (!isValidPriceMultiplier(value)) return value
  const [whole = '0', fraction = ''] = value.split('.')
  const normalizedWhole = whole.replace(/^0+/u, '') || '0'
  const normalizedFraction = fraction.replace(/0+$/u, '')
  return normalizedFraction ? `${normalizedWhole}.${normalizedFraction}` : normalizedWhole
}

export function isValidPriceMultiplier(value: string): boolean {
  if (typeof value !== 'string') return false
  if (!/^\d+(?:\.\d{1,6})?$/u.test(value)) return false
  const [whole = '0', fraction = ''] = value.split('.')
  const normalizedWhole = whole.replace(/^0+/u, '') || '0'
  if (normalizedWhole.length > 4) return false
  const millionths = BigInt(normalizedWhole) * 1_000_000n + BigInt(fraction.padEnd(6, '0'))
  return millionths <= 1_000_000_000n
}
