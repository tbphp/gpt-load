import type { GroupModelUpdateDto } from '@/app/resources/groups'

export type ModelDraftKey = string | number

export interface ModelDraftValue extends GroupModelUpdateDto {
  key: ModelDraftKey
  editable_id?: boolean
}

export interface ModelNameConflict {
  client_model: string
  indexes: number[]
}

export interface ModelAliasEditorLabels {
  tableLabel: string
  id: string
  alias: string
  thirdColumn: string
  actions: string
  search: string
  searchLabel: string
  clearSearch: string
  aliasEnabledFor: (id: string) => string
  aliasFor: (id: string) => string
  aliasPlaceholder: string
  aliasRequired: string
  removeFor: (id: string) => string
  manualId: string
  manualIdRequired: string
  add: string
  addInline: string
  count: (count: number) => string
  empty: string
  noMatches: string
  nameConflict: (name: string) => string
}

export interface ModelDiscoveryDrawerLabels {
  title: string
  description: string
  close: string
  loading: string
  notice: string
  search: string
  clearSearch: string
  filterLabel: string
  filterUnadded: string
  filterAll: string
  alreadyAdded: string
  unadded: string
  noMatches: string
  empty: string
  selected: (count: number) => string
  selectAll: string
  deselectAll: string
  retry: string
  cancel: string
  confirm: string
}

export function readModelNameConflicts(value: unknown): ModelNameConflict[] {
  if (typeof value !== 'object' || value === null || !('conflicts' in value)) return []
  const conflicts = (value as { conflicts?: unknown }).conflicts
  if (!Array.isArray(conflicts)) return []

  return conflicts.flatMap((item) => {
    if (
      typeof item !== 'object' ||
      item === null ||
      typeof (item as { client_model?: unknown }).client_model !== 'string' ||
      !Array.isArray((item as { indexes?: unknown }).indexes) ||
      !(item as { indexes: unknown[] }).indexes.every(
        (index) => typeof index === 'number' && Number.isSafeInteger(index) && index >= 0,
      )
    ) {
      return []
    }
    return [item as ModelNameConflict]
  })
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

export function indexesWithEmptyAliases(models: readonly GroupModelUpdateDto[]): Set<number> {
  return new Set(
    models.flatMap((model, index) => (model.alias_enabled && !model.alias.trim() ? [index] : [])),
  )
}

export function indexesWithEmptyIDs(models: readonly GroupModelUpdateDto[]): Set<number> {
  return new Set(models.flatMap((model, index) => (!model.id.trim() ? [index] : [])))
}

export function modelDraftValidity(
  models: readonly GroupModelUpdateDto[],
  conflicts: readonly ModelNameConflict[] = findModelNameConflicts(models),
): {
  conflictIndexes: Set<number>
  emptyIDIndexes: Set<number>
  emptyAliasIndexes: Set<number>
  invalidIndexes: Set<number>
} {
  const conflictIndexes = indexesWithConflicts(conflicts)
  const emptyIDIndexes = indexesWithEmptyIDs(models)
  const emptyAliasIndexes = indexesWithEmptyAliases(models)
  return {
    conflictIndexes,
    emptyIDIndexes,
    emptyAliasIndexes,
    invalidIndexes: new Set([...conflictIndexes, ...emptyIDIndexes, ...emptyAliasIndexes]),
  }
}

export function createModelDraft<T extends GroupModelUpdateDto>(
  items: readonly T[],
): Array<T & { key: number }>
export function createModelDraft<T extends GroupModelUpdateDto, K extends ModelDraftKey>(
  items: readonly T[],
  createKey: (item: T, index: number) => K,
): Array<T & { key: K }>
export function createModelDraft<T extends GroupModelUpdateDto, K extends ModelDraftKey>(
  items: readonly T[],
  createKey?: (item: T, index: number) => K,
): Array<T & { key: K | number }> {
  return items.map((item, index) => ({
    ...item,
    key: createKey ? createKey(item, index) : index,
  }))
}

export function normalizedModels(draft: readonly GroupModelUpdateDto[]): GroupModelUpdateDto[] {
  return draft.flatMap((item) => {
    const normalized = normalizeModel(item)
    return normalized === undefined ? [] : [normalized]
  })
}

export function sameModels(
  left: readonly GroupModelUpdateDto[],
  right: readonly GroupModelUpdateDto[],
): boolean {
  return JSON.stringify(normalizedModels(left)) === JSON.stringify(normalizedModels(right))
}
