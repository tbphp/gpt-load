import { resolveLocalTimeZone } from './date-time'

describe('local timezone resolution', () => {
  it('uses the browser timezone and falls back honestly when resolution fails', () => {
    expect(resolveLocalTimeZone(() => 'Asia/Shanghai')).toBe('Asia/Shanghai')
    expect(resolveLocalTimeZone(() => '')).toBe('UTC')
    expect(
      resolveLocalTimeZone(() => {
        throw new Error('timezone unavailable')
      }),
    ).toBe('UTC')
  })
})
