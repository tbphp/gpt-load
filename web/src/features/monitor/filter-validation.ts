const textEncoder = new TextEncoder()

export const maxSignedInt64 = 9_223_372_036_854_775_807n

export function isValidMonitorText(value: string): boolean {
  return (
    value !== '' &&
    value.trim() === value &&
    !/\p{Cc}/u.test(value) &&
    textEncoder.encode(value).byteLength <= 255
  )
}

export function normalizeMonitorText(raw: unknown): string | undefined {
  return typeof raw === 'string' && isValidMonitorText(raw) ? raw : undefined
}
