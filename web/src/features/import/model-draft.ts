import type { GroupProtocol } from '@/api/control/types'
import type { GroupModelUpdateDto } from '@/app/resources/groups'
import type { ModelCandidate } from '@/app/resources/providers'
import { normalizedModels, type ModelDraftValue } from '@/features/models/model-draft'

export interface ModelDraftItem extends ModelDraftValue {
  key: number
}

export interface ImportDraft {
  mode: 'new'
  provider_id: string | null
  name: string
  upstream_url: string
  protocols: GroupProtocol[]
  keys: string
  models: ModelDraftItem[]
}

export interface ExistingGroupImportDraft {
  mode: 'existing'
  group_id: number | null
  keys: string
}

export type ImportRecoveryDraft = ImportDraft | ExistingGroupImportDraft

export function createDiscoveredModelDraft(
  candidates: readonly ModelCandidate[],
  nextKey: () => number,
): ModelDraftItem[] {
  const seen = new Set<string>()
  return candidates.flatMap((candidate) => {
    const id = candidate.id.trim()
    if (!id || seen.has(id)) return []
    seen.add(id)
    return [
      {
        id,
        name: candidate.name,
        sources: [...candidate.sources],
        pricing_status: candidate.pricing_status,
        alias: '',
        alias_enabled: false,
        key: nextKey(),
      },
    ]
  })
}

export function toGroupModels(draft: readonly ModelDraftItem[]): GroupModelUpdateDto[] {
  return normalizedModels(draft)
}
