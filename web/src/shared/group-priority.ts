import { InvalidResponseError } from './http/errors'

export function isValidGroupPriority(value: string | number): boolean {
  return (
    /^-?\d+$/u.test(String(value).trim()) &&
    Number.isInteger(Number(value)) &&
    Number(value) >= -2147483648 &&
    Number(value) <= 2147483647
  )
}

export function readGroupPriority(value: unknown): number {
  if (typeof value !== 'number' || !isValidGroupPriority(value)) throw new InvalidResponseError()
  return value
}
