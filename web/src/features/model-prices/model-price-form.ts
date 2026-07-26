import type { ModelPriceRuleDto } from '@/api/control/model-prices'

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

function formatPrice(value: number | null): string {
  return value === null ? '' : String(value)
}

export function createModelPriceDraft(rule?: ModelPriceRuleDto | null): ModelPriceDraft {
  return {
    pattern: rule?.pattern ?? '',
    uncached_input: formatPrice(rule?.prices.uncached_input ?? null),
    cache_read: formatPrice(rule?.prices.cache_read ?? null),
    cache_write_5m: formatPrice(rule?.prices.cache_write_5m ?? null),
    cache_write_1h: formatPrice(rule?.prices.cache_write_1h ?? null),
    output: formatPrice(rule?.prices.output ?? null),
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

function parsePrice(raw: string): number | null | undefined {
  if (raw === '') return null
  if (!/^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(raw)) return undefined
  const value = Number(raw)
  return Number.isFinite(value) && value >= 0 ? value : undefined
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
      uncached_input: parsePrice(draft.uncached_input) ?? null,
      cache_read: parsePrice(draft.cache_read) ?? null,
      cache_write_5m: parsePrice(draft.cache_write_5m) ?? null,
      cache_write_1h: parsePrice(draft.cache_write_1h) ?? null,
      output: parsePrice(draft.output) ?? null,
    },
  }
}

export function modelPricePatternKind(pattern: string): ModelPricePatternKind {
  if (pattern === '*') return 'global'
  return pattern.endsWith('*') ? 'prefix' : 'exact'
}
