import type {
  ModelPriceContextTierUpdateRequest,
  ModelPriceDto,
  ModelPriceScheduleDto,
  ModelPriceScheduleUpdateRequest,
  ModelPriceUpdateRequest,
} from '@/app/resources/model-prices'
import { createUUID } from '@/lib/uuid'

export const modelPriceFields = ['input', 'output', 'cache_read', 'cache_write'] as const
export type ModelPriceField = (typeof modelPriceFields)[number]
export type ModelPriceSlotDraft = Record<ModelPriceField, string>
export type ModelPriceSlotErrors = Partial<Record<ModelPriceField, 'invalid_price'>>

export interface ModelPriceTierDraft {
  /** 本地稳定 key，仅用于 v-for 与错误定位，不随阈值编辑变化。 */
  key: string
  threshold: string
  slots: ModelPriceSlotDraft
}

export interface ModelPriceTierErrors {
  threshold?: 'required' | 'invalid' | 'duplicate'
  slots?: ModelPriceSlotErrors
  emptyTier?: true
}

export interface ModelPriceScheduleDraft {
  base: ModelPriceSlotDraft
  tiers: ModelPriceTierDraft[]
}

export interface ModelPriceDraft extends ModelPriceScheduleDraft {
  modeSchedules: Record<string, ModelPriceScheduleDraft>
}

export interface ModelPriceFormErrors {
  base: ModelPriceSlotErrors
  tiers: Record<string, ModelPriceTierErrors>
  modeSchedules: Record<string, ModelPriceScheduleErrors>
}

export interface ModelPriceScheduleErrors {
  base: ModelPriceSlotErrors
  tiers: Record<string, ModelPriceTierErrors>
}

const maximumInt64 = 9_223_372_036_854_775_807n
const maximumSafeIntegerBig = 9_007_199_254_740_991n
const nanoUSDPerUSD = 1_000_000_000n
const acceptedPrice = /^\d+(?:\.\d{1,9})?$/u
const acceptedThreshold = /^(?:0|[1-9]\d*)$/u

function emptySlotDraft(): ModelPriceSlotDraft {
  return { input: '', output: '', cache_read: '', cache_write: '' }
}

export function createEmptyTierDraft(): ModelPriceTierDraft {
  return { key: createUUID(), threshold: '', slots: emptySlotDraft() }
}

function createScheduleDraft(schedule?: ModelPriceScheduleDto | null): ModelPriceScheduleDraft {
  return {
    base: {
      input: schedule?.prices.input ?? '',
      output: schedule?.prices.output ?? '',
      cache_read: schedule?.prices.cache_read ?? '',
      cache_write: schedule?.prices.cache_write ?? '',
    },
    tiers: (schedule?.context_tiers ?? []).map((tier) => ({
      key: createUUID(),
      threshold: String(tier.threshold_tokens),
      slots: {
        input: tier.prices.input ?? '',
        output: tier.prices.output ?? '',
        cache_read: tier.prices.cache_read ?? '',
        cache_write: tier.prices.cache_write ?? '',
      },
    })),
  }
}

export function createModelPriceDraft(row?: ModelPriceDto | null): ModelPriceDraft {
  const standard = createScheduleDraft(
    row ? { prices: row.prices, context_tiers: row.context_tiers } : undefined,
  )
  const modeSchedules: Record<string, ModelPriceScheduleDraft> = {}
  for (const [mode, schedule] of Object.entries(row?.mode_schedules ?? {})) {
    modeSchedules[mode] = createScheduleDraft(schedule)
  }
  return { ...standard, modeSchedules }
}

function parsePrice(raw: string): string | null | undefined {
  if (raw === '') return null
  if (!acceptedPrice.test(raw)) return undefined
  const [whole = '', fraction = ''] = raw.split('.')
  const nanoUSD = BigInt(whole) * nanoUSDPerUSD + BigInt(fraction.padEnd(9, '0') || '0')
  return nanoUSD > maximumInt64 ? undefined : raw
}

function parseThreshold(raw: string): number | undefined {
  if (!acceptedThreshold.test(raw)) return undefined
  const parsed = BigInt(raw)
  return parsed > maximumSafeIntegerBig ? undefined : Number(raw)
}

/**
 * 展示排序用途：空值或非法输入排到最后，避免尚未填写的新档位打断已有顺序。
 * 与提交前的严格校验（parseThreshold）分开，这里只用于让编辑区的行序
 * 和后端优先级（数组升序）、派生说明的排序保持一致。
 */
export function tierDisplayOrder(threshold: string): number {
  const trimmed = threshold.trim()
  if (trimmed === '') return Number.POSITIVE_INFINITY
  const parsed = Number(trimmed)
  return Number.isFinite(parsed) ? parsed : Number.POSITIVE_INFINITY
}

function validateSlots(slots: ModelPriceSlotDraft): ModelPriceSlotErrors {
  const errors: ModelPriceSlotErrors = {}
  for (const field of modelPriceFields) {
    if (parsePrice(slots[field]) === undefined) errors[field] = 'invalid_price'
  }
  return errors
}

function slotsAllEmpty(slots: ModelPriceSlotDraft): boolean {
  return modelPriceFields.every((field) => slots[field] === '')
}

function validateScheduleDraft(draft: ModelPriceScheduleDraft): ModelPriceScheduleErrors {
  const base = validateSlots(draft.base)

  const tiers: Record<string, ModelPriceTierErrors> = {}
  const seenThresholds = new Set<number>()
  for (const tier of draft.tiers) {
    const errors: ModelPriceTierErrors = {}
    const trimmed = tier.threshold.trim()
    if (trimmed === '') {
      errors.threshold = 'required'
    } else {
      const parsed = parseThreshold(trimmed)
      if (parsed === undefined) {
        errors.threshold = 'invalid'
      } else if (seenThresholds.has(parsed)) {
        errors.threshold = 'duplicate'
      } else {
        seenThresholds.add(parsed)
      }
    }

    const slotErrors = validateSlots(tier.slots)
    if (Object.keys(slotErrors).length > 0) errors.slots = slotErrors
    if (slotsAllEmpty(tier.slots)) errors.emptyTier = true

    if (Object.keys(errors).length > 0) tiers[tier.key] = errors
  }

  return { base, tiers }
}

export function validateModelPriceDraft(draft: ModelPriceDraft): ModelPriceFormErrors {
  const standard = validateScheduleDraft(draft)
  const modeSchedules: Record<string, ModelPriceScheduleErrors> = {}
  for (const [mode, schedule] of Object.entries(draft.modeSchedules)) {
    const errors = validateScheduleDraft(schedule)
    if (slotsAllEmpty(schedule.base)) {
      errors.base.input = 'invalid_price'
    }
    if (Object.keys(errors.base).length > 0 || Object.keys(errors.tiers).length > 0) {
      modeSchedules[mode] = errors
    }
  }
  return { ...standard, modeSchedules }
}

export function modelPriceFormHasErrors(errors: ModelPriceFormErrors): boolean {
  return (
    Object.keys(errors.base).length > 0 ||
    Object.keys(errors.tiers).length > 0 ||
    Object.keys(errors.modeSchedules).length > 0
  )
}

function buildTierRequests(tiers: ModelPriceTierDraft[]): ModelPriceContextTierUpdateRequest[] {
  return tiers
    .map((tier) => ({
      threshold_tokens: parseThreshold(tier.threshold.trim()) as number,
      input: parsePrice(tier.slots.input) ?? null,
      output: parsePrice(tier.slots.output) ?? null,
      cache_read: parsePrice(tier.slots.cache_read) ?? null,
      cache_write: parsePrice(tier.slots.cache_write) ?? null,
    }))
    .sort((left, right) => left.threshold_tokens - right.threshold_tokens)
}

function buildScheduleRequest(schedule: ModelPriceScheduleDraft): ModelPriceScheduleUpdateRequest {
  return {
    prices: {
      input: parsePrice(schedule.base.input) ?? null,
      output: parsePrice(schedule.base.output) ?? null,
      cache_read: parsePrice(schedule.base.cache_read) ?? null,
      cache_write: parsePrice(schedule.base.cache_write) ?? null,
    },
    context_tiers: buildTierRequests(schedule.tiers),
  }
}

export function buildModelPriceRequest(
  draft: ModelPriceDraft,
  confirmUnpriced: boolean,
): ModelPriceUpdateRequest | null {
  const errors = validateModelPriceDraft(draft)
  if (modelPriceFormHasErrors(errors)) return null

  const modeSchedules: Record<string, ModelPriceScheduleUpdateRequest> = {}
  for (const [mode, schedule] of Object.entries(draft.modeSchedules)) {
    modeSchedules[mode] = buildScheduleRequest(schedule)
  }

  return {
    input: parsePrice(draft.base.input) ?? null,
    output: parsePrice(draft.base.output) ?? null,
    cache_read: parsePrice(draft.base.cache_read) ?? null,
    cache_write: parsePrice(draft.base.cache_write) ?? null,
    context_tiers: buildTierRequests(draft.tiers),
    mode_schedules: modeSchedules,
    confirm_unpriced: confirmUnpriced,
  }
}

/** 是否处于「用户主动清空」状态；基础价格和全部 Tier 都没有任何价格。 */
export function modelPriceDraftIsAllNull(draft: ModelPriceDraft): boolean {
  return (
    slotsAllEmpty(draft.base) &&
    draft.tiers.every((tier) => slotsAllEmpty(tier.slots)) &&
    Object.keys(draft.modeSchedules).length === 0
  )
}

function scheduleDraftChanged(
  original: ModelPriceScheduleDto,
  draft: ModelPriceScheduleDraft,
): boolean {
  if (
    modelPriceFields.some((field) => draft.base[field] !== (original.prices[field] ?? '')) ||
    draft.tiers.length !== original.context_tiers.length
  ) {
    return true
  }
  return draft.tiers.some((tier, index) => {
    const originalTier = original.context_tiers[index]
    if (!originalTier) return true
    return (
      tier.threshold !== String(originalTier.threshold_tokens) ||
      modelPriceFields.some((field) => tier.slots[field] !== (originalTier.prices[field] ?? ''))
    )
  })
}

export function modelPriceDraftChanged(row: ModelPriceDto, draft: ModelPriceDraft): boolean {
  if (scheduleDraftChanged({ prices: row.prices, context_tiers: row.context_tiers }, draft)) {
    return true
  }
  const originalModes = Object.keys(row.mode_schedules).sort()
  const draftModes = Object.keys(draft.modeSchedules).sort()
  if (originalModes.join('\0') !== draftModes.join('\0')) return true
  return originalModes.some((mode) => {
    const original = row.mode_schedules[mode]
    const schedule = draft.modeSchedules[mode]
    return !original || !schedule || scheduleDraftChanged(original, schedule)
  })
}
