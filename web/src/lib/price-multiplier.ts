export function normalizePriceMultiplier(value: string): string {
  const trimmed = value.trim()
  if (!isValidPriceMultiplier(trimmed)) return trimmed
  const [whole = '0', fraction = ''] = trimmed.split('.')
  const normalizedWhole = whole.replace(/^0+/u, '') || '0'
  const normalizedFraction = fraction.replace(/0+$/u, '')
  return normalizedFraction ? `${normalizedWhole}.${normalizedFraction}` : normalizedWhole
}

export function isValidPriceMultiplier(value: string): boolean {
  const trimmed = value.trim()
  if (!/^\d+(?:\.\d{1,6})?$/u.test(trimmed)) return false
  const [whole = '0', fraction = ''] = trimmed.split('.')
  const normalizedWhole = whole.replace(/^0+/u, '') || '0'
  if (normalizedWhole.length > 4) return false
  const millionths = BigInt(normalizedWhole) * 1_000_000n + BigInt(fraction.padEnd(6, '0'))
  return millionths <= 1_000_000_000n
}
