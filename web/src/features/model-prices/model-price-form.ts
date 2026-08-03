import type {
  ModelPriceDto,
  ModelPriceSlotsDto,
  ModelPriceUpdateRequest,
} from '@/app/resources/model-prices'

export const modelPriceFields = ['input', 'output', 'cache_read', 'cache_write'] as const
export type ModelPriceField = (typeof modelPriceFields)[number]
export type ModelPriceDraft = Record<ModelPriceField, string>
export type ModelPriceFormErrors = Partial<Record<ModelPriceField, 'invalid_price'>>

const maximumInt64 = 9_223_372_036_854_775_807n
const nanoUSDPerUSD = 1_000_000_000n
const acceptedPrice = /^\d+(?:\.\d{1,9})?$/u

export function createModelPriceDraft(row?: ModelPriceDto | null): ModelPriceDraft {
  return {
    input: row?.prices.input ?? '',
    output: row?.prices.output ?? '',
    cache_read: row?.prices.cache_read ?? '',
    cache_write: row?.prices.cache_write ?? '',
  }
}

function parsePrice(raw: string): string | null | undefined {
  if (raw === '') return null
  if (!acceptedPrice.test(raw)) return undefined
  const [whole = '', fraction = ''] = raw.split('.')
  const nanoUSD = BigInt(whole) * nanoUSDPerUSD + BigInt(fraction.padEnd(9, '0') || '0')
  return nanoUSD > maximumInt64 ? undefined : raw
}

export function validateModelPriceDraft(draft: ModelPriceDraft): ModelPriceFormErrors {
  const errors: ModelPriceFormErrors = {}
  for (const field of modelPriceFields) {
    if (parsePrice(draft[field]) === undefined) errors[field] = 'invalid_price'
  }
  return errors
}

export function buildModelPriceRequest(
  draft: ModelPriceDraft,
  confirmUnpriced: boolean,
): ModelPriceUpdateRequest | null {
  const errors = validateModelPriceDraft(draft)
  if (Object.keys(errors).length > 0) return null
  return {
    input: parsePrice(draft.input) ?? null,
    output: parsePrice(draft.output) ?? null,
    cache_read: parsePrice(draft.cache_read) ?? null,
    cache_write: parsePrice(draft.cache_write) ?? null,
    confirm_unpriced: confirmUnpriced,
  }
}

export function modelPriceDraftIsAllNull(draft: ModelPriceDraft): boolean {
  return modelPriceFields.every((field) => draft[field] === '')
}

export function modelPriceDraftChanged(row: ModelPriceDto, draft: ModelPriceDraft): boolean {
  return modelPriceFields.some(
    (field) => draft[field] !== (row.prices[field as keyof ModelPriceSlotsDto] ?? ''),
  )
}
