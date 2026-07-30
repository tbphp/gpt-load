import type { ModelPriceRuleDto } from '@/app/resources/model-prices'

export type ModelPricePatternKind = 'exact' | 'prefix' | 'global'
export const modelPriceFields = [
  'uncached_input',
  'cache_read',
  'cache_write_5m',
  'cache_write_1h',
  'output',
] as const
export type ModelPriceField = (typeof modelPriceFields)[number]

export interface ModelPriceDraft {
  pattern: string
  uncached_input: string
  cache_read: string
  cache_write_5m: string
  cache_write_1h: string
  output: string
}

export interface ModelPriceFormErrors {
  pattern?:
    | 'required'
    | 'too_long'
    | 'surrounding_whitespace'
    | 'control_character'
    | 'question_mark'
    | 'star_position'
  prices?: 'all_empty'
  fields: Partial<Record<ModelPriceField, 'invalid_price'>>
}

const maximumInt64 = 9_223_372_036_854_775_807n
const nanoUSDPerUSD = 1_000_000_000n
const canonicalPrice = /^(?:0|[1-9]\d*)(?:\.\d{0,8}[1-9])?$/

function formatPrice(value: string | null): string {
  return value ?? ''
}

export function createModelPriceDraft(rule?: ModelPriceRuleDto | null): ModelPriceDraft {
  return {
    pattern: rule?.pattern ?? '',
    uncached_input: formatPrice(rule?.prices.input_price_usd_per_million_tokens ?? null),
    cache_read: formatPrice(rule?.prices.cache_read_price_usd_per_million_tokens ?? null),
    cache_write_5m: formatPrice(rule?.prices.cache_write_5m_price_usd_per_million_tokens ?? null),
    cache_write_1h: formatPrice(rule?.prices.cache_write_1h_price_usd_per_million_tokens ?? null),
    output: formatPrice(rule?.prices.output_price_usd_per_million_tokens ?? null),
  }
}

function patternError(pattern: string): ModelPriceFormErrors['pattern'] {
  if (pattern.length === 0) return 'required'
  if (new TextEncoder().encode(pattern).length > 255) return 'too_long'
  if (pattern.trim() !== pattern) return 'surrounding_whitespace'
  if (/\p{Cc}/u.test(pattern)) return 'control_character'
  if (pattern.includes('?')) return 'question_mark'
  const stars = pattern.match(/\*/g)?.length ?? 0
  if (stars > 1 || (stars === 1 && !pattern.endsWith('*'))) return 'star_position'
  return undefined
}

function parsePrice(raw: string): string | null | undefined {
  if (raw === '') return null
  if (!canonicalPrice.test(raw)) return undefined
  const [whole = '', fraction = ''] = raw.split('.')
  const nanoUSD = BigInt(whole) * nanoUSDPerUSD + BigInt(fraction.padEnd(9, '0') || '0')
  return nanoUSD <= maximumInt64 ? raw : undefined
}

export function validateModelPriceDraft(draft: ModelPriceDraft): ModelPriceFormErrors {
  const errors: ModelPriceFormErrors = { fields: {} }
  errors.pattern = patternError(draft.pattern)
  if (errors.pattern === undefined) delete errors.pattern

  let configured = false
  for (const field of modelPriceFields) {
    const parsed = parsePrice(draft[field])
    if (parsed === undefined) errors.fields[field] = 'invalid_price'
    else if (parsed !== null) configured = true
  }
  if (!configured && Object.keys(errors.fields).length === 0) errors.prices = 'all_empty'
  return errors
}

export function buildModelPriceRequest(draft: ModelPriceDraft): {
  pattern: string
  prices: ModelPriceRuleDto['prices']
} | null {
  const errors = validateModelPriceDraft(draft)
  if (errors.pattern || errors.prices || Object.keys(errors.fields).length > 0) return null
  return {
    pattern: draft.pattern,
    prices: {
      input_price_usd_per_million_tokens: parsePrice(draft.uncached_input) ?? null,
      output_price_usd_per_million_tokens: parsePrice(draft.output) ?? null,
      cache_read_price_usd_per_million_tokens: parsePrice(draft.cache_read) ?? null,
      cache_write_5m_price_usd_per_million_tokens: parsePrice(draft.cache_write_5m) ?? null,
      cache_write_1h_price_usd_per_million_tokens: parsePrice(draft.cache_write_1h) ?? null,
    },
  }
}

export function modelPricePatternKind(pattern: string): ModelPricePatternKind {
  if (pattern === '*') return 'global'
  return pattern.endsWith('*') ? 'prefix' : 'exact'
}
