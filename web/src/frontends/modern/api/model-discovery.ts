import { InvalidResponseError } from '@shared/http/errors'
import { list, oneOf, record, text } from './response'

export type ModelSource = 'catalog' | 'live'
export type ModelPricingStatus = 'pending' | 'configured'
export interface ModelCandidate {
  id: string
  name: string
  sources: ModelSource[]
  pricingStatus: ModelPricingStatus
  pricingSource: string | null
}

export function readModelCandidates(value: unknown): ModelCandidate[] {
  const candidates = list(value).map((raw): ModelCandidate => {
    const model = record(raw)
    const id = text(model.id)
    const name = text(model.name)
    const sources = list(model.sources).map((source) => oneOf(source, ['catalog', 'live'] as const))
    const pricingStatus = oneOf(model.pricing_status, ['pending', 'configured'])
    const pricingSource = model.pricing_source === null ? null : text(model.pricing_source)
    if (
      !id ||
      id !== id.trim() ||
      !name.trim() ||
      !sources.length ||
      new Set(sources).size !== sources.length ||
      (pricingSource !== null && (!pricingSource || pricingSource !== pricingSource.trim())) ||
      (pricingStatus === 'pending' && pricingSource !== null)
    )
      throw new InvalidResponseError()
    return { id, name, sources, pricingStatus, pricingSource }
  })
  if (new Set(candidates.map((item) => item.id)).size !== candidates.length)
    throw new InvalidResponseError()
  return candidates
}
