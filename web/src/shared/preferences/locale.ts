export const supportedLocales = ['zh-CN', 'en-US', 'ja-JP'] as const
export type AppLocale = (typeof supportedLocales)[number]

export function getBrowserLocale(): AppLocale {
  try {
    const stored = window.localStorage.getItem('gpt-load.locale')
    if (supportedLocales.includes(stored as AppLocale)) return stored as AppLocale
  } catch {
    // 无法读取偏好时继续使用浏览器语言。
  }

  for (const language of [...navigator.languages, navigator.language]) {
    const family = language.toLowerCase().split('-')[0]
    if (family === 'zh') return 'zh-CN'
    if (family === 'en') return 'en-US'
    if (family === 'ja') return 'ja-JP'
  }
  return 'en-US'
}
