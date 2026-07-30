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
