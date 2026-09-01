import type { GroupModelItemDto } from '@/api/control/types'
import type { ModelCandidate } from '@/app/resources/providers'
import {
  findModelNameConflicts,
  normalizedModels as normalizeSharedModels,
  sameModels as sameSharedModels,
  type ModelDraftValue,
  type ModelNameConflict,
} from '@/features/models/model-draft'

export interface ModelDraftItem extends ModelDraftValue {
  key: number
}

export type ModelSyncMode = 'cleanup' | 'add' | 'full'

export interface ModelSyncDiff {
  additions: ModelCandidate[]
  removals: ModelDraftItem[]
}

export type { ModelNameConflict }
export { findModelNameConflicts }

export function createModelDraft(items: readonly GroupModelItemDto[]): ModelDraftItem[] {
  return items.map((item, index) => ({
    id: item.id,
    name: item.id,
    sources: [],
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

export function createModelSyncDiff(
  current: readonly ModelDraftItem[],
  candidates: readonly ModelCandidate[],
): ModelSyncDiff {
  const liveCandidates = candidates.filter(({ sources }) => sources.includes('live'))
  const liveIDs = new Set(liveCandidates.map(({ id }) => id))
  const currentIDs = new Set(current.map(({ id }) => id.trim()).filter(Boolean))
  return {
    additions: liveCandidates.filter(({ id }) => !currentIDs.has(id)),
    removals: current.filter(({ id }) => !liveIDs.has(id.trim())),
  }
}

export function syncedModels(
  current: readonly ModelDraftItem[],
  diff: ModelSyncDiff,
  mode: ModelSyncMode,
) {
  const removalKeys = new Set(diff.removals.map(({ key }) => key))
  const retained = mode === 'add' ? current : current.filter(({ key }) => !removalKeys.has(key))
  const additions =
    mode === 'cleanup'
      ? []
      : diff.additions.map(({ id }) => ({ id, alias: '', alias_enabled: false }))
  return normalizeSharedModels([...retained, ...additions])
}
