import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import { getModelPrices, modelPriceMutationInvalidations, projectModelPrices } from './model-prices'

const prices = {
  uncached_input: 1.25,
  cache_read: 0,
  cache_write_5m: null,
  cache_write_1h: 3.5,
  output: 7.5,
}

const builtin = {
  pattern: 'gpt-5.6',
  source: 'builtin',
  prices,
  source_url: null,
  updated_at: '2026-07-29T01:00:00Z',
  pricing_policy: null,
} as const

const report = {
  price_unit: 'usd_per_million_tokens',
  builtin: [builtin],
  overrides: [],
} as const

describe('ModelPrice resource', () => {
  it('accepts an absent built-in source URL and projects transport data', async () => {
    expect(projectModelPrices(report)).toEqual(report)
    const request = vi.fn().mockResolvedValue(report) as ApiClient['request']
    await expect(getModelPrices({ request })).resolves.toEqual(report)
  })

  it.each(['ftp://example.test/pricing', 'javascript:alert(1)', ' https://example.test/pricing'])(
    'rejects a non-HTTP(S) built-in source URL %s',
    (sourceURL) => {
      expect(() =>
        projectModelPrices({
          ...report,
          builtin: [{ ...builtin, source_url: sourceURL }],
        }),
      ).toThrow(InvalidResponseError)
    },
  )

  it.each([Number.NaN, Number.POSITIVE_INFINITY, -0.01])(
    'rejects an invalid finite price %s',
    (output) => {
      expect(() =>
        projectModelPrices({
          ...report,
          builtin: [{ ...builtin, prices: { ...prices, output } }],
        }),
      ).toThrow(InvalidResponseError)
    },
  )

  it('owns exact upsert and reset invalidations', () => {
    expect(modelPriceMutationInvalidations).toEqual({
      upsert: [controlQueryKeys.modelPrices()],
      reset: [controlQueryKeys.modelPrices()],
    })
  })
})
