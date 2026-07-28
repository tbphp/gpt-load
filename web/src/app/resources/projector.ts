import { InvalidResponseError } from '@/api/errors'

interface NumberBounds {
  minimum?: number
  maximum?: number
}

interface StringOptions {
  allowEmpty?: boolean
}

const isoInstant = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/
const secretLikeField =
  /(?:^|_)(?:authorization|credential|credentials|key|keys|password|secret|token|tokens)(?:_|$)/i

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

export function projectISOInstant(value: unknown): string {
  if (typeof value !== 'string' || !isoInstant.test(value) || !Number.isFinite(Date.parse(value))) {
    invalidResponse()
  }
  return value
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
    if (!allowed.has(field) && secretLikeField.test(field)) invalidResponse()
  }
}
