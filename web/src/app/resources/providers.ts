import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { GroupProtocol, ModelPricingStatus } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectEpochMilliseconds,
  projectHTTPURL,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type ProviderSuggestionSource = 'official' | 'curated' | 'catalog'

export interface ProviderSuggestion {
  provider_id: string
  name: string
  api_url: string | null
  protocols: GroupProtocol[]
  mark: string
  source: ProviderSuggestionSource
}

export interface ProviderSuggestionList {
  items: ProviderSuggestion[]
  total: number
}

export type ModelCandidateSource = 'catalog' | 'live'

export interface ModelCandidate {
  id: string
  name: string
  sources: ModelCandidateSource[]
  pricing_status: ModelPricingStatus
  pricing_source: string | null
}

export interface ProviderModelFilters {
  q?: string
  status?: ModelPricingStatus
}

export interface ProviderModelList {
  items: ModelCandidate[]
  total: number
}

export interface CatalogSyncStatus {
  trigger: 'startup' | 'periodic' | 'group_change' | 'manual'
  checked_at_ms: number
  successful_fetch_at_ms: number
  not_modified: boolean
  skipped: boolean
  error_code: string | null
}

const providerSuggestionFields = [
  'provider_id',
  'name',
  'api_url',
  'protocols',
  'mark',
  'source',
] as const
const providerSuggestionSources = ['official', 'curated', 'catalog'] as const
const providerSuggestionListFields = ['items', 'total'] as const
const modelCandidateFields = ['id', 'name', 'sources', 'pricing_status', 'pricing_source'] as const
const providerModelListFields = ['items', 'total'] as const
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

export function projectProviderID(value: unknown): string {
  const providerID = projectString(value)
  if (
    providerID !== providerID.trim() ||
    providerID.includes(':') ||
    /[\u0000-\u001f\u007f]/u.test(providerID)
  ) {
    invalidResponse()
  }
  return providerID
}

function projectProtocols(value: unknown): GroupProtocol[] {
  const protocols = projectArray(value, (protocol) => projectEnum(protocol, enabledDataProtocols))
  if (new Set(protocols).size !== protocols.length) invalidResponse()
  return protocols
}

function projectProviderSuggestion(value: unknown): ProviderSuggestion {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, providerSuggestionFields)
  return {
    provider_id: projectProviderID(record.provider_id),
    name: projectString(record.name),
    api_url: record.api_url === undefined ? null : projectHTTPURL(record.api_url),
    protocols: projectProtocols(record.protocols),
    mark: projectString(record.mark, { allowEmpty: true }),
    source: projectEnum(record.source, providerSuggestionSources),
  }
}

export function projectProviderSuggestionList(value: unknown): ProviderSuggestionList {
  return projectProviderSuggestionListValue(value, true)
}

function projectProviderSuggestionListByIDs(value: unknown): ProviderSuggestionList {
  const result = projectProviderSuggestionListValue(value, false)
  if (result.items.some(({ source }) => source !== 'catalog')) invalidResponse()
  return result
}

function projectProviderSuggestionListValue(
  value: unknown,
  requireOfficialSuggestions: boolean,
): ProviderSuggestionList {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, providerSuggestionListFields)
  const items = projectArray(record.items, projectProviderSuggestion)
  const total = projectSafeInteger(record.total, { minimum: 0 })
  if (
    total !== items.length ||
    new Set(items.map(({ provider_id }) => provider_id)).size !== items.length ||
    (requireOfficialSuggestions && items.filter(({ source }) => source === 'official').length !== 3)
  ) {
    invalidResponse()
  }
  return { items, total }
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

function projectProviderModelList(value: unknown): ProviderModelList {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, providerModelListFields)
  const items = projectArray(record.items, projectModelCandidate)
  const total = projectSafeInteger(record.total, { minimum: 0 })
  if (items.length > total || new Set(items.map(({ id }) => id)).size !== items.length) {
    invalidResponse()
  }
  return { items, total }
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

export function normalizeProviderSearch(value: string): string {
  return value.trim().slice(0, 200)
}

export function normalizeProviderModelFilters(filters: ProviderModelFilters): ProviderModelFilters {
  const normalized: ProviderModelFilters = {}
  const query = normalizeProviderSearch(filters.q ?? '')
  if (query) normalized.q = query
  if (filters.status !== undefined) normalized.status = filters.status
  return normalized
}

export async function listProviderSuggestions(
  client: ApiClient,
  search: string,
  signal?: AbortSignal,
): Promise<ProviderSuggestionList> {
  const query = normalizeProviderSearch(search)
  const params = new URLSearchParams()
  if (query) params.set('q', query)
  const suffix = params.size ? `?${params.toString()}` : ''
  return projectProviderSuggestionList(
    await client.request(`/api/provider-suggestions${suffix}`, { method: 'GET', signal }),
  )
}

export async function listProviderSuggestionsByIDs(
  client: ApiClient,
  providerIDs: readonly string[],
  signal?: AbortSignal,
): Promise<ProviderSuggestionList> {
  const normalizedIDs = normalizeProviderIDs(providerIDs)
  if (!normalizedIDs.length) return { items: [], total: 0 }
  const params = new URLSearchParams()
  params.set('provider_ids', normalizedIDs.join(','))
  return projectProviderSuggestionListByIDs(
    await client.request(`/api/provider-suggestions?${params.toString()}`, {
      method: 'GET',
      signal,
    }),
  )
}

function normalizeProviderIDs(providerIDs: readonly string[]): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const providerID of providerIDs) {
    const normalized = providerID.trim()
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    result.push(normalized)
  }
  return result
}

export async function listProviderModels(
  client: ApiClient,
  providerID: string,
  filters: ProviderModelFilters,
  signal?: AbortSignal,
): Promise<ProviderModelList> {
  const canonicalProviderID = projectProviderID(providerID)
  const normalized = normalizeProviderModelFilters(filters)
  const params = new URLSearchParams()
  if (normalized.q !== undefined) params.set('q', normalized.q)
  if (normalized.status !== undefined) params.set('status', normalized.status)
  const suffix = params.size ? `?${params.toString()}` : ''
  return projectProviderModelList(
    await client.request(
      `/api/providers/${encodeURIComponent(canonicalProviderID)}/models${suffix}`,
      { method: 'GET', signal },
    ),
  )
}

export async function syncModelPrices(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<CatalogSyncStatus> {
  return projectCatalogSyncStatus(
    await client.request('/api/model-prices/sync', { method: 'POST', signal }),
  )
}

export function providerSuggestionsQueryOptions(
  client: ApiClient,
  search: MaybeRefOrGetter<string>,
) {
  return queryOptions({
    queryKey: computed(() =>
      controlQueryKeys.providers.suggestions(normalizeProviderSearch(toValue(search))),
    ),
    queryFn: ({ queryKey, signal }) => listProviderSuggestions(client, queryKey[3], signal),
    staleTime: 5 * 60 * 1_000,
    placeholderData: keepPreviousData,
  })
}

export function providerSuggestionsByIDsQueryOptions(
  client: ApiClient,
  providerIDs: MaybeRefOrGetter<readonly string[]>,
) {
  return queryOptions({
    queryKey: computed(() => {
      const normalizedIDs = normalizeProviderIDs(toValue(providerIDs))
      return controlQueryKeys.providers.suggestionsByIDs(normalizedIDs)
    }),
    queryFn: ({ queryKey, signal }) => listProviderSuggestionsByIDs(client, queryKey[3], signal),
    enabled: computed(() => normalizeProviderIDs(toValue(providerIDs)).length > 0),
    staleTime: 5 * 60 * 1_000,
    placeholderData: keepPreviousData,
  })
}

export function providerModelsQueryOptions(
  client: ApiClient,
  providerID: MaybeRefOrGetter<string | null>,
  filters: MaybeRefOrGetter<ProviderModelFilters>,
) {
  return queryOptions({
    queryKey: computed(() => {
      const id = toValue(providerID)
      return id === null
        ? controlQueryKeys.providers.modelsAll()
        : controlQueryKeys.providers.models(id, normalizeProviderModelFilters(toValue(filters)))
    }),
    queryFn: ({ signal }) => {
      const id = toValue(providerID)
      if (id === null) invalidResponse()
      return listProviderModels(client, id, toValue(filters), signal)
    },
    enabled: computed(() => toValue(providerID) !== null),
    staleTime: 5 * 60 * 1_000,
    placeholderData: keepPreviousData,
  })
}
