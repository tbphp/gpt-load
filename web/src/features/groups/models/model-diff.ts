import type { GroupModelDto } from '@/api/control/types'

export type ModelDiffOrigin = 'persisted' | 'discovered' | 'manual'

export interface ModelDiffItem extends GroupModelDto {
  origin: ModelDiffOrigin
  rediscovered: boolean
  selected: boolean
}

function normalizeModel(model: GroupModelDto): GroupModelDto | undefined {
  const id = model.id.trim()
  if (!id) return undefined
  return { id, alias: model.alias.trim() }
}

function pairKey(model: GroupModelDto): string {
  return JSON.stringify([model.id, model.alias])
}

export function buildModelDiff(saved: GroupModelDto[], discoveredIDs: string[]): ModelDiffItem[] {
  const discovered = new Set<string>()
  for (const rawID of discoveredIDs) {
    const id = rawID.trim()
    if (id) discovered.add(id)
  }

  const result: ModelDiffItem[] = []
  const savedIDs = new Set<string>()
  for (const value of saved) {
    const model = normalizeModel(value)
    if (!model) continue
    savedIDs.add(model.id)
    result.push({
      ...model,
      origin: 'persisted',
      rediscovered: discovered.has(model.id),
      selected: true,
    })
  }
  for (const id of discovered) {
    if (savedIDs.has(id)) continue
    result.push({ id, alias: '', origin: 'discovered', rediscovered: true, selected: true })
  }
  return result
}

export function normalizeSelectedModels(draft: readonly ModelDiffItem[]): GroupModelDto[] {
  const result: GroupModelDto[] = []
  const seen = new Set<string>()
  for (const value of draft) {
    if (!value.selected) continue
    const model = normalizeModel(value)
    if (!model) continue
    const key = pairKey(model)
    if (seen.has(key)) continue
    seen.add(key)
    result.push(model)
  }
  return result
}

export function sameNormalizedModels(
  saved: readonly GroupModelDto[],
  draft: readonly ModelDiffItem[],
): boolean {
  const normalizedSaved = saved.flatMap((model) => {
    const normalized = normalizeModel(model)
    return normalized ? [normalized] : []
  })
  const normalizedDraft = normalizeSelectedModels(draft)
  if (normalizedSaved.length !== normalizedDraft.length) return false
  return normalizedSaved.every(
    (model, index) => pairKey(model) === pairKey(normalizedDraft[index]!),
  )
}

export function hasModelRemovals(
  saved: readonly GroupModelDto[],
  draft: readonly ModelDiffItem[],
): boolean {
  const selected = new Set(normalizeSelectedModels(draft).map(pairKey))
  return saved.some((value) => {
    const model = normalizeModel(value)
    return model !== undefined && !selected.has(pairKey(model))
  })
}
