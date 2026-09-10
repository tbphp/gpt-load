type TokenCount = number | string

function parseTokenCount(value: TokenCount): bigint | null {
  if (typeof value === 'number') {
    return Number.isSafeInteger(value) && value >= 0 ? BigInt(value) : null
  }
  if (!/^(?:0|[1-9]\d*)$/u.test(value)) return null
  try {
    return BigInt(value)
  } catch {
    return null
  }
}

export function cacheHitRate(cacheReadTokens: TokenCount, inputTokens: TokenCount): number | null {
  const cacheRead = parseTokenCount(cacheReadTokens)
  const input = parseTokenCount(inputTokens)
  if (cacheRead === null || input === null || input === 0n || cacheRead > input) return null

  const roundedPermille = (cacheRead * 1_000n + input / 2n) / input
  return Number(roundedPermille) / 1_000
}

export function formatCacheHitRate(
  cacheReadTokens: TokenCount,
  inputTokens: TokenCount,
  locale: string,
): string {
  const rate = cacheHitRate(cacheReadTokens, inputTokens)
  if (rate === null) return '—'
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 1,
  }).format(rate)
}
