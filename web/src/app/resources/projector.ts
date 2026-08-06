import { InvalidResponseError } from '@/api/errors'

interface NumberBounds {
  minimum?: number
  maximum?: number
}

interface StringOptions {
  allowEmpty?: boolean
}

const maximumInt64 = 9_223_372_036_854_775_807n
const minimumInt64 = -9_223_372_036_854_775_808n
const nanoUSDPerUSD = 1_000_000_000n
const canonicalInteger = /^(?:0|-?[1-9]\d*)$/u
const canonicalNonNegativeInteger = /^(?:0|[1-9]\d*)$/u
const canonicalNonNegativeDecimal = /^(?:0|[1-9]\d*)(?:\.\d{0,8}[1-9])?$/u
const secretLikeField =
  /(?:^|_)(?:authorization|credential|credentials|key|keys|mask|masked|password|plaintext|secret|token|tokens)(?:_|$)/i

function invalidResponse(): never {
  throw new InvalidResponseError()
}

export function projectRecord(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) invalidResponse()
  return value as Record<string, unknown>
}

export function projectArray<T>(value: unknown, projectItem: (item: unknown) => T): T[] {
  if (!Array.isArray(value)) invalidResponse()
  return value.map(projectItem)
}

export function projectString(value: unknown, options: StringOptions = {}): string {
  if (typeof value !== 'string' || (!options.allowEmpty && value.length === 0)) invalidResponse()
  return value
}

export function projectBoolean(value: unknown): boolean {
  if (typeof value !== 'boolean') invalidResponse()
  return value
}

export function projectSafeInteger(value: unknown, bounds: NumberBounds = {}): number {
  if (
    typeof value !== 'number' ||
    !Number.isSafeInteger(value) ||
    (bounds.minimum !== undefined && value < bounds.minimum) ||
    (bounds.maximum !== undefined && value > bounds.maximum)
  ) {
    invalidResponse()
  }
  return value
}

export function projectEpochMilliseconds(value: unknown): number {
  return projectSafeInteger(value, { minimum: 0 })
}

export function projectNullableEpochMilliseconds(value: unknown): number | null {
  return value === null ? null : projectEpochMilliseconds(value)
}

export function projectNonNegativeInt64String(value: unknown): string {
  if (
    typeof value !== 'string' ||
    !canonicalNonNegativeInteger.test(value) ||
    BigInt(value) > maximumInt64
  ) {
    invalidResponse()
  }
  return value
}

export function projectInt64String(value: unknown): string {
  if (typeof value !== 'string' || !canonicalInteger.test(value)) invalidResponse()
  const parsed = BigInt(value)
  if (parsed < minimumInt64 || parsed > maximumInt64) invalidResponse()
  return value
}

export function projectNullableDecimalString(value: unknown): string | null {
  if (value === null) return null
  if (typeof value !== 'string' || !canonicalNonNegativeDecimal.test(value)) invalidResponse()

  const [whole = '', fraction = ''] = value.split('.')
  const nanoUSD = BigInt(whole) * nanoUSDPerUSD + BigInt(fraction.padEnd(9, '0') || '0')
  if (nanoUSD > maximumInt64) invalidResponse()
  return value
}

export function projectFiniteNumber(value: unknown, bounds: NumberBounds = {}): number {
  if (
    typeof value !== 'number' ||
    !Number.isFinite(value) ||
    (bounds.minimum !== undefined && value < bounds.minimum) ||
    (bounds.maximum !== undefined && value > bounds.maximum)
  ) {
    invalidResponse()
  }
  return value
}

export function projectEnum<const T extends readonly string[]>(
  value: unknown,
  allowed: T,
): T[number] {
  if (typeof value !== 'string' || !allowed.includes(value)) invalidResponse()
  return value as T[number]
}

export function projectHTTPURL(value: unknown): string {
  if (typeof value !== 'string' || value.length === 0 || value !== value.trim()) invalidResponse()
  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    return invalidResponse()
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') invalidResponse()
  return value
}

export function assertNoSecretLikeFields(
  record: Record<string, unknown>,
  allowedFields: readonly string[],
): void {
  const allowed = new Set(allowedFields)
  for (const field of Object.keys(record)) {
    if (allowed.has(field)) continue
    if (secretLikeField.test(field)) invalidResponse()
    invalidResponse()
  }
}
