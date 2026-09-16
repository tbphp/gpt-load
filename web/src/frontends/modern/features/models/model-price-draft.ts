import {
  priceFields,
  type PriceField,
  type ModelPrice,
  type ModelPriceUpdate,
  type PriceSlots,
  type PriceSchedule,
} from '@modern/api/models'

export interface PriceDraftTier {
  key: string
  threshold: string
  prices: Record<PriceField, string>
}
export interface PriceDraftSchedule {
  mode: string
  prices: Record<PriceField, string>
  tiers: PriceDraftTier[]
}
export function draftSlots(slots?: PriceSlots): Record<PriceField, string> {
  return {
    input: slots?.input ?? '',
    output: slots?.output ?? '',
    cache_read: slots?.cache_read ?? '',
    cache_write: slots?.cache_write ?? '',
  }
}
let nextTier = 0
export function newPriceTier(): PriceDraftTier {
  return { key: String(++nextTier), threshold: '', prices: draftSlots() }
}
export function priceDraft(price: ModelPrice): PriceDraftSchedule[] {
  const read = (mode: string, schedule: PriceSchedule): PriceDraftSchedule => ({
    mode,
    prices: draftSlots(schedule.prices),
    tiers: schedule.context_tiers.map((tier) => ({
      key: String(++nextTier),
      threshold: String(tier.threshold_tokens),
      prices: draftSlots(tier.prices),
    })),
  })
  return [
    read('standard', price),
    ...Object.entries(price.mode_schedules).map(([mode, schedule]) => read(mode, schedule)),
  ]
}
function validPrice(raw: string): boolean {
  if (!raw) return true
  if (!/^\d+(\.\d{1,9})?$/.test(raw)) return false
  const [whole = '0', fraction = ''] = raw.split('.')
  return (
    BigInt(whole) * 1_000_000_000n + BigInt(fraction.padEnd(9, '0')) <= 9_223_372_036_854_775_807n
  )
}
export function priceDraftErrors(draft: PriceDraftSchedule[]): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const schedule of draft) {
    for (const field of priceFields)
      if (!validPrice(schedule.prices[field].trim()))
        errors[schedule.mode + '.' + field] = 'invalidPrice'
    if (
      schedule.mode !== 'standard' &&
      priceFields.every((field) => !schedule.prices[field].trim())
    )
      errors[schedule.mode] = 'emptyMode'
    const thresholds = new Set<number>()
    for (const tier of schedule.tiers) {
      const threshold = tier.threshold.trim()
      const value = Number(threshold)
      if (
        !/^(0|[1-9]\d*)$/.test(threshold) ||
        !Number.isSafeInteger(value) ||
        thresholds.has(value)
      )
        errors[tier.key] = 'invalidThreshold'
      thresholds.add(value)
      if (priceFields.every((field) => !tier.prices[field].trim()))
        errors[tier.key + '.prices'] = 'emptyTier'
      for (const field of priceFields)
        if (!validPrice(tier.prices[field].trim())) errors[tier.key + '.' + field] = 'invalidPrice'
    }
  }
  return errors
}
export function priceDraftRequest(draft: PriceDraftSchedule[]): ModelPriceUpdate {
  const slots = (prices: Record<PriceField, string>): PriceSlots => ({
    input: prices.input.trim() || null,
    output: prices.output.trim() || null,
    cache_read: prices.cache_read.trim() || null,
    cache_write: prices.cache_write.trim() || null,
  })
  const tiers = (schedule: PriceDraftSchedule) =>
    schedule.tiers
      .map((tier) => ({ threshold_tokens: Number(tier.threshold), ...slots(tier.prices) }))
      .sort((a, b) => a.threshold_tokens - b.threshold_tokens)
  const standard = draft.find((schedule) => schedule.mode === 'standard')!
  return {
    ...slots(standard.prices),
    context_tiers: tiers(standard),
    mode_schedules: Object.fromEntries(
      draft
        .filter((schedule) => schedule.mode !== 'standard')
        .map((schedule) => [
          schedule.mode,
          { prices: slots(schedule.prices), context_tiers: tiers(schedule) },
        ]),
    ),
    confirm_unpriced:
      draft.length === 1 &&
      standard.tiers.length === 0 &&
      priceFields.every((field) => !standard.prices[field].trim()),
  }
}
