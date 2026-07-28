import type { ModelPriceRuleDto } from '@/app/resources/model-prices'

import { presentModelPriceRule } from './model-price-presenter'

const rule: ModelPriceRuleDto = {
  pattern: 'gpt-*',
  source: 'builtin',
  prices: {
    uncached_input: null,
    cache_read: 0,
    cache_write_5m: 1.25,
    cache_write_1h: null,
    output: 8,
  },
  source_url: null,
  updated_at: '2026-07-29T00:00:00Z',
  pricing_policy: {
    input_threshold_tokens: 272000,
    input_multiplier: 2,
    output_multiplier: 1.5,
  },
}

describe('Model Price presenter', () => {
  it('keeps null, explicit zero, and configured prices distinct', () => {
    const presentation = presentModelPriceRule(rule, {
      fieldLabels: {
        uncached_input: 'Uncached input',
        cache_read: 'Cache read',
        cache_write_5m: '5-minute cache write',
        cache_write_1h: '1-hour cache write',
        output: 'Output',
      },
      notConfigured: 'Not configured',
      explicitlyFree: '$0 · Explicitly free',
      configuredPrice: (value) => `$${value} / 1M`,
      kindLabel: () => 'Prefix rule',
      sourceLabel: () => 'Built-in',
      policySummary: (policy) =>
        `More than ${policy.input_threshold_tokens}: ×${policy.input_multiplier}/×${policy.output_multiplier}`,
    })

    expect(presentation.priceRows.map(({ state, value }) => ({ state, value }))).toEqual([
      { state: 'not-configured', value: 'Not configured' },
      { state: 'free', value: '$0 · Explicitly free' },
      { state: 'configured', value: '$1.25 / 1M' },
      { state: 'not-configured', value: 'Not configured' },
      { state: 'configured', value: '$8 / 1M' },
    ])
    expect(presentation.kind).toBe('Prefix rule')
    expect(presentation.source).toBe('Built-in')
    expect(presentation.sourceUrl).toBeUndefined()
    expect(presentation.policySummary).toContain('272000')
  })
})
