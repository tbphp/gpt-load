import type { ApiClient } from '@/api/client'
import type { ModelPricingStatus } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectEpochMilliseconds,
  projectRecord,
  projectString,
} from './projector'

export type ModelCandidateSource = 'catalog' | 'live'

export interface ModelCandidate {
  id: string
  name: string
  sources: ModelCandidateSource[]
  pricing_status: ModelPricingStatus
  pricing_source: string | null
}

export interface CatalogSyncStatus {
  trigger: 'startup' | 'periodic' | 'group_change' | 'manual'
  checked_at_ms: number
  successful_fetch_at_ms: number
  not_modified: boolean
  skipped: boolean
  error_code: string | null
}

const modelCandidateFields = ['id', 'name', 'sources', 'pricing_status', 'pricing_source'] as const
const catalogSyncStatusFields = [
  'trigger',
  'checked_at_ms',
  'successful_fetch_at_ms',
  'not_modified',
  'skipped',
  'error_code',
] as const
const modelCandidateSources = ['catalog', 'live'] as const
const pricingStatuses = ['pending', 'configured'] as const
const catalogSyncTriggers = ['startup', 'periodic', 'group_change', 'manual'] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

export function projectModelCandidate(value: unknown): ModelCandidate {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, modelCandidateFields)
  const id = projectString(record.id)
  const name = projectString(record.name)
  const sources = projectArray(record.sources, (source) =>
    projectEnum(source, modelCandidateSources),
  )
  const pricingStatus = projectEnum(record.pricing_status, pricingStatuses)
  const pricingSource = record.pricing_source === null ? null : projectString(record.pricing_source)
  if (
    id !== id.trim() ||
    name.trim().length === 0 ||
    sources.length === 0 ||
    sources.length > modelCandidateSources.length ||
    new Set(sources).size !== sources.length ||
    (pricingSource !== null && pricingSource !== pricingSource.trim()) ||
    (pricingStatus === 'pending' && pricingSource !== null)
  ) {
    invalidResponse()
  }
  return {
    id,
    name,
    sources,
    pricing_status: pricingStatus,
    pricing_source: pricingSource,
  }
}

function projectCatalogSyncStatus(value: unknown): CatalogSyncStatus {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, catalogSyncStatusFields)
  return {
    trigger: projectEnum(record.trigger, catalogSyncTriggers),
    checked_at_ms: projectEpochMilliseconds(record.checked_at_ms),
    successful_fetch_at_ms: projectEpochMilliseconds(record.successful_fetch_at_ms),
    not_modified: projectBoolean(record.not_modified),
    skipped: projectBoolean(record.skipped),
    error_code:
      record.error_code === undefined
        ? null
        : projectString(record.error_code, { allowEmpty: false }),
  }
}

export async function syncModelPrices(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<CatalogSyncStatus> {
  return projectCatalogSyncStatus(
    await client.request('/api/model-prices/sync', { method: 'POST', signal }),
  )
}
