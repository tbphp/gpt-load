export class JSONNumberPrecisionError extends Error {}

export function isJSONSafeNumber(value: number): boolean {
  return Number.isFinite(value) && (!Number.isInteger(value) || Number.isSafeInteger(value))
}

export function assertJSONNumbersRoundTrip(source: string): void {
  let index = 0
  while (index < source.length) {
    if (source[index] === '"') {
      index += 1
      while (index < source.length) {
        if (source[index] === '\\') index += 2
        else if (source[index] === '"') {
          index += 1
          break
        } else index += 1
      }
      continue
    }
    const character = source[index]
    if (character !== '-' && (character === undefined || character < '0' || character > '9')) {
      index += 1
      continue
    }
    let end = index + 1
    while (end < source.length && '0123456789eE+-.'.includes(source[end] ?? '')) end += 1
    if (!numberRoundTrips(source.slice(index, end))) throw new JSONNumberPrecisionError()
    index = end
  }
}

function numberRoundTrips(literal: string): boolean {
  const value = Number(literal)
  if (!isJSONSafeNumber(value)) return false
  const serialized = JSON.stringify(value)
  return (
    typeof serialized === 'string' && normalizeDecimal(literal) === normalizeDecimal(serialized)
  )
}

function normalizeDecimal(literal: string): string {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/u.exec(literal)
  if (!match) throw new JSONNumberPrecisionError()
  const fraction = match[3] ?? ''
  const digits = `${match[2] ?? ''}${fraction}`.replace(/^0+/u, '') || '0'
  if (digits === '0') return '0'
  const parsedExponent = Number(match[4] ?? '0')
  if (!Number.isSafeInteger(parsedExponent)) throw new JSONNumberPrecisionError()
  const trailingZeroCount = /0+$/u.exec(digits)?.[0].length ?? 0
  const coefficientDigits = trailingZeroCount === 0 ? digits : digits.slice(0, -trailingZeroCount)
  const exponent = parsedExponent - fraction.length + trailingZeroCount
  let coefficient = BigInt(coefficientDigits)
  if (match[1] === '-') coefficient = -coefficient
  return `${coefficient}e${exponent}`
}
