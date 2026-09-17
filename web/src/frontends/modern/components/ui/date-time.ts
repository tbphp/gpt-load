export function formatLocalDateTime(value: number | Date): string {
  const date = value instanceof Date ? value : new Date(value)
  if (!Number.isFinite(date.getTime())) return ''
  const pad = (value: number, length = 2) => String(value).padStart(length, '0')
  const fraction = date.getMilliseconds() ? '.' + pad(date.getMilliseconds(), 3) : ''
  return `${pad(date.getFullYear(), 4)}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}${fraction}`
}
export function parseLocalDateTime(value: string): Date | undefined {
  const match = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?$/.exec(
    value.trim(),
  )
  if (!match) return undefined
  const [year, month, day, hour, minute] = match.slice(1, 6).map(Number)
  if (!year || year > 9999) return undefined
  const second = Number(match[6] ?? 0)
  const fraction = Number((match[7] ?? '').padEnd(3, '0'))
  const date = new Date(0)
  date.setFullYear(year!, month! - 1, day!)
  date.setHours(hour!, minute!, second, fraction)
  return date.getFullYear() === year &&
    date.getMonth() === month! - 1 &&
    date.getDate() === day &&
    date.getHours() === hour &&
    date.getMinutes() === minute &&
    date.getSeconds() === second &&
    date.getMilliseconds() === fraction
    ? date
    : undefined
}
export function localTimeZone(): string {
  return new Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}
export function dateWithinBounds(value: string, min?: string, max?: string): boolean {
  const date = parseLocalDateTime(value)?.getTime()
  return (
    date !== undefined &&
    date >= (min ? (parseLocalDateTime(min)?.getTime() ?? -Infinity) : -Infinity) &&
    date <= (max ? (parseLocalDateTime(max)?.getTime() ?? Infinity) : Infinity)
  )
}
export function localDay(value: Date): string {
  return formatLocalDateTime(value).slice(0, 10)
}
export interface DateTimeShortcut {
  label: string
  resolve: () => string
}
export const dateRangePresets = [
  'today',
  'yesterday',
  '1h',
  '6h',
  '24h',
  '3d',
  '7d',
  '15d',
  '30d',
] as const
export type DateRangePreset = (typeof dateRangePresets)[number]
export function dateRangeFor(
  preset: DateRangePreset,
  now = Date.now(),
): { from: string; to: string } {
  let to = new Date(Math.floor(now / 1000) * 1000)
  const from = new Date(to)
  if (preset === 'today' || preset === 'yesterday') {
    from.setHours(0, 0, 0, 0)
    if (preset === 'yesterday') {
      to = new Date(from)
      from.setDate(from.getDate() - 1)
    }
  } else {
    const amount = Number(preset.slice(0, -1))
    from.setTime(to.getTime() - amount * (preset.endsWith('h') ? 3600000 : 86400000))
  }
  return { from: formatLocalDateTime(from), to: formatLocalDateTime(to) }
}
