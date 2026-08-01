import type { GroupModelItemDto } from '@/api/control/types'
import type { GroupModelUpdateDto } from '@/app/resources/groups'

export interface ModelDraftItem extends GroupModelUpdateDto {
  key: number
  pricing_status: 'priced' | 'unpriced'
}

export interface ModelNameConflict {
  client_model: string
  indexes: number[]
}

export function normalizeModel(model: GroupModelUpdateDto): GroupModelUpdateDto | undefined {
  const id = model.id.trim()
  if (!id) return undefined
  const alias = model.alias_enabled ? model.alias.trim() : ''
  return { id, alias, alias_enabled: model.alias_enabled }
}

export function clientModel(model: GroupModelUpdateDto): string {
  const normalized = normalizeModel(model)
  return normalized === undefined ? '' : normalized.alias_enabled ? normalized.alias : normalized.id
}

/** Client names are intentionally exact and case sensitive, matching the API contract. */
export function findModelNameConflicts(
  models: readonly GroupModelUpdateDto[],
): ModelNameConflict[] {
  const byClientModel = new Map<string, number[]>()
  for (const [index, model] of models.entries()) {
    const name = clientModel(model)
    if (!name) continue
    byClientModel.set(name, [...(byClientModel.get(name) ?? []), index])
  }
  return [...byClientModel.entries()]
    .filter(([, indexes]) => indexes.length > 1)
    .map(([client_model, indexes]) => ({ client_model, indexes }))
}

export function indexesWithConflicts(conflicts: readonly ModelNameConflict[]): Set<number> {
  return new Set(conflicts.flatMap((conflict) => conflict.indexes))
}

export function createModelDraft(items: readonly GroupModelItemDto[]): ModelDraftItem[] {
  return items.map((item, index) => ({
    id: item.id,
    alias: item.alias,
    alias_enabled: item.alias_enabled,
    pricing_status: item.pricing_status,
    key: index,
  }))
}

export function normalizedModels(draft: readonly ModelDraftItem[]): GroupModelUpdateDto[] {
  return draft.flatMap((item) => {
    const normalized = normalizeModel(item)
    return normalized === undefined ? [] : [normalized]
  })
}

export function sameModels(
  left: readonly ModelDraftItem[],
  right: readonly ModelDraftItem[],
): boolean {
  return JSON.stringify(normalizedModels(left)) === JSON.stringify(normalizedModels(right))
}
