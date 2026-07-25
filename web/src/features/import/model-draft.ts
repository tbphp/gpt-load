import type { GroupModelDto, Protocol } from '@/api/control/types'

import type { ChannelPreset } from './channel-presets'

export interface HeaderRules {
  set: Record<string, string>
  remove: string[]
}

export interface ModelDraftItem {
  id: string
  alias: string
  selected: boolean
}

export interface ImportDraft {
  mode: 'new'
  step: 1 | 2 | 3
  preset_id: ChannelPreset['id']
  name: string
  upstream_url: string
  protocols: Protocol[]
  keys: string
  header_rules: HeaderRules
  models: ModelDraftItem[]
}

export function createModelDraft(ids: string[]): ModelDraftItem[] {
  const seen = new Set<string>()
  const result: ModelDraftItem[] = []
  for (const value of ids) {
    const id = value.trim()
    if (!id || seen.has(id)) continue
    seen.add(id)
    result.push({ id, alias: '', selected: true })
  }
  return result
}

export function setManualModel(
  draft: ModelDraftItem[],
  rawID: string,
  rawAlias: string,
): ModelDraftItem[] {
  const id = rawID.trim()
  const alias = rawAlias.trim()
  if (!id) return draft
  const existing = draft.find((model) => model.id === id)
  if (existing) {
    return draft.map((model) =>
      model.id === id ? { ...model, alias, selected: true } : { ...model },
    )
  }
  return [...draft.map((model) => ({ ...model })), { id, alias, selected: true }]
}

export function toGroupModels(draft: ModelDraftItem[]): GroupModelDto[] {
  return draft
    .filter((model) => model.selected && model.id.trim() !== '')
    .map((model) => ({ id: model.id.trim(), alias: model.alias.trim() }))
}
