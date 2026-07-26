import type { ModelPriceRuleDto } from '@/api/control/model-prices'

import {
  buildModelPriceRequest,
  createModelPriceDraft,
  modelPricePatternKind,
  validateModelPriceDraft,
  type ModelPriceDraft,
} from './model-price-form'

function draft(overrides: Partial<ModelPriceDraft> = {}): ModelPriceDraft {
  return {
    pattern: 'gpt-5.6',
    uncached_input: '',
    cache_read: '',
    cache_write_5m: '',
    cache_write_1h: '',
    output: '30',
    ...overrides,
  }
}

describe('model-price form helpers', () => {
  it.each([
    ['empty', '', 'required'],
    ['more than 255 UTF-8 bytes', '界'.repeat(86), 'too_long'],
    ['leading whitespace', ' gpt-5.6', 'surrounding_whitespace'],
    ['trailing whitespace', 'gpt-5.6 ', 'surrounding_whitespace'],
    ['ASCII control character', 'gpt-\u00075.6', 'control_character'],
    ['Unicode control character', 'gpt-\u00855.6', 'control_character'],
    ['question mark', 'gpt-?', 'question_mark'],
    ['embedded star', 'gpt-*model', 'star_position'],
    ['multiple stars', 'gpt-**', 'star_position'],
  ])('rejects a pattern with %s', (_name, pattern, error) => {
    expect(validateModelPriceDraft(draft({ pattern })).pattern).toBe(error)
  })

  it('accepts a 255-byte UTF-8 pattern', () => {
    expect(
      validateModelPriceDraft(draft({ pattern: `${'界'.repeat(84)}abc` })).pattern,
    ).toBeUndefined()
  })

  it.each([
    ['negative', '-0.01'],
    ['positive infinity', 'Infinity'],
    ['negative infinity', '-Infinity'],
    ['NaN', 'NaN'],
    ['partial decimal', '.'],
    ['non-decimal', '1usd'],
    ['hexadecimal', '0x10'],
    ['surrounding whitespace', ' 1'],
  ])('rejects a %s price', (_name, value) => {
    expect(validateModelPriceDraft(draft({ cache_read: value })).fields.cache_read).toBe(
      'invalid_price',
    )
  })

  it('maps empty fields to null, preserves explicit zero, and emits all five price keys', () => {
    expect(
      buildModelPriceRequest(
        draft({
          uncached_input: '',
          cache_read: '0',
          cache_write_5m: '1.25',
          cache_write_1h: '',
          output: '9',
        }),
      ),
    ).toEqual({
      pattern: 'gpt-5.6',
      prices: {
        uncached_input: null,
        cache_read: 0,
        cache_write_5m: 1.25,
        cache_write_1h: null,
        output: 9,
      },
    })
  })

  it('rejects a rule when all five prices are empty', () => {
    const value = draft({ output: '' })

    expect(validateModelPriceDraft(value).prices).toBe('all_empty')
    expect(buildModelPriceRequest(value)).toBeNull()
  })

  it.each([
    ['gpt-5.6', 'exact'],
    ['gpt-*', 'prefix'],
    ['*', 'global'],
  ] as const)('classifies %s as %s', (pattern, kind) => {
    expect(modelPricePatternKind(pattern)).toBe(kind)
  })

  it('creates a string draft from null and zero API prices without losing their distinction', () => {
    const rule: ModelPriceRuleDto = {
      pattern: 'vendor-*',
      source: 'builtin',
      prices: {
        uncached_input: null,
        cache_read: 0,
        cache_write_5m: 1.5,
        cache_write_1h: null,
        output: 8,
      },
      source_url: 'https://example.test/pricing',
      updated_at: '2026-07-27T00:00:00Z',
    }

    expect(createModelPriceDraft(rule)).toEqual({
      pattern: 'vendor-*',
      uncached_input: '',
      cache_read: '0',
      cache_write_5m: '1.5',
      cache_write_1h: '',
      output: '8',
    })
  })
})
