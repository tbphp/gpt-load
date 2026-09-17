import { InvalidResponseError } from '@shared/http/errors'

export function record(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value))
    throw new InvalidResponseError()
  return value as Record<string, unknown>
}
export function text(value: unknown): string {
  if (typeof value !== 'string') throw new InvalidResponseError()
  return value
}
export function integer(value: unknown, min = 0): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min)
    throw new InvalidResponseError()
  return value
}
export function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new InvalidResponseError()
  return value
}
export function oneOf<T extends string>(value: unknown, values: readonly T[]): T {
  if (!values.includes(value as T)) throw new InvalidResponseError()
  return value as T
}
export function list(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value
}
