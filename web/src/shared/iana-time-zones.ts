export interface IanaTimeZoneOption {
  value: string
  label: string
}

const fallbackTimeZones = [
  'Pacific/Honolulu',
  'America/Anchorage',
  'America/Los_Angeles',
  'America/Denver',
  'America/Chicago',
  'America/New_York',
  'America/Toronto',
  'America/Sao_Paulo',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Moscow',
  'Africa/Johannesburg',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Bangkok',
  'Asia/Shanghai',
  'Asia/Hong_Kong',
  'Asia/Taipei',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Australia/Perth',
  'Australia/Sydney',
  'Pacific/Auckland',
] as const

type IntlWithSupportedValues = typeof Intl & {
  supportedValuesOf?: (key: 'timeZone') => string[]
}

function supportedTimeZones(): string[] {
  try {
    return (Intl as IntlWithSupportedValues).supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}

const zones = Array.from(new Set(['UTC', ...supportedTimeZones(), ...fallbackTimeZones]))
const optionCache = new Map<string, IanaTimeZoneOption[]>()
const timeZoneNameReference = new Date(Date.UTC(2026, 0, 15, 12))

function localizedTimeZoneName(value: string, locale: string): string | undefined {
  if (value === 'UTC') {
    if (locale.toLowerCase().startsWith('zh')) return '协调世界时'
    if (locale.toLowerCase().startsWith('ja')) return '協定世界時'
  }
  try {
    return new Intl.DateTimeFormat(locale, {
      timeZone: value,
      timeZoneName: 'longGeneric',
    })
      .formatToParts(timeZoneNameReference)
      .find((part) => part.type === 'timeZoneName')?.value
  } catch {
    return undefined
  }
}

function conciseTimeZoneName(value: string, locale: string): string {
  const language = locale.toLowerCase()
  if (language.startsWith('zh')) return value.replace(/(?:标准时间|时间)$/, '')
  if (language.startsWith('ja')) return value.replace(/(?:標準時|時間)$/, '')
  return value.replace(/\s+(?:Standard\s+)?Time$/, '')
}

export function ianaTimeZoneOptions(locale: string): IanaTimeZoneOption[] {
  const normalizedLocale = locale || 'en-US'
  const cached = optionCache.get(normalizedLocale)
  if (cached) return cached
  const options = zones.map((value) => {
    const name = localizedTimeZoneName(value, normalizedLocale)
    const conciseName = name ? conciseTimeZoneName(name, normalizedLocale) : ''
    return {
      value,
      label: conciseName && conciseName !== value ? `${conciseName} · ${value}` : value,
    }
  })
  optionCache.set(normalizedLocale, options)
  return options
}
