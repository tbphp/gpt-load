import type { GroupProtocol } from '@/api/control/types'
import type { GroupModelUpdateDto } from '@/app/resources/groups'
import { normalizedModels, type ModelDraftValue } from '@/features/models/model-draft'

import type { ProviderPresetID } from './channel-presets'

export type ImportModelSource = 'manual' | 'discovered'

export interface ModelDraftItem extends ModelDraftValue {
  source: ImportModelSource
  key: number
}

export interface ImportDraft {
  mode: 'new'
  preset_id: ProviderPresetID
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
  ids: readonly string[],
  nextKey: () => number,
): ModelDraftItem[] {
  const seen = new Set<string>()
  return ids.flatMap((value) => {
    const id = value.trim()
    if (!id || seen.has(id)) return []
    seen.add(id)
    return [
      {
        id,
        alias: '',
        alias_enabled: false,
        source: 'discovered' as const,
        key: nextKey(),
      },
    ]
  })
}

export function toGroupModels(draft: readonly ModelDraftItem[]): GroupModelUpdateDto[] {
  return normalizedModels(draft)
}
