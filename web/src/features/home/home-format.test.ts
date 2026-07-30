import { formatCompactMetric } from './home-format'

describe('formatCompactMetric', () => {
  it('uses stable K/M/B units instead of locale-specific long compact notation', () => {
    expect(formatCompactMetric(9_200_000, 'zh-CN')).toBe('9.2M')
    expect(formatCompactMetric(2_100_000, 'en-US')).toBe('2.1M')
    expect(formatCompactMetric(600_000, 'zh-CN')).toBe('0.6M')
    expect(formatCompactMetric(400_000, 'ja-JP')).toBe('0.4M')
    expect(formatCompactMetric(1_600, 'zh-CN')).toBe('1.6K')
    expect(formatCompactMetric(820, 'zh-CN')).toBe('820')
  })
})
