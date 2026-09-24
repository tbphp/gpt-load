// 跨组件和页面复用格式化器，避免逐行、逐单元格重复创建 Intl 实例。
const numbers = new Map<string, Intl.NumberFormat>()
const dates = new Map<string, Intl.DateTimeFormat>()
const relatives = new Map<string, Intl.RelativeTimeFormat>()
const maximumEntries = 64

export function numberFormatter(
  locale: string,
  options: Intl.NumberFormatOptions = {},
): Intl.NumberFormat {
  const key = JSON.stringify([locale, options])
  let formatter = numbers.get(key)
  if (!formatter) {
    formatter = new Intl.NumberFormat(locale, options)
    if (numbers.size >= maximumEntries) {
      const oldest = numbers.keys().next().value
      if (oldest !== undefined) numbers.delete(oldest)
    }
    numbers.set(key, formatter)
  }
  return formatter
}

export function dateFormatter(
  locale: string,
  options: Intl.DateTimeFormatOptions = {},
): Intl.DateTimeFormat {
  const key = JSON.stringify([locale, options])
  let formatter = dates.get(key)
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(locale, options)
    if (dates.size >= maximumEntries) {
      const oldest = dates.keys().next().value
      if (oldest !== undefined) dates.delete(oldest)
    }
    dates.set(key, formatter)
  }
  return formatter
}

export function relativeTimeFormatter(locale: string): Intl.RelativeTimeFormat {
  let formatter = relatives.get(locale)
  if (!formatter) {
    formatter = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
    if (relatives.size >= maximumEntries) {
      const oldest = relatives.keys().next().value
      if (oldest !== undefined) relatives.delete(oldest)
    }
    relatives.set(locale, formatter)
  }
  return formatter
}
