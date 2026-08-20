import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import type {
  AccessKeyCreateResultDto,
  AccessKeyCollectionFilters,
  AccessKeyCollectionItemDto,
  AccessKeyCollectionPaginationDto,
  AccessKeyCollectionResponseDto,
  AccessKeyCollectionSummaryDto,
  AccessKeyCostLimitKind,
  AccessKeyCostLimitRuleDto,
  AccessKeyCostLimitRuleStatusDto,
  AccessKeyCostLimitStatusDto,
  AccessKeyDto,
  AccessKeyFiltersDto,
  AccessKeyOptionDto,
  AccessKeyRevealDto,
} from '@/api/control/types'
import { knownAccessProtocols } from '@/api/control/protocols'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys, normalizeAccessKeyCollectionFilters } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEpochMilliseconds,
  projectEnum,
  projectNullableEpochMilliseconds,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type {
  AccessKeyCreateResultDto,
  AccessKeyCollectionFilters,
  AccessKeyCollectionItemDto,
  AccessKeyCollectionPaginationDto,
  AccessKeyCollectionResponseDto,
  AccessKeyCollectionSummaryDto,
  AccessKeyDto,
  AccessKeyFiltersDto,
  AccessKeyOptionDto,
  AccessKeyRevealDto,
  AccessProtocol,
} from '@/api/control/types'

export interface CreateAccessKeyRequest {
  name: string
  status: AccessKeyDto['status']
  filters: AccessKeyFiltersDto
  rpm_limit: number
  cost_limit_rules: AccessKeyCostLimitRuleInput[]
}

export interface AccessKeyCostLimitRuleInput {
  id?: number
  kind: AccessKeyCostLimitKind
  limit_usd: string
  period_seconds?: number
}

export type UpdateAccessKeyRequest = Partial<{
  name: string
  status: AccessKeyDto['status']
  filters: AccessKeyFiltersDto
  rpm_limit: number
  cost_limit_rules: AccessKeyCostLimitRuleInput[]
}>

const metadataFields = [
  'id',
  'name',
  'masked_key',
  'status',
  'filters',
  'rpm_limit',
  'cost_limit_rules',
  'cost_limit_status',
  'created_at_ms',
  'updated_at_ms',
] as const
const optionFields = ['id', 'name', 'status'] as const
const collectionFields = ['summary', 'items', 'pagination'] as const
const collectionSummaryFields = ['total', 'active', 'disabled'] as const
const collectionPaginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const collectionItemFields = [...metadataFields, 'last_request_at_ms'] as const
const costLimitRuleFields = ['id', 'kind', 'limit_usd', 'period_seconds'] as const
const costLimitRuleStatusFields = [
  ...costLimitRuleFields,
  'used_usd',
  'remaining_usd',
  'status',
  'window_started_at_ms',
  'window_ends_at_ms',
] as const
const costLimitStatusFields = [
  'observed_at_ms',
  'allowed',
  'recoverable',
  'next_available_at_ms',
  'rules',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankTrimmedString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0 || result !== result.trim()) invalidResponse()
  return result
}

function projectFilters(value: unknown): AccessKeyFiltersDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['groups', 'protocols', 'models'])
  const groups = projectArray(record.groups, (id) => projectSafeInteger(id, { minimum: 1 }))
  const protocols = projectArray(record.protocols, (protocol) =>
    projectEnum(protocol, knownAccessProtocols),
  )
  const models = projectArray(record.models, projectNonBlankTrimmedString)
  if (
    new Set(groups).size !== groups.length ||
    new Set(protocols).size !== protocols.length ||
    new Set(models).size !== models.length
  ) {
    invalidResponse()
  }
  return { groups, protocols, models }
}

function projectUSD(value: unknown, positive = false): string {
  const result = projectString(value)
  if (!/^(0|[1-9]\d*)(\.\d{1,9})?$/.test(result)) invalidResponse()
  if (positive && /^0(?:\.0+)?$/.test(result)) invalidResponse()
  return result
}

function projectCostLimitPeriod(value: unknown, kind: AccessKeyCostLimitKind): number {
  if (kind === 'total') {
    if (value !== undefined && value !== 0) invalidResponse()
    return 0
  }
  return projectSafeInteger(value, { minimum: 60, maximum: 31_536_000 })
}

export function projectAccessKeyCostLimitRule(value: unknown): AccessKeyCostLimitRuleDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, costLimitRuleFields)
  const kind = projectEnum(record.kind, ['total', 'periodic'] as const)
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    kind,
    limit_usd: projectUSD(record.limit_usd, true),
    period_seconds: projectCostLimitPeriod(record.period_seconds, kind),
  }
}

export function projectAccessKeyCostLimitRuleStatus(
  value: unknown,
): AccessKeyCostLimitRuleStatusDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, costLimitRuleStatusFields)
  const definition = projectAccessKeyCostLimitRule(
    Object.fromEntries(costLimitRuleFields.map((field) => [field, record[field]])),
  )
  const windowStarted =
    record.window_started_at_ms === undefined || record.window_started_at_ms === null
      ? null
      : projectEpochMilliseconds(record.window_started_at_ms)
  const windowEnds =
    record.window_ends_at_ms === undefined || record.window_ends_at_ms === null
      ? null
      : projectEpochMilliseconds(record.window_ends_at_ms)
  if (
    (windowStarted === null) !== (windowEnds === null) ||
    (windowEnds ?? 1) <= (windowStarted ?? 0)
  ) {
    invalidResponse()
  }
  const status = projectEnum(record.status, ['available', 'inactive', 'exhausted'] as const)
  if (definition.kind === 'total' && (windowStarted !== null || status === 'inactive')) {
    invalidResponse()
  }
  if (definition.kind === 'periodic' && status === 'inactive' && windowStarted !== null) {
    invalidResponse()
  }
  return {
    ...definition,
    used_usd: projectUSD(record.used_usd),
    remaining_usd: projectUSD(record.remaining_usd),
    status,
    window_started_at_ms: windowStarted,
    window_ends_at_ms: windowEnds,
  }
}

function validCostLimitRuleSet(rules: readonly AccessKeyCostLimitRuleDto[]): boolean {
  const ids = new Set<number>()
  const periods = new Set<number>()
  let totalCount = 0
  let periodicCount = 0
  for (const rule of rules) {
    if (ids.has(rule.id)) return false
    ids.add(rule.id)
    if (rule.kind === 'total') totalCount += 1
    else {
      periodicCount += 1
      if (periods.has(rule.period_seconds)) return false
      periods.add(rule.period_seconds)
    }
  }
  return totalCount <= 1 && periodicCount <= 10
}

export function projectAccessKeyCostLimitStatus(value: unknown): AccessKeyCostLimitStatusDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, costLimitStatusFields)
  const allowed = projectBoolean(record.allowed)
  const recoverable = projectBoolean(record.recoverable)
  const nextAvailable = projectNullableEpochMilliseconds(record.next_available_at_ms)
  const rules = projectArray(record.rules, projectAccessKeyCostLimitRuleStatus)
  if (
    !validCostLimitRuleSet(rules) ||
    (allowed && nextAvailable !== null) ||
    (!recoverable && nextAvailable !== null) ||
    (!allowed && recoverable && nextAvailable === null)
  ) {
    invalidResponse()
  }
  return {
    observed_at_ms: projectEpochMilliseconds(record.observed_at_ms),
    allowed,
    recoverable,
    next_available_at_ms: nextAvailable,
    rules,
  }
}

export function projectAccessKeyMetadata(value: unknown): AccessKeyDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, metadataFields)
  const maskedKey = projectString(record.masked_key)
  const costLimitRules = projectArray(record.cost_limit_rules, projectAccessKeyCostLimitRule)
  if (!validCostLimitRuleSet(costLimitRules)) invalidResponse()
  const costLimitStatus =
    record.cost_limit_status === undefined
      ? null
      : projectAccessKeyCostLimitStatus(record.cost_limit_status)
  if (
    costLimitStatus !== null &&
    JSON.stringify(costLimitRules) !==
      JSON.stringify(
        costLimitStatus.rules.map(({ id, kind, limit_usd, period_seconds }) => ({
          id,
          kind,
          limit_usd,
          period_seconds,
        })),
      )
  ) {
    invalidResponse()
  }
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankTrimmedString(record.name),
    masked_key: maskedKey,
    status: projectEnum(record.status, ['active', 'disabled'] as const),
    filters: projectFilters(record.filters),
    rpm_limit: projectSafeInteger(record.rpm_limit, { minimum: 0 }),
    cost_limit_rules: costLimitRules,
    cost_limit_status: costLimitStatus,
    created_at_ms: projectEpochMilliseconds(record.created_at_ms),
    updated_at_ms: projectEpochMilliseconds(record.updated_at_ms),
  }
}

function projectAccessKeyCollectionSummary(value: unknown): AccessKeyCollectionSummaryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, collectionSummaryFields)
  const result = {
    total: projectSafeInteger(record.total, { minimum: 0 }),
    active: projectSafeInteger(record.active, { minimum: 0 }),
    disabled: projectSafeInteger(record.disabled, { minimum: 0 }),
  }
  if (result.total !== result.active + result.disabled) invalidResponse()
  return result
}

function projectAccessKeyCollectionPagination(value: unknown): AccessKeyCollectionPaginationDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, collectionPaginationFields)
  return {
    page: projectSafeInteger(record.page, { minimum: 1 }),
    page_size: projectSafeInteger(record.page_size, { minimum: 20, maximum: 20 }) as 20,
    total_items: projectSafeInteger(record.total_items, { minimum: 0 }),
    total_pages: projectSafeInteger(record.total_pages, { minimum: 0 }),
  }
}

function expectedCollectionTotalPages(totalItems: number, pageSize: number): number {
  return totalItems === 0 ? 0 : Math.ceil(totalItems / pageSize)
}

function expectedCollectionPageItems(pagination: AccessKeyCollectionPaginationDto): number {
  if (pagination.total_items === 0 || pagination.page > pagination.total_pages) return 0
  if (pagination.page < pagination.total_pages) return pagination.page_size
  const finalPageItems = pagination.total_items % pagination.page_size
  return finalPageItems === 0 ? pagination.page_size : finalPageItems
}

export function projectAccessKeyCollectionItem(value: unknown): AccessKeyCollectionItemDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, collectionItemFields)
  const metadata = projectAccessKeyMetadata(
    Object.fromEntries(metadataFields.map((field) => [field, record[field]])),
  )
  return {
    ...metadata,
    last_request_at_ms: projectNullableEpochMilliseconds(record.last_request_at_ms),
  }
}

export function projectAccessKeyCollection(value: unknown): AccessKeyCollectionResponseDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, collectionFields)
  const summary = projectAccessKeyCollectionSummary(record.summary)
  const items = projectArray(record.items, projectAccessKeyCollectionItem)
  const pagination = projectAccessKeyCollectionPagination(record.pagination)
  if (
    pagination.total_items > summary.total ||
    pagination.total_pages !==
      expectedCollectionTotalPages(pagination.total_items, pagination.page_size) ||
    items.length !== expectedCollectionPageItems(pagination) ||
    new Set(items.map(({ id }) => id)).size !== items.length
  ) {
    invalidResponse()
  }
  return { summary, items, pagination }
}

export function projectAccessKeyOption(value: unknown): AccessKeyOptionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, optionFields)
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankTrimmedString(record.name),
    status: projectEnum(record.status, ['active', 'disabled'] as const),
  }
}

export async function listAccessKeyCollection(
  client: ApiClient,
  filters: AccessKeyCollectionFilters,
  signal?: AbortSignal,
): Promise<AccessKeyCollectionResponseDto> {
  const normalized = normalizeAccessKeyCollectionFilters(filters)
  const params = new URLSearchParams({
    page: String(normalized.page),
    page_size: String(normalized.page_size),
  })
  if (normalized.q !== undefined) params.set('q', normalized.q)
  if (normalized.status !== undefined) params.set('status', normalized.status)
  const result = projectAccessKeyCollection(
    await client.request(`/api/access-keys?${params.toString()}`, { method: 'GET', signal }),
  )
  if (result.pagination.page !== normalized.page) invalidResponse()
  return result
}

export async function listAccessKeyOptions(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<AccessKeyOptionDto[]> {
  return projectArray(
    await client.request('/api/access-keys/options', { method: 'GET', signal }),
    projectAccessKeyOption,
  )
}

export function accessKeyCollectionQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<AccessKeyCollectionFilters>,
) {
  return queryOptions({
    queryKey: computed(() => controlQueryKeys.accessKeys.collection(toValue(filters))),
    queryFn: ({ queryKey, signal }) => listAccessKeyCollection(client, queryKey[3], signal),
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
}

export function accessKeyOptionsQueryOptions(
  client: ApiClient,
  enabled: MaybeRefOrGetter<boolean> = true,
) {
  return queryOptions({
    queryKey: controlQueryKeys.accessKeys.options(),
    queryFn: ({ signal }) => listAccessKeyOptions(client, signal),
    enabled: computed(() => toValue(enabled)),
    gcTime: 0,
  })
}

export async function createAccessKey(
  client: ApiClient,
  body: CreateAccessKeyRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<AccessKeyCreateResultDto> {
  const record = projectRecord(
    await client.request('/api/access-keys', {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      json: body,
      signal,
    }),
  )
  assertNoSecretLikeFields(record, [...metadataFields, 'key', 'replayed'])
  const key = record.key === undefined ? undefined : projectString(record.key)
  if (typeof record.replayed !== 'boolean') invalidResponse()
  const metadata = Object.fromEntries(metadataFields.map((field) => [field, record[field]]))
  return {
    ...projectAccessKeyMetadata(metadata),
    ...(key === undefined ? {} : { key }),
    replayed: record.replayed,
  }
}

export async function revealAccessKey(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<AccessKeyRevealDto> {
  const record = projectRecord(
    await client.request(`/api/access-keys/${id}/reveal`, {
      method: 'POST',
      signal,
    }),
  )
  assertNoSecretLikeFields(record, ['id', 'key', 'revealed_at_ms'])
  if (projectSafeInteger(record.id, { minimum: 1 }) !== id) invalidResponse()
  return {
    id,
    key: projectString(record.key),
    revealed_at_ms: projectEpochMilliseconds(record.revealed_at_ms),
  }
}

export async function updateAccessKey(
  client: ApiClient,
  id: number,
  body: UpdateAccessKeyRequest,
  signal?: AbortSignal,
): Promise<AccessKeyDto> {
  return projectAccessKeyMetadata(
    await client.request(`/api/access-keys/${id}`, {
      method: 'PUT',
      json: body,
      signal,
    }),
  )
}

export function deleteAccessKey(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<void> {
  return client.request(`/api/access-keys/${id}`, { method: 'DELETE', signal })
}

export function resetAccessKeyCostLimits(
  client: ApiClient,
  id: number,
  ruleIDs: readonly number[],
  signal?: AbortSignal,
): Promise<void> {
  return client.request(`/api/access-keys/${id}/cost-limits/reset`, {
    method: 'POST',
    json: { rule_ids: ruleIDs },
    signal,
  })
}

export const accessKeyResources = {
  collection: {
    queryKey: controlQueryKeys.accessKeys.collectionAll,
    gcTime: 0,
    cleanup: 'authenticated-session',
    optimisticUpdates: false,
    allowedFields: collectionItemFields,
  },
  options: {
    queryKey: controlQueryKeys.accessKeys.options(),
    gcTime: 0,
    cleanup: 'authenticated-session',
    optimisticUpdates: false,
    allowedFields: optionFields,
  },
} as const
