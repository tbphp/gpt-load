import { queryOptions } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectEnum,
  projectFiniteNumber,
  projectHTTPURL,
  projectISOInstant,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type ModelPriceSource = 'builtin' | 'user'

export interface ModelPriceValues {
  uncached_input: number | null
  cache_read: number | null
  cache_write_5m: number | null
  cache_write_1h: number | null
  output: number | null
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
  updated_at: string
  pricing_policy: ModelPricePolicyDto | null
}

export interface ModelPriceReportDto {
  price_unit: 'usd_per_million_tokens'
  builtin: ModelPriceRuleDto[]
  overrides: ModelPriceRuleDto[]
}

const priceFields = [
  'uncached_input',
  'cache_read',
  'cache_write_5m',
  'cache_write_1h',
  'output',
] as const
const ruleFields = [
  'pattern',
  'source',
  'prices',
  'source_url',
  'updated_at',
  'pricing_policy',
] as const
const policyFields = ['input_threshold_tokens', 'input_multiplier', 'output_multiplier'] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectPrice(value: unknown): number | null {
  return value === null ? null : projectFiniteNumber(value, { minimum: 0 })
}

function projectPrices(value: unknown): ModelPriceValues {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, priceFields)
  return {
    uncached_input: projectPrice(record.uncached_input),
    cache_read: projectPrice(record.cache_read),
    cache_write_5m: projectPrice(record.cache_write_5m),
    cache_write_1h: projectPrice(record.cache_write_1h),
    output: projectPrice(record.output),
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
    updated_at: projectISOInstant(record.updated_at),
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
