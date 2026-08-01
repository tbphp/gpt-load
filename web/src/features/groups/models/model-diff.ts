import type { GroupModelItemDto } from '@/api/control/types'
import {
  findModelNameConflicts,
  normalizedModels as normalizeSharedModels,
  sameModels as sameSharedModels,
  type ModelDraftValue,
  type ModelNameConflict,
} from '@/features/models/model-draft'

export interface ModelDraftItem extends ModelDraftValue {
  key: number
  pricing_status: 'priced' | 'unpriced'
}

export type { ModelNameConflict }
export { findModelNameConflicts }

export function createModelDraft(items: readonly GroupModelItemDto[]): ModelDraftItem[] {
  return items.map((item, index) => ({
    id: item.id,
    alias: item.alias,
    alias_enabled: item.alias_enabled,
    pricing_status: item.pricing_status,
    key: index,
  }))
}

export function normalizedModels(draft: readonly ModelDraftItem[]) {
  return normalizeSharedModels(draft)
}

export function sameModels(
  left: readonly ModelDraftItem[],
  right: readonly ModelDraftItem[],
): boolean {
  return sameSharedModels(left, right)
}
