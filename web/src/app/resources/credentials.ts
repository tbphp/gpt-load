import {
  keepPreviousData,
  queryOptions,
  type QueryClient,
  type QueryKey,
} from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import type {
  CredentialBatchResultDto,
  CredentialCollectionDto,
  CredentialCollectionFilters,
  CredentialConfiguredStatus,
  CredentialItemDto,
  CredentialRecoveryDto,
  CredentialRevealDto,
  CredentialStatus,
  CredentialSummaryDto,
} from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys, normalizeCredentialCollectionFilters } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectEpochMilliseconds,
  projectNullableEpochMilliseconds,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type {
  CredentialBatchResultDto,
  CredentialCollectionDto,
  CredentialCollectionFilters,
  CredentialConfiguredStatus,
  CredentialItemDto,
  CredentialRecoveryDto,
  CredentialRevealDto,
  CredentialStatus,
  CredentialSummaryDto,
  CredentialWeightMode,
} from '@/api/control/types'

export interface CredentialPatch {
  status?: CredentialConfiguredStatus
  weight_manual?: number | null
}

export interface CredentialBatchRequest {
  action: 'enable' | 'disable' | 'delete'
  credential_ids: number[]
}

const credentialCollectionFields = [
  'observed_at_ms',
  'stats_window_seconds',
  'summary',
  'items',
  'pagination',
] as const
const credentialSummaryFields = [
  'total',
  'available',
  'cooldown',
  'blacklisted',
  'disabled',
] as const
const credentialItemFields = [
  'credential_id',
  'mask',
  'configured_status',
  'effective_status',
  'weight_mode',
  'weight',
  'recent_success_count',
  'recent_failure_count',
  'consecutive_failure_count',
  'last_failure_category',
  'last_status_code',
  'cooldown_until_ms',
  'recovery',
] as const
const credentialRecoveryFields = ['mode', 'automatic', 'at_ms'] as const
const credentialPaginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const credentialBatchFields = ['affected_credential_ids', 'summary'] as const
const configuredStatuses = ['active', 'disabled'] as const
const effectiveStatuses = ['available', 'cooldown', 'blacklisted', 'disabled'] as const
const weightModes = ['auto', 'manual'] as const
const recoveryModes = ['none', 'cooldown', 'probe', 'manual'] as const
const failureCategories = [
  'ok',
  'rate_limited',
  'model_unavailable',
  'invalid_key',
  'upstream_host_error',
  'client_error',
  'downstream_cancel',
  'ambiguous',
] as const
const canonicalMask = /^(?:\*{4}|.{4}\*{4}.{4})$/u

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectMask(value: unknown): string {
  const mask = projectString(value)
  if (!canonicalMask.test(mask)) invalidResponse()
  return mask
}

export function projectCredentialSummary(value: unknown): CredentialSummaryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, credentialSummaryFields)
  const result = {
    total: projectSafeInteger(record.total, { minimum: 0 }),
    available: projectSafeInteger(record.available, { minimum: 0 }),
    cooldown: projectSafeInteger(record.cooldown, { minimum: 0 }),
    blacklisted: projectSafeInteger(record.blacklisted, { minimum: 0 }),
    disabled: projectSafeInteger(record.disabled, { minimum: 0 }),
  }
  if (result.total !== result.available + result.cooldown + result.blacklisted + result.disabled) {
    invalidResponse()
  }
  return result
}

function projectRecovery(value: unknown): CredentialRecoveryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, credentialRecoveryFields)
  const result = {
    mode: projectEnum(record.mode, recoveryModes),
    automatic: projectBoolean(record.automatic),
    at_ms: projectNullableEpochMilliseconds(record.at_ms),
  }
  if (
    (result.mode === 'cooldown' && (!result.automatic || result.at_ms === null)) ||
    (result.mode === 'probe' && !result.automatic) ||
    (result.mode === 'manual' && result.automatic) ||
    (result.mode === 'none' && (result.automatic || result.at_ms !== null))
  ) {
    invalidResponse()
  }
  return result
}

export function projectCredentialItem(value: unknown): CredentialItemDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, credentialItemFields)
  const configuredStatus = projectEnum(record.configured_status, configuredStatuses)
  const effectiveStatus = projectEnum(record.effective_status, effectiveStatuses)
  const weightMode = projectEnum(record.weight_mode, weightModes)
  const weight =
    record.weight === null ? null : projectSafeInteger(record.weight, { minimum: 1, maximum: 100 })
  const cooldownUntil = projectNullableEpochMilliseconds(record.cooldown_until_ms)
  const recovery = projectRecovery(record.recovery)
  if (
    // 分组停用或手动权重为 0 时，active 凭据的运行时状态也会是 disabled。
    (configuredStatus === 'disabled' && effectiveStatus !== 'disabled') ||
    (effectiveStatus === 'available') !== (weight !== null) ||
    (effectiveStatus === 'cooldown') !== (cooldownUntil !== null) ||
    (recovery.mode === 'cooldown') !== (effectiveStatus === 'cooldown')
  ) {
    invalidResponse()
  }
  return {
    credential_id: projectSafeInteger(record.credential_id, { minimum: 1 }),
    mask: projectMask(record.mask),
    configured_status: configuredStatus,
    effective_status: effectiveStatus,
    weight_mode: weightMode,
    weight,
    recent_success_count: projectSafeInteger(record.recent_success_count, { minimum: 0 }),
    recent_failure_count: projectSafeInteger(record.recent_failure_count, { minimum: 0 }),
    consecutive_failure_count: projectSafeInteger(record.consecutive_failure_count, { minimum: 0 }),
    last_failure_category: projectEnum(record.last_failure_category, failureCategories),
    last_status_code:
      record.last_status_code === null
        ? null
        : projectSafeInteger(record.last_status_code, { minimum: 100, maximum: 999 }),
    cooldown_until_ms: cooldownUntil,
    recovery,
  }
}

function projectPagination(value: unknown): CredentialCollectionDto['pagination'] {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, credentialPaginationFields)
  const pageSize = projectSafeInteger(record.page_size, { minimum: 20, maximum: 100 })
  if (pageSize !== 20 && pageSize !== 50 && pageSize !== 100) invalidResponse()
  return {
    page: projectSafeInteger(record.page, { minimum: 1 }),
    page_size: pageSize,
    total_items: projectSafeInteger(record.total_items, { minimum: 0 }),
    total_pages: projectSafeInteger(record.total_pages, { minimum: 0 }),
  }
}

function expectedPageCount(totalItems: number, pageSize: number): number {
  return totalItems === 0 ? 0 : Math.ceil(totalItems / pageSize)
}

function expectedPageItems(pagination: CredentialCollectionDto['pagination']): number {
  if (pagination.total_items === 0 || pagination.page > pagination.total_pages) return 0
  if (pagination.page < pagination.total_pages) return pagination.page_size
  const finalPageItems = pagination.total_items % pagination.page_size
  return finalPageItems === 0 ? pagination.page_size : finalPageItems
}

export function projectCredentialCollection(value: unknown): CredentialCollectionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, credentialCollectionFields)
  const items = projectArray(record.items, projectCredentialItem)
  const summary = projectCredentialSummary(record.summary)
  const pagination = projectPagination(record.pagination)
  if (
    pagination.total_items > summary.total ||
    pagination.total_pages !== expectedPageCount(pagination.total_items, pagination.page_size) ||
    items.length !== expectedPageItems(pagination) ||
    new Set(items.map(({ credential_id }) => credential_id)).size !== items.length
  ) {
    invalidResponse()
  }
  return {
    observed_at_ms: projectEpochMilliseconds(record.observed_at_ms),
    stats_window_seconds: projectSafeInteger(record.stats_window_seconds, { minimum: 1 }),
    summary,
    items,
    pagination,
  }
}

function normalizePatch(patch: CredentialPatch): CredentialPatch {
  const keys = Object.keys(patch)
  if (keys.length === 0 || keys.some((key) => key !== 'status' && key !== 'weight_manual')) {
    throw new Error('INVALID_CREDENTIAL_PATCH')
  }
  const body: CredentialPatch = {}
  if (Object.prototype.hasOwnProperty.call(patch, 'status')) {
    body.status = projectEnum(patch.status, configuredStatuses)
  }
  if (Object.prototype.hasOwnProperty.call(patch, 'weight_manual')) {
    const weight = patch.weight_manual
    if (
      weight === undefined ||
      (weight !== null && (!Number.isInteger(weight) || weight < 1 || weight > 100))
    ) {
      throw new Error('INVALID_CREDENTIAL_WEIGHT')
    }
    body.weight_manual = weight
  }
  return body
}

function credentialCollectionURL(
  groupId: number,
  filters: CredentialCollectionFilters,
): `/api/${string}` {
  const normalized = normalizeCredentialCollectionFilters(filters)
  const params = new URLSearchParams({
    page: String(normalized.page),
    page_size: String(normalized.page_size),
  })
  if (normalized.q !== undefined) params.set('q', normalized.q)
  if (normalized.status !== undefined) params.set('status', normalized.status)
  return `/api/groups/${groupId}/credentials?${params.toString()}`
}

export async function getCredentialCollection(
  client: ApiClient,
  groupId: number,
  filters: CredentialCollectionFilters,
  signal?: AbortSignal,
): Promise<CredentialCollectionDto> {
  const normalized = normalizeCredentialCollectionFilters(filters)
  const result = projectCredentialCollection(
    await client.request(credentialCollectionURL(groupId, normalized), { method: 'GET', signal }),
  )
  if (
    result.pagination.page !== normalized.page ||
    result.pagination.page_size !== normalized.page_size
  ) {
    invalidResponse()
  }
  return result
}

const manualGroupQueryOptions = {
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
} as const

export function credentialCollectionQueryOptions(
  client: ApiClient,
  groupID: MaybeRefOrGetter<number>,
  filters: MaybeRefOrGetter<CredentialCollectionFilters>,
) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: computed(() =>
      controlQueryKeys.groups.credentials(toValue(groupID), toValue(filters)),
    ),
    queryFn: ({ queryKey, signal }) =>
      getCredentialCollection(client, queryKey[3], queryKey[5], signal),
    placeholderData: keepPreviousData,
  })
}

export async function updateCredential(
  client: ApiClient,
  groupId: number,
  credentialId: number,
  patch: CredentialPatch,
  signal?: AbortSignal,
): Promise<CredentialItemDto> {
  return projectCredentialItem(
    await client.request(`/api/groups/${groupId}/credentials/${credentialId}`, {
      method: 'PUT',
      json: normalizePatch(patch),
      signal,
    }),
  )
}

export async function revealCredential(
  client: ApiClient,
  groupId: number,
  credentialId: number,
  signal?: AbortSignal,
): Promise<CredentialRevealDto> {
  const record = projectRecord(
    await client.request(`/api/groups/${groupId}/credentials/${credentialId}/reveal`, {
      method: 'POST',
      signal,
    }),
  )
  assertNoSecretLikeFields(record, ['credential_id', 'credential', 'revealed_at_ms'])
  if (projectSafeInteger(record.credential_id, { minimum: 1 }) !== credentialId) invalidResponse()
  const credentialRecord = projectRecord(record.credential)
  const credential: Record<string, string> = {}
  for (const [key, value] of Object.entries(credentialRecord)) {
    if (key !== key.trim() || !/^[a-z][a-z0-9_]*$/u.test(key)) invalidResponse()
    credential[key] = projectString(value)
  }
  if (Object.keys(credential).length === 0) invalidResponse()
  return {
    credential_id: credentialId,
    credential,
    revealed_at_ms: projectEpochMilliseconds(record.revealed_at_ms),
  }
}

export async function restoreCredential(
  client: ApiClient,
  groupId: number,
  credentialId: number,
  signal?: AbortSignal,
): Promise<CredentialItemDto> {
  return projectCredentialItem(
    await client.request(`/api/groups/${groupId}/credentials/${credentialId}/restore`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
}

export async function batchCredentials(
  client: ApiClient,
  groupId: number,
  body: CredentialBatchRequest,
  signal?: AbortSignal,
): Promise<CredentialBatchResultDto> {
  const ids = body.credential_ids
  if (
    !['enable', 'disable', 'delete'].includes(body.action) ||
    ids.length < 1 ||
    ids.length > 100 ||
    ids.some((id) => !Number.isSafeInteger(id) || id < 1) ||
    new Set(ids).size !== ids.length
  ) {
    throw new Error('INVALID_CREDENTIAL_BATCH')
  }
  const record = projectRecord(
    await client.request(`/api/groups/${groupId}/credentials/batch`, {
      method: 'POST',
      json: body,
      signal,
    }),
  )
  assertNoSecretLikeFields(record, credentialBatchFields)
  const affectedCredentialIDs = projectArray(record.affected_credential_ids, (id) =>
    projectSafeInteger(id, { minimum: 1 }),
  )
  if (
    new Set(affectedCredentialIDs).size !== affectedCredentialIDs.length ||
    affectedCredentialIDs.length !== ids.length
  )
    invalidResponse()
  return {
    affected_credential_ids: affectedCredentialIDs,
    summary: projectCredentialSummary(record.summary),
  }
}

export async function deleteCredential(
  client: ApiClient,
  groupId: number,
  credentialId: number,
  signal?: AbortSignal,
): Promise<void> {
  await client.request(`/api/groups/${groupId}/credentials/${credentialId}`, {
    method: 'DELETE',
    signal,
  })
}

function queryFilters(queryKey: QueryKey): CredentialCollectionFilters | undefined {
  const filters = queryKey[5]
  if (typeof filters !== 'object' || filters === null || Array.isArray(filters)) return undefined
  const record = filters as Record<string, unknown>
  if (
    !Number.isSafeInteger(record.page) ||
    (record.page_size !== 20 && record.page_size !== 50 && record.page_size !== 100) ||
    (record.q !== undefined && typeof record.q !== 'string') ||
    (record.status !== undefined && !effectiveStatuses.includes(record.status as CredentialStatus))
  ) {
    return undefined
  }
  return {
    page: record.page as number,
    page_size: record.page_size as 20 | 50 | 100,
    ...(record.q === undefined ? {} : { q: record.q }),
    ...(record.status === undefined ? {} : { status: record.status as CredentialStatus }),
  }
}

function matchesFilters(item: CredentialItemDto, filters: CredentialCollectionFilters): boolean {
  if (filters.status !== undefined && item.effective_status !== filters.status) return false
  return filters.q === undefined || item.mask.toLowerCase().includes(filters.q.toLowerCase())
}

function withSummaryDelta(
  summary: CredentialSummaryDto,
  previous: CredentialItemDto,
  next: CredentialItemDto | undefined,
): CredentialSummaryDto {
  const result = { ...summary }
  result[previous.effective_status]--
  if (next !== undefined) result[next.effective_status]++
  return result
}

function withRemovedItem(
  collection: CredentialCollectionDto,
  id: number,
  summary: CredentialSummaryDto,
): CredentialCollectionDto {
  const items = collection.items.filter((item) => item.credential_id !== id)
  const totalItems = collection.pagination.total_items - 1
  return {
    ...collection,
    summary,
    items,
    pagination: {
      ...collection.pagination,
      total_items: totalItems,
      total_pages: expectedPageCount(totalItems, collection.pagination.page_size),
    },
  }
}

async function invalidateExactCredentialPage(
  queryClient: QueryClient,
  queryKey: QueryKey,
): Promise<void> {
  await queryClient.invalidateQueries({ queryKey, exact: true, refetchType: 'none' })
}

interface MaterializedCredentialPage {
  queryKey: QueryKey
  collection: CredentialCollectionDto
  filters: CredentialCollectionFilters
}

function credentialFilterSetID(filters: CredentialCollectionFilters): string {
  return JSON.stringify({
    q: filters.q ?? null,
    status: filters.status ?? null,
    page_size: filters.page_size,
  })
}

function totalItemsAfterBatchDelete(
  pages: MaterializedCredentialPage[],
  knownDeletedIDs: Set<number>,
  summary: CredentialSummaryDto,
): number {
  const { q, status } = pages[0].filters
  if (q === undefined) return status === undefined ? summary.total : summary[status]
  return Math.max(
    0,
    Math.max(...pages.map(({ collection }) => collection.pagination.total_items)) -
      knownDeletedIDs.size,
  )
}

/**
 * Reconciles only a cached page containing the previous item. Pages whose
 * membership or sort position cannot be known are explicitly marked stale.
 */
export async function cacheCredentialItem(
  queryClient: QueryClient,
  groupId: number,
  item: CredentialItemDto,
): Promise<void> {
  const queries = queryClient
    .getQueryCache()
    .findAll({ queryKey: controlQueryKeys.groups.credentialsAll(groupId) })
  const previous = queries
    .map((query) => (query.state.data as CredentialCollectionDto | undefined)?.items)
    .flatMap((items) => items ?? [])
    .find(({ credential_id }) => credential_id === item.credential_id)
  for (const query of queries) {
    const filters = queryFilters(query.queryKey)
    const collection = query.state.data as CredentialCollectionDto | undefined
    const current = collection?.items.find(
      ({ credential_id }) => credential_id === item.credential_id,
    )
    if (collection === undefined || previous === undefined) {
      await invalidateExactCredentialPage(queryClient, query.queryKey)
      continue
    }
    const summary = withSummaryDelta(collection.summary, previous, item)
    if (filters === undefined || current === undefined) {
      queryClient.setQueryData<CredentialCollectionDto>(query.queryKey, { ...collection, summary })
      await invalidateExactCredentialPage(queryClient, query.queryKey)
      continue
    }
    const nextMatches = matchesFilters(item, filters)
    if (!nextMatches) {
      queryClient.setQueryData(
        query.queryKey,
        withRemovedItem(collection, item.credential_id, summary),
      )
      await invalidateExactCredentialPage(queryClient, query.queryKey)
      continue
    }
    if (current.effective_status !== item.effective_status) {
      queryClient.setQueryData<CredentialCollectionDto>(query.queryKey, { ...collection, summary })
      await invalidateExactCredentialPage(queryClient, query.queryKey)
      continue
    }
    queryClient.setQueryData<CredentialCollectionDto>(query.queryKey, {
      ...collection,
      summary,
      items: collection.items.map((current) =>
        current.credential_id === item.credential_id ? item : current,
      ),
    })
  }
}

/** Marks only this Group's credential pages stale without scheduling an automatic request. */
export async function invalidateCredentialCollections(
  queryClient: QueryClient,
  groupId: number,
): Promise<void> {
  await queryClient.invalidateQueries({
    queryKey: controlQueryKeys.groups.credentialsAll(groupId),
    refetchType: 'none',
  })
}

/** A batch result carries the authoritative aggregate, so cached pages can be reconciled deterministically. */
export async function cacheCredentialBatch(
  queryClient: QueryClient,
  groupId: number,
  action: CredentialBatchRequest['action'],
  result: CredentialBatchResultDto,
): Promise<void> {
  const affected = new Set(result.affected_credential_ids)
  const queries = queryClient
    .getQueryCache()
    .findAll({ queryKey: controlQueryKeys.groups.credentialsAll(groupId) })
  const pages = queries.flatMap((query): MaterializedCredentialPage[] => {
    const collection = query.state.data as CredentialCollectionDto | undefined
    const filters = queryFilters(query.queryKey)
    return collection === undefined || filters === undefined
      ? []
      : [{ queryKey: query.queryKey, collection, filters }]
  })
  if (action !== 'delete') {
    for (const { queryKey, collection } of pages) {
      queryClient.setQueryData<CredentialCollectionDto>(queryKey, {
        ...collection,
        summary: result.summary,
      })
      await invalidateExactCredentialPage(queryClient, queryKey)
    }
    return
  }

  const pageSets = new Map<string, MaterializedCredentialPage[]>()
  for (const page of pages) {
    const id = credentialFilterSetID(page.filters)
    const set = pageSets.get(id)
    if (set === undefined) pageSets.set(id, [page])
    else set.push(page)
  }
  for (const pageSet of pageSets.values()) {
    const knownDeletedIDs = new Set(
      pageSet
        .flatMap(({ collection }) => collection.items)
        .filter(({ credential_id }) => affected.has(credential_id))
        .map(({ credential_id }) => credential_id),
    )
    const totalItems = totalItemsAfterBatchDelete(pageSet, knownDeletedIDs, result.summary)
    const totalPages = expectedPageCount(totalItems, pageSet[0].filters.page_size)
    for (const { queryKey, collection } of pageSet) {
      queryClient.setQueryData<CredentialCollectionDto>(queryKey, {
        ...collection,
        summary: result.summary,
        items: collection.items.filter(({ credential_id }) => !affected.has(credential_id)),
        pagination: {
          ...collection.pagination,
          total_items: totalItems,
          total_pages: totalPages,
        },
      })
      await invalidateExactCredentialPage(queryClient, queryKey)
    }
  }
}
