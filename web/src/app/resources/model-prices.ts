import { queryOptions } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectEpochMilliseconds,
  projectEnum,
  projectFiniteNumber,
  projectHTTPURL,
  projectNullableDecimalString,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type ModelPriceSource = 'builtin' | 'user'

export interface ModelPriceValues {
  input_price_usd_per_million_tokens: string | null
  output_price_usd_per_million_tokens: string | null
  cache_read_price_usd_per_million_tokens: string | null
  cache_write_5m_price_usd_per_million_tokens: string | null
  cache_write_1h_price_usd_per_million_tokens: string | null
}

export interface ModelPricePolicyDto {
  input_threshold_tokens: number
  input_multiplier: number
  output_multiplier: number
}

export interface ModelPriceRuleDto {
  pattern: string
  source: ModelPriceSource
  prices: ModelPriceValues
  source_url: string | null
  updated_at_ms: number
  pricing_policy: ModelPricePolicyDto | null
}

export interface ModelPriceReportDto {
  price_unit: 'usd_per_million_tokens'
  builtin: ModelPriceRuleDto[]
  overrides: ModelPriceRuleDto[]
}

const priceFields = [
  'input_price_usd_per_million_tokens',
  'output_price_usd_per_million_tokens',
  'cache_read_price_usd_per_million_tokens',
  'cache_write_5m_price_usd_per_million_tokens',
  'cache_write_1h_price_usd_per_million_tokens',
] as const
const ruleFields = [
  'pattern',
  'source',
  'prices',
  'source_url',
  'updated_at_ms',
  'pricing_policy',
] as const
const policyFields = ['input_threshold_tokens', 'input_multiplier', 'output_multiplier'] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectPrices(value: unknown): ModelPriceValues {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, priceFields)
  return {
    input_price_usd_per_million_tokens: projectNullableDecimalString(
      record.input_price_usd_per_million_tokens,
    ),
    output_price_usd_per_million_tokens: projectNullableDecimalString(
      record.output_price_usd_per_million_tokens,
    ),
    cache_read_price_usd_per_million_tokens: projectNullableDecimalString(
      record.cache_read_price_usd_per_million_tokens,
    ),
    cache_write_5m_price_usd_per_million_tokens: projectNullableDecimalString(
      record.cache_write_5m_price_usd_per_million_tokens,
    ),
    cache_write_1h_price_usd_per_million_tokens: projectNullableDecimalString(
      record.cache_write_1h_price_usd_per_million_tokens,
    ),
  }
}

function projectPricingPolicy(
  value: unknown,
  source: ModelPriceSource,
): ModelPricePolicyDto | null {
  if (value === null) return null
  if (source === 'user') invalidResponse()
  const record = projectRecord(value)
  if (
    Object.keys(record).length !== policyFields.length ||
    policyFields.some((field) => !Object.prototype.hasOwnProperty.call(record, field))
  ) {
    invalidResponse()
  }
  return {
    input_threshold_tokens: projectSafeInteger(record.input_threshold_tokens, { minimum: 1 }),
    input_multiplier: projectFiniteNumber(record.input_multiplier, { minimum: Number.MIN_VALUE }),
    output_multiplier: projectFiniteNumber(record.output_multiplier, { minimum: Number.MIN_VALUE }),
  }
}

function projectRule(value: unknown): ModelPriceRuleDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ruleFields)
  const pattern = projectString(record.pattern)
  if (pattern.trim().length === 0 || pattern !== pattern.trim()) invalidResponse()
  const source = projectEnum(record.source, ['builtin', 'user'] as const)
  let sourceURL: string | null
  if (source === 'builtin') {
    sourceURL = record.source_url === null ? null : projectHTTPURL(record.source_url)
  } else {
    if (record.source_url !== null) invalidResponse()
    sourceURL = null
  }
  return {
    pattern,
    source,
    prices: projectPrices(record.prices),
    source_url: sourceURL,
    updated_at_ms: projectEpochMilliseconds(record.updated_at_ms),
    pricing_policy: projectPricingPolicy(record.pricing_policy, source),
  }
}

export function projectModelPrices(value: unknown): ModelPriceReportDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['price_unit', 'builtin', 'overrides'])
  if (record.price_unit !== 'usd_per_million_tokens') invalidResponse()
  const builtin = projectArray(record.builtin, projectRule)
  const overrides = projectArray(record.overrides, projectRule)
  if (
    builtin.some((rule) => rule.source !== 'builtin') ||
    overrides.some((rule) => rule.source !== 'user')
  ) {
    invalidResponse()
  }
  return { price_unit: 'usd_per_million_tokens', builtin, overrides }
}

export async function getModelPrices(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<ModelPriceReportDto> {
  return projectModelPrices(await client.request('/api/model-prices', { method: 'GET', signal }))
}

export function modelPriceQueryOptions(client: ApiClient) {
  return queryOptions({
    queryKey: controlQueryKeys.modelPrices(),
    queryFn: ({ signal }) => getModelPrices(client, signal),
  })
}

export function putModelPrice(
  client: ApiClient,
  pattern: string,
  prices: ModelPriceValues,
  signal?: AbortSignal,
): Promise<void> {
  return client.request('/api/model-prices', {
    method: 'PUT',
    json: { pattern, prices },
    signal,
  })
}

export function resetModelPrice(
  client: ApiClient,
  pattern: string,
  signal?: AbortSignal,
): Promise<void> {
  const encodedPattern = encodeURIComponent(pattern).replaceAll('*', '%2A')
  return client.request(`/api/model-prices?pattern=${encodedPattern}`, {
    method: 'DELETE',
    signal,
  })
}
