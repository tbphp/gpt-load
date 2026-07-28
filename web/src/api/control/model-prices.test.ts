import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import {
  getModelPrices,
  projectModelPrices,
  putModelPrice,
  resetModelPrice,
  type ModelPriceValues,
} from './model-prices'

const prices: ModelPriceValues = {
  uncached_input: 1.25,
  cache_read: 0,
  cache_write_5m: null,
  cache_write_1h: 3.5,
  output: 7.5,
}

const builtinRule = {
  pattern: 'gpt-5.6',
  source: 'builtin',
  prices,
  source_url: 'https://developers.openai.com/api/docs/pricing',
  updated_at: '2026-07-26T00:00:00Z',
  pricing_policy: {
    input_threshold_tokens: 272000,
    input_multiplier: 2,
    output_multiplier: 1.5,
  },
} as const

const userRule = {
  pattern: 'my-model-*',
  source: 'user',
  prices: {
    uncached_input: null,
    cache_read: 0,
    cache_write_5m: null,
    cache_write_1h: null,
    output: 9,
  },
  source_url: null,
  updated_at: '2026-07-27T12:30:00Z',
  pricing_policy: null,
} as const

describe('ModelPrice control API', () => {
  it('projects valid builtin and user rules while preserving explicit null and zero prices', async () => {
    const signal = new AbortController().signal
    const response = {
      price_unit: 'usd_per_million_tokens',
      builtin: [builtinRule],
      overrides: [userRule],
    } as const
    const request = vi.fn().mockResolvedValue(response)

    await expect(
      getModelPrices({ request: request as ApiClient['request'] }, signal),
    ).resolves.toEqual(response)
    expect(request).toHaveBeenCalledWith('/api/model-prices', { method: 'GET', signal })
  })

  it.each([
    ['legacy flat response', [builtinRule, userRule]],
    [
      'wrong price unit',
      {
        price_unit: 'usd_per_token',
        builtin: [builtinRule],
        overrides: [userRule],
      },
    ],
    ['missing builtin partition', { price_unit: 'usd_per_million_tokens', overrides: [userRule] }],
    [
      'malformed rule',
      { price_unit: 'usd_per_million_tokens', builtin: [{}], overrides: [userRule] },
    ],
    [
      'unknown source',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, source: 'remote' }],
        overrides: [userRule],
      },
    ],
    [
      'user source in builtin partition',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [userRule],
        overrides: [],
      },
    ],
    [
      'builtin source in override partition',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [],
        overrides: [builtinRule],
      },
    ],
    [
      'malformed timestamp',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, updated_at: 'tomorrow' }],
        overrides: [userRule],
      },
    ],
    [
      'normalized invalid calendar timestamp',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, updated_at: '2026-02-30T00:00:00Z' }],
        overrides: [userRule],
      },
    ],
    [
      'invalid RFC3339 offset',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, updated_at: '2026-07-27T12:30:00+24:00' }],
        overrides: [userRule],
      },
    ],
    [
      'missing five-price key',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, prices: { ...prices, output: undefined } }],
        overrides: [userRule],
      },
    ],
    [
      'non-finite price',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, prices: { ...prices, output: Number.POSITIVE_INFINITY } }],
        overrides: [userRule],
      },
    ],
    [
      'negative price',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, prices: { ...prices, output: -0.01 } }],
        overrides: [userRule],
      },
    ],
    [
      'user with source URL',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [builtinRule],
        overrides: [{ ...userRule, source_url: 'https://example.test/pricing' }],
      },
    ],
    [
      'missing pricing policy',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, pricing_policy: undefined }],
        overrides: [userRule],
      },
    ],
    [
      'non-object pricing policy',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [{ ...builtinRule, pricing_policy: 'long-context' }],
        overrides: [userRule],
      },
    ],
    [
      'pricing policy with an unknown field',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [
          {
            ...builtinRule,
            pricing_policy: { ...builtinRule.pricing_policy, inherited_by_user: false },
          },
        ],
        overrides: [userRule],
      },
    ],
    [
      'pricing policy with a non-positive threshold',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [
          {
            ...builtinRule,
            pricing_policy: { ...builtinRule.pricing_policy, input_threshold_tokens: 0 },
          },
        ],
        overrides: [userRule],
      },
    ],
    [
      'pricing policy with a fractional threshold',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [
          {
            ...builtinRule,
            pricing_policy: { ...builtinRule.pricing_policy, input_threshold_tokens: 272000.5 },
          },
        ],
        overrides: [userRule],
      },
    ],
    [
      'pricing policy with a non-finite multiplier',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [
          {
            ...builtinRule,
            pricing_policy: {
              ...builtinRule.pricing_policy,
              output_multiplier: Number.POSITIVE_INFINITY,
            },
          },
        ],
        overrides: [userRule],
      },
    ],
    [
      'user pricing policy',
      {
        price_unit: 'usd_per_million_tokens',
        builtin: [builtinRule],
        overrides: [{ ...userRule, pricing_policy: builtinRule.pricing_policy }],
      },
    ],
  ])('rejects %s', (_name, value) => {
    expect(() => projectModelPrices(value)).toThrow(InvalidResponseError)
  })

  it('sends a complete replacement payload with every price key', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue(undefined)
    const client: ApiClient = { request: request as ApiClient['request'] }

    await putModelPrice(client, 'my-model-*', userRule.prices, signal)

    expect(request).toHaveBeenCalledWith('/api/model-prices', {
      method: 'PUT',
      json: {
        pattern: 'my-model-*',
        prices: {
          uncached_input: null,
          cache_read: 0,
          cache_write_5m: null,
          cache_write_1h: null,
          output: 9,
        },
      },
      signal,
    })
  })

  it('URL-encodes wildcard patterns when resetting a user override', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue(undefined)

    await resetModelPrice({ request: request as ApiClient['request'] }, 'my-model-*', signal)

    expect(request).toHaveBeenCalledWith('/api/model-prices?pattern=my-model-%2A', {
      method: 'DELETE',
      signal,
    })
  })
})
