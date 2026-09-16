// CSS 媒体条件中的阈值由 check:styles 校验；需要 JS 行为时从此处读取。
export const breakpoints = {
  narrow: 420,
  mobile: 760,
  compact: 1150,
  wide: 1700,
} as const

export const desktopMediaQuery = `(width > ${breakpoints.mobile}px)`
