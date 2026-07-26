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
} as const

describe('ModelPrice control API', () => {
  it('projects valid builtin and user rules while preserving explicit null and zero prices', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue([builtinRule, userRule])

    await expect(
      getModelPrices({ request: request as ApiClient['request'] }, signal),
    ).resolves.toEqual({
      price_unit: 'usd_per_million_tokens',
      builtin: [builtinRule],
      overrides: [userRule],
    })
    expect(request).toHaveBeenCalledWith('/api/model-prices', { method: 'GET', signal })
  })

  it.each([
    ['non-array response', {}],
    ['malformed rule', [{}]],
    ['unknown source', [{ ...builtinRule, source: 'remote' }]],
    ['malformed timestamp', [{ ...builtinRule, updated_at: 'tomorrow' }]],
    ['missing five-price key', [{ ...builtinRule, prices: { ...prices, output: undefined } }]],
    [
      'non-finite price',
      [{ ...builtinRule, prices: { ...prices, output: Number.POSITIVE_INFINITY } }],
    ],
    ['negative price', [{ ...builtinRule, prices: { ...prices, output: -0.01 } }]],
    ['builtin without source URL', [{ ...builtinRule, source_url: null }]],
    ['user with source URL', [{ ...userRule, source_url: 'https://example.test/pricing' }]],
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
