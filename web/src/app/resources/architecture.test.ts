import { describe, expect, it } from 'vitest'

const featureSources = import.meta.glob('../../features/**/*.{ts,vue}', {
  eager: true,
  import: 'default',
  query: '?raw',
}) as Record<string, string>

describe('frontend resource architecture', () => {
  it('forbids feature modules from bypassing resource APIs through ApiClient.request', () => {
    const violations = Object.entries(featureSources)
      .filter(([path]) => !path.includes('.test.'))
      .filter(([, source]) => /\.request(?:WithResponse)?(?:<[^>]+>)?\s*\(/.test(source))
      .map(([path]) => path)

    expect(violations).toEqual([])
  })
})
