import { queryOptions, type QueryClient, type QueryKey } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import type {
  GroupKeyBatchResultDto,
  GroupKeyCollectionDto,
  GroupKeyCollectionFilters,
  GroupKeyConfiguredStatus,
  GroupKeyItemDto,
  GroupKeyRecoveryDto,
  GroupKeyStatus,
  GroupKeySummaryDto,
} from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys, normalizeGroupKeyCollectionFilters } from '@/app/query-keys'

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
  GroupKeyBatchResultDto,
  GroupKeyCollectionDto,
  GroupKeyCollectionFilters,
  GroupKeyConfiguredStatus,
  GroupKeyItemDto,
  GroupKeyRecoveryDto,
  GroupKeyStatus,
  GroupKeySummaryDto,
  GroupKeyWeightMode,
} from '@/api/control/types'

export type UpstreamKeyStatus = GroupKeyConfiguredStatus
export type UpstreamKeyEffectiveStatus = GroupKeyStatus

export interface UpstreamKeyPatch {
  status?: UpstreamKeyStatus
  weight_manual?: number | null
}

export interface GroupKeyBatchRequest {
  action: 'enable' | 'disable' | 'delete'
  key_ids: number[]
}

const groupKeyCollectionFields = [
  'observed_at_ms',
  'stats_window_seconds',
  'summary',
  'items',
  'pagination',
] as const
const groupKeySummaryFields = ['total', 'available', 'cooldown', 'blacklisted', 'disabled'] as const
const groupKeyItemFields = [
  'id',
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
const groupKeyRecoveryFields = ['mode', 'automatic', 'at_ms'] as const
const groupKeyPaginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const groupKeyBatchFields = ['affected_ids', 'summary'] as const
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

export function projectGroupKeySummary(value: unknown): GroupKeySummaryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupKeySummaryFields)
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

function projectRecovery(value: unknown): GroupKeyRecoveryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupKeyRecoveryFields)
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

export function projectGroupKeyItem(value: unknown): GroupKeyItemDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupKeyItemFields)
  const configuredStatus = projectEnum(record.configured_status, configuredStatuses)
  const effectiveStatus = projectEnum(record.effective_status, effectiveStatuses)
  const weightMode = projectEnum(record.weight_mode, weightModes)
  const weight =
    record.weight === null ? null : projectSafeInteger(record.weight, { minimum: 1, maximum: 100 })
  const cooldownUntil = projectNullableEpochMilliseconds(record.cooldown_until_ms)
  const recovery = projectRecovery(record.recovery)
  if (
    (configuredStatus === 'disabled') !== (effectiveStatus === 'disabled') ||
    (effectiveStatus === 'available') !== (weight !== null) ||
    (effectiveStatus === 'cooldown') !== (cooldownUntil !== null) ||
    (recovery.mode === 'cooldown') !== (effectiveStatus === 'cooldown')
  ) {
    invalidResponse()
  }
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
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
        : projectSafeInteger(record.last_status_code, { minimum: 100, maximum: 599 }),
    cooldown_until_ms: cooldownUntil,
    recovery,
  }
}

function projectPagination(value: unknown): GroupKeyCollectionDto['pagination'] {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupKeyPaginationFields)
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

export function projectGroupKeyCollection(value: unknown): GroupKeyCollectionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupKeyCollectionFields)
  const items = projectArray(record.items, projectGroupKeyItem)
  const summary = projectGroupKeySummary(record.summary)
  const pagination = projectPagination(record.pagination)
  if (
    pagination.total_items > summary.total ||
    pagination.total_pages !== expectedPageCount(pagination.total_items, pagination.page_size) ||
    (pagination.total_items === 0 ? items.length !== 0 : items.length > pagination.page_size) ||
    new Set(items.map(({ id }) => id)).size !== items.length
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

function normalizePatch(patch: UpstreamKeyPatch): UpstreamKeyPatch {
  const keys = Object.keys(patch)
  if (keys.length === 0 || keys.some((key) => key !== 'status' && key !== 'weight_manual')) {
    throw new Error('INVALID_UPSTREAM_KEY_PATCH')
  }
  const body: UpstreamKeyPatch = {}
  if (Object.prototype.hasOwnProperty.call(patch, 'status')) {
    body.status = projectEnum(patch.status, configuredStatuses)
  }
  if (Object.prototype.hasOwnProperty.call(patch, 'weight_manual')) {
    const weight = patch.weight_manual
    if (
      weight === undefined ||
      (weight !== null && (!Number.isInteger(weight) || weight < 1 || weight > 100))
    ) {
      throw new Error('INVALID_UPSTREAM_KEY_WEIGHT')
    }
    body.weight_manual = weight
  }
  return body
}

function groupKeyCollectionURL(
  groupId: number,
  filters: GroupKeyCollectionFilters,
): `/api/${string}` {
  const normalized = normalizeGroupKeyCollectionFilters(filters)
  const params = new URLSearchParams({
    page: String(normalized.page),
    page_size: String(normalized.page_size),
  })
  if (normalized.q !== undefined) params.set('q', normalized.q)
  if (normalized.status !== undefined) params.set('status', normalized.status)
  return `/api/groups/${groupId}/keys?${params.toString()}`
}

export async function getGroupKeyCollection(
  client: ApiClient,
  groupId: number,
  filters: GroupKeyCollectionFilters,
  signal?: AbortSignal,
): Promise<GroupKeyCollectionDto> {
  const normalized = normalizeGroupKeyCollectionFilters(filters)
  const result = projectGroupKeyCollection(
    await client.request(groupKeyCollectionURL(groupId, normalized), { method: 'GET', signal }),
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
  staleTime: Number.POSITIVE_INFINITY,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
} as const

export function groupKeyCollectionQueryOptions(
  client: ApiClient,
  groupID: MaybeRefOrGetter<number>,
  filters: MaybeRefOrGetter<GroupKeyCollectionFilters>,
) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: computed(() => controlQueryKeys.groups.keys(toValue(groupID), toValue(filters))),
    queryFn: ({ queryKey, signal }) =>
      getGroupKeyCollection(client, queryKey[3], queryKey[5], signal),
  })
}

export async function updateGroupKey(
  client: ApiClient,
  groupId: number,
  keyId: number,
  patch: UpstreamKeyPatch,
  signal?: AbortSignal,
): Promise<GroupKeyItemDto> {
  return projectGroupKeyItem(
    await client.request(`/api/groups/${groupId}/keys/${keyId}`, {
      method: 'PUT',
      json: normalizePatch(patch),
      signal,
    }),
  )
}

export async function restoreGroupKey(
  client: ApiClient,
  groupId: number,
  keyId: number,
  signal?: AbortSignal,
): Promise<GroupKeyItemDto> {
  return projectGroupKeyItem(
    await client.request(`/api/groups/${groupId}/keys/${keyId}/restore`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
}

export async function batchGroupKeys(
  client: ApiClient,
  groupId: number,
  body: GroupKeyBatchRequest,
  signal?: AbortSignal,
): Promise<GroupKeyBatchResultDto> {
  const ids = body.key_ids
  if (
    !['enable', 'disable', 'delete'].includes(body.action) ||
    ids.length < 1 ||
    ids.length > 100 ||
    ids.some((id) => !Number.isSafeInteger(id) || id < 1) ||
    new Set(ids).size !== ids.length
  ) {
    throw new Error('INVALID_GROUP_KEY_BATCH')
  }
  const record = projectRecord(
    await client.request(`/api/groups/${groupId}/keys/batch`, {
      method: 'POST',
      json: body,
      signal,
    }),
  )
  assertNoSecretLikeFields(record, groupKeyBatchFields)
  const affectedIDs = projectArray(record.affected_ids, (id) =>
    projectSafeInteger(id, { minimum: 1 }),
  )
  if (new Set(affectedIDs).size !== affectedIDs.length || affectedIDs.length !== ids.length)
    invalidResponse()
  return { affected_ids: affectedIDs, summary: projectGroupKeySummary(record.summary) }
}

export async function deleteGroupKey(
  client: ApiClient,
  groupId: number,
  keyId: number,
  signal?: AbortSignal,
): Promise<void> {
  await client.request(`/api/groups/${groupId}/keys/${keyId}`, { method: 'DELETE', signal })
}

function queryFilters(queryKey: QueryKey): GroupKeyCollectionFilters | undefined {
  const filters = queryKey[5]
  if (typeof filters !== 'object' || filters === null || Array.isArray(filters)) return undefined
  const record = filters as Record<string, unknown>
  if (
    !Number.isSafeInteger(record.page) ||
    (record.page_size !== 20 && record.page_size !== 50 && record.page_size !== 100) ||
    (record.q !== undefined && typeof record.q !== 'string') ||
    (record.status !== undefined && !effectiveStatuses.includes(record.status as GroupKeyStatus))
  ) {
    return undefined
  }
  return {
    page: record.page as number,
    page_size: record.page_size as 20 | 50 | 100,
    ...(record.q === undefined ? {} : { q: record.q }),
    ...(record.status === undefined ? {} : { status: record.status as GroupKeyStatus }),
  }
}

function matchesFilters(item: GroupKeyItemDto, filters: GroupKeyCollectionFilters): boolean {
  if (filters.status !== undefined && item.effective_status !== filters.status) return false
  return filters.q === undefined || item.mask.toLowerCase().includes(filters.q.toLowerCase())
}

function withSummaryDelta(
  summary: GroupKeySummaryDto,
  previous: GroupKeyItemDto,
  next: GroupKeyItemDto | undefined,
): GroupKeySummaryDto {
  const result = { ...summary }
  result[previous.effective_status]--
  if (next !== undefined) result[next.effective_status]++
  return result
}

function withRemovedItem(
  collection: GroupKeyCollectionDto,
  id: number,
  summary: GroupKeySummaryDto,
): GroupKeyCollectionDto {
  const items = collection.items.filter((item) => item.id !== id)
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

async function invalidateExactKeyPage(queryClient: QueryClient, queryKey: QueryKey): Promise<void> {
  await queryClient.invalidateQueries({ queryKey, exact: true, refetchType: 'none' })
}

/**
 * Reconciles only a cached page containing the previous item. Pages whose
 * membership or sort position cannot be known are explicitly marked stale.
 */
export async function cacheGroupKeyItem(
  queryClient: QueryClient,
  groupId: number,
  item: GroupKeyItemDto,
): Promise<void> {
  const queries = queryClient
    .getQueryCache()
    .findAll({ queryKey: controlQueryKeys.groups.keysAll(groupId) })
  const previous = queries
    .map((query) => (query.state.data as GroupKeyCollectionDto | undefined)?.items)
    .flatMap((items) => items ?? [])
    .find(({ id }) => id === item.id)
  for (const query of queries) {
    const filters = queryFilters(query.queryKey)
    const collection = query.state.data as GroupKeyCollectionDto | undefined
    const current = collection?.items.find(({ id }) => id === item.id)
    if (collection === undefined || previous === undefined) {
      await invalidateExactKeyPage(queryClient, query.queryKey)
      continue
    }
    const summary = withSummaryDelta(collection.summary, previous, item)
    if (filters === undefined || current === undefined) {
      queryClient.setQueryData<GroupKeyCollectionDto>(query.queryKey, { ...collection, summary })
      await invalidateExactKeyPage(queryClient, query.queryKey)
      continue
    }
    const nextMatches = matchesFilters(item, filters)
    if (!nextMatches) {
      queryClient.setQueryData(query.queryKey, withRemovedItem(collection, item.id, summary))
      await invalidateExactKeyPage(queryClient, query.queryKey)
      continue
    }
    if (current.effective_status !== item.effective_status) {
      queryClient.setQueryData<GroupKeyCollectionDto>(query.queryKey, { ...collection, summary })
      await invalidateExactKeyPage(queryClient, query.queryKey)
      continue
    }
    queryClient.setQueryData<GroupKeyCollectionDto>(query.queryKey, {
      ...collection,
      summary,
      items: collection.items.map((current) => (current.id === item.id ? item : current)),
    })
  }
}

/** Marks only this Group's key pages stale without scheduling an automatic request. */
export async function invalidateGroupKeyCollections(
  queryClient: QueryClient,
  groupId: number,
): Promise<void> {
  await queryClient.invalidateQueries({
    queryKey: controlQueryKeys.groups.keysAll(groupId),
    refetchType: 'none',
  })
}

/** A batch result carries the authoritative aggregate, so cached pages can be reconciled deterministically. */
export async function cacheGroupKeyBatch(
  queryClient: QueryClient,
  groupId: number,
  action: GroupKeyBatchRequest['action'],
  result: GroupKeyBatchResultDto,
): Promise<void> {
  const affected = new Set(result.affected_ids)
  const queries = queryClient
    .getQueryCache()
    .findAll({ queryKey: controlQueryKeys.groups.keysAll(groupId) })
  for (const query of queries) {
    const collection = query.state.data as GroupKeyCollectionDto | undefined
    if (collection === undefined) continue
    if (action !== 'delete') {
      queryClient.setQueryData<GroupKeyCollectionDto>(query.queryKey, {
        ...collection,
        summary: result.summary,
      })
      await invalidateExactKeyPage(queryClient, query.queryKey)
      continue
    }
    const matchingItems = collection.items.filter((item) => affected.has(item.id))
    if (matchingItems.length !== affected.size) {
      queryClient.setQueryData<GroupKeyCollectionDto>(query.queryKey, {
        ...collection,
        summary: result.summary,
      })
      await invalidateExactKeyPage(queryClient, query.queryKey)
      continue
    }
    let next = { ...collection, summary: result.summary }
    for (const item of matchingItems) {
      next = withRemovedItem(next, item.id, result.summary)
    }
    queryClient.setQueryData<GroupKeyCollectionDto>(query.queryKey, next)
    await invalidateExactKeyPage(queryClient, query.queryKey)
  }
}

/**
 * Compatibility-only shape for the unported legacy detail tabs. The new API is
 * still read through the collection contract; this projection must not be used
 * by the Ledger detail implementation.
 */
export interface UpstreamKeyDto {
  id: number
  group_id: number
  mask: string
  status: UpstreamKeyStatus
  effective_status: UpstreamKeyEffectiveStatus
  weight_manual: number | null
  weight_auto: number
  blacklisted: boolean
  cooldown_until_ms: number | null
  failure_count: number
}

export async function listGroupKeys(
  client: ApiClient,
  groupId: number,
  signal?: AbortSignal,
): Promise<UpstreamKeyDto[]> {
  const collection = await getGroupKeyCollection(
    client,
    groupId,
    { page: 1, page_size: 100 },
    signal,
  )
  return collection.items.map((item) => ({
    id: item.id,
    group_id: groupId,
    mask: item.mask,
    status: item.configured_status,
    effective_status: item.effective_status,
    weight_manual: item.weight_mode === 'manual' ? item.weight : null,
    weight_auto: item.weight_mode === 'auto' ? (item.weight ?? 0) : 0,
    blacklisted: item.effective_status === 'blacklisted',
    cooldown_until_ms: item.cooldown_until_ms,
    failure_count: item.consecutive_failure_count,
  }))
}

export function upstreamKeyListQueryOptions(client: ApiClient, groupID: MaybeRefOrGetter<number>) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: computed(() => controlQueryKeys.groups.legacyKeys(toValue(groupID))),
    queryFn: ({ signal }) => listGroupKeys(client, toValue(groupID), signal),
  })
}
