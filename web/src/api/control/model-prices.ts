import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function projectPrice(value: unknown): number | null {
  if (value === null) return null
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
    throw new InvalidResponseError()
  }
  return value
}

function projectPrices(value: unknown): ModelPriceValues {
  if (!isRecord(value)) throw new InvalidResponseError()
  return {
    uncached_input: projectPrice(value.uncached_input),
    cache_read: projectPrice(value.cache_read),
    cache_write_5m: projectPrice(value.cache_write_5m),
    cache_write_1h: projectPrice(value.cache_write_1h),
    output: projectPrice(value.output),
  }
}

function projectPricingPolicy(
  value: unknown,
  source: ModelPriceSource,
): ModelPricePolicyDto | null {
  if (value === null) return null
  if (source === 'user' || !isRecord(value)) throw new InvalidResponseError()
  const keys = Object.keys(value).sort()
  if (
    keys.length !== 3 ||
    keys[0] !== 'input_multiplier' ||
    keys[1] !== 'input_threshold_tokens' ||
    keys[2] !== 'output_multiplier' ||
    !Number.isSafeInteger(value.input_threshold_tokens) ||
    (value.input_threshold_tokens as number) <= 0 ||
    typeof value.input_multiplier !== 'number' ||
    !Number.isFinite(value.input_multiplier) ||
    value.input_multiplier <= 0 ||
    typeof value.output_multiplier !== 'number' ||
    !Number.isFinite(value.output_multiplier) ||
    value.output_multiplier <= 0
  ) {
    throw new InvalidResponseError()
  }
  return {
    input_threshold_tokens: value.input_threshold_tokens as number,
    input_multiplier: value.input_multiplier,
    output_multiplier: value.output_multiplier,
  }
}

function isRFC3339Timestamp(value: string): boolean {
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$/.exec(
      value,
    )
  if (match === null) return false
  const [year, month, day, hour, minute, second] = match.slice(1, 7).map(Number)
  const [offsetHour, offsetMinute] = match.slice(7, 9).map(Number)
  if (
    month < 1 ||
    month > 12 ||
    hour > 23 ||
    minute > 59 ||
    second > 59 ||
    (match[7] !== undefined && (offsetHour > 23 || offsetMinute > 59))
  ) {
    return false
  }
  const daysInMonth = [
    31,
    year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ]
  return day >= 1 && day <= daysInMonth[month - 1]
}

function projectRule(value: unknown): ModelPriceRuleDto {
  if (
    !isRecord(value) ||
    typeof value.pattern !== 'string' ||
    (value.source !== 'builtin' && value.source !== 'user') ||
    typeof value.updated_at !== 'string' ||
    !isRFC3339Timestamp(value.updated_at)
  ) {
    throw new InvalidResponseError()
  }
  const sourceURL = value.source === 'builtin' ? projectBuiltinSourceURL(value.source_url) : null
  if (value.source === 'user' && value.source_url !== null) throw new InvalidResponseError()
  const pricingPolicy = projectPricingPolicy(value.pricing_policy, value.source)
  return {
    pattern: value.pattern,
    source: value.source,
    prices: projectPrices(value.prices),
    source_url: sourceURL,
    updated_at: value.updated_at,
    pricing_policy: pricingPolicy,
  }
}

function projectBuiltinSourceURL(value: unknown): string {
  if (typeof value !== 'string' || value === '') throw new InvalidResponseError()
  return value
}

export function projectModelPrices(value: unknown): ModelPriceReportDto {
  if (
    !isRecord(value) ||
    value.price_unit !== 'usd_per_million_tokens' ||
    !Array.isArray(value.builtin) ||
    !Array.isArray(value.overrides)
  ) {
    throw new InvalidResponseError()
  }
  const builtin = value.builtin.map(projectRule)
  const overrides = value.overrides.map(projectRule)
  if (
    builtin.some((rule) => rule.source !== 'builtin') ||
    overrides.some((rule) => rule.source !== 'user')
  ) {
    throw new InvalidResponseError()
  }
  return { price_unit: 'usd_per_million_tokens', builtin, overrides }
}

export async function getModelPrices(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<ModelPriceReportDto> {
  return projectModelPrices(
    await client.request<unknown>('/api/model-prices', { method: 'GET', signal }),
  )
}

export function putModelPrice(
  client: ApiClient,
  pattern: string,
  prices: ModelPriceValues,
  signal?: AbortSignal,
): Promise<void> {
  return client.request<void>('/api/model-prices', {
    method: 'PUT',
    json: {
      pattern,
      prices: {
        uncached_input: prices.uncached_input,
        cache_read: prices.cache_read,
        cache_write_5m: prices.cache_write_5m,
        cache_write_1h: prices.cache_write_1h,
        output: prices.output,
      },
    },
    signal,
  })
}

export function resetModelPrice(
  client: ApiClient,
  pattern: string,
  signal?: AbortSignal,
): Promise<void> {
  const encodedPattern = encodeURIComponent(pattern).replaceAll('*', '%2A')
  return client.request<void>(`/api/model-prices?pattern=${encodedPattern}`, {
    method: 'DELETE',
    signal,
  })
}
