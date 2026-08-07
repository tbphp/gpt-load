export const timeRanges = ['1h', '24h', '3d', '7d', '15d', '30d'] as const
export type TimeRange = (typeof timeRanges)[number]

export const defaultTimeRange: TimeRange = '24h'

const hourMilliseconds = 60 * 60 * 1000

export const timeRangeMilliseconds: Record<TimeRange, number> = {
  '1h': hourMilliseconds,
  '24h': 24 * hourMilliseconds,
  '3d': 3 * 24 * hourMilliseconds,
  '7d': 7 * 24 * hourMilliseconds,
  '15d': 15 * 24 * hourMilliseconds,
  '30d': 30 * 24 * hourMilliseconds,
}

export function isTimeRange(value: unknown): value is TimeRange {
  return typeof value === 'string' && timeRanges.some((range) => range === value)
}

export function currentTimeZone(): string {
  try {
    return new Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function serverClockOffset(serverNowMs: number, clientNowMs: number = Date.now()): number {
  assertMilliseconds(serverNowMs)
  assertMilliseconds(clientNowMs)
  return serverNowMs - clientNowMs
}

export function serverNow(offsetMs: number, clientNowMs: number = Date.now()): number {
  if (!Number.isFinite(offsetMs)) {
    throw new RangeError('Server clock offset must be finite')
  }
  assertMilliseconds(clientNowMs)
  const resolved = clientNowMs + offsetMs
  assertMilliseconds(resolved)
  return resolved
}

function assertMilliseconds(value: number): void {
  if (!Number.isSafeInteger(value)) {
    throw new RangeError('Epoch milliseconds must be a safe integer')
  }
}
