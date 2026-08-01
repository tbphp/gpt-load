import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { enabledDataProtocols } from '@/api/control/protocols'
import type {
  GroupCollectionFilters,
  GroupCollectionItemDto,
  GroupCollectionPaginationDto,
  GroupCollectionResponseDto,
  GroupCollectionStatus,
  GroupCollectionSummaryDto,
  GroupModelDto,
  GroupOptionDto,
  GroupProtocol,
  KeyCounts,
} from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys, normalizeGroupCollectionFilters } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEpochMilliseconds,
  projectEnum,
  projectHTTPURL,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

const groupDetailFields = [
  'id',
  'name',
  'upstream_url',
  'protocols',
  'models',
  'enabled',
  'key_count',
  'validation_model',
  'weight_manual',
  'config',
  'effective_config',
] as const
const groupCollectionFields = ['observed_at_ms', 'summary', 'items', 'pagination'] as const
const groupCollectionSummaryFields = ['total', 'available', 'unavailable', 'disabled'] as const
const groupCollectionItemFields = [
  'id',
  'name',
  'status',
  'upstream_url',
  'protocols',
  'model_count',
  'key_counts',
] as const
const groupCollectionPaginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const groupOptionFields = ['id', 'name', 'models'] as const
const keyCountFields = ['total', 'available', 'cooldown', 'blacklisted', 'disabled'] as const
const groupCollectionStatuses = ['available', 'unavailable', 'disabled'] as const
const runtimeSettingFields = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'header_rules',
  'inject_usage_options',
] as const

export interface HeaderRulesDto {
  set: Record<string, string>
  remove: string[]
}

export interface GroupRuntimeConfigDto {
  connect_timeout?: number
  first_byte_timeout?: number
  request_timeout?: number
  stream_idle_timeout?: number
  header_rules?: HeaderRulesDto
  inject_usage_options?: boolean
}

export interface GroupEffectiveConfigDto {
  connect_timeout: number
  first_byte_timeout: number
  request_timeout: number
  stream_idle_timeout: number
  header_rules: HeaderRulesDto
  inject_usage_options: boolean
}

export interface GroupDetailDto {
  id: number
  name: string
  upstream_url: string
  protocols: GroupProtocol[]
  models: GroupModelDto[]
  enabled: boolean
  key_count: number
  validation_model: string | null
  weight_manual: number | null
  config: GroupRuntimeConfigDto
  effective_config: GroupEffectiveConfigDto
}

export interface GroupUpdateRequest {
  name?: string
  enabled?: boolean
  upstream_url?: string
  protocols?: GroupProtocol[]
  validation_model?: string | null
  weight_manual?: number | null
  config?: GroupRuntimeConfigDto
  confirm_upstream_url_change?: true
}

export interface GroupUpdateResult {
  group: GroupDetailDto
  model_rediscovery_recommended: boolean
}

export interface AccessKeyReferenceDto {
  id: number
  name: string
}

export interface GroupInUseData {
  access_keys: AccessKeyReferenceDto[]
}

export interface ModelDiscoveryRequest {
  upstream_url: string
  protocols: readonly GroupProtocol[]
  keys: string
  config: GroupRuntimeConfigDto
}

export interface ModelDiscoveryResult {
  models: string[]
}

export interface GroupModelsReplaceRequest {
  models: GroupModelDto[]
}

export interface GroupCreateRequest {
  name?: string
  upstream_url: string
  protocols: readonly GroupProtocol[]
  models: GroupModelDto[]
  config: GroupRuntimeConfigDto
  keys: string
  confirm_same_upstream_url: boolean
}

export interface GroupCreateResult {
  group_id: number
  group_name: string
  keys_added: number
  keys_duplicated: number
  models: GroupModelDto[]
}

export interface GroupKeyImportRequest {
  keys: string
}

export interface GroupKeyImportResult {
  group_id: number
  keys_added: number
  keys_duplicated: number
}

export interface UpstreamUrlConflictData {
  groups: Array<{ id: number; name: string }>
}

function projectNonBlankString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0) throw new InvalidResponseError()
  return result
}

function projectGroupModel(value: unknown): GroupModelDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['id', 'alias'])
  return {
    id: projectNonBlankString(record.id),
    alias: projectString(record.alias, { allowEmpty: true }),
  }
}

function projectHeaderRules(value: unknown): HeaderRulesDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['set', 'remove'])
  const setRecord = projectRecord(record.set)
  const set: Record<string, string> = {}
  for (const [name, headerValue] of Object.entries(setRecord)) {
    if (name.trim().length === 0) throw new InvalidResponseError()
    set[name] = projectString(headerValue, { allowEmpty: true })
  }
  return {
    set,
    remove: projectArray(record.remove, projectNonBlankString),
  }
}

function projectRuntimeConfig(value: unknown, complete: false): GroupRuntimeConfigDto
function projectRuntimeConfig(value: unknown, complete: true): GroupEffectiveConfigDto
function projectRuntimeConfig(
  value: unknown,
  complete: boolean,
): GroupRuntimeConfigDto | GroupEffectiveConfigDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, runtimeSettingFields)
  const result: GroupRuntimeConfigDto = {}

  for (const field of [
    'connect_timeout',
    'first_byte_timeout',
    'request_timeout',
    'stream_idle_timeout',
  ] as const) {
    if (complete || Object.prototype.hasOwnProperty.call(record, field)) {
      result[field] = projectSafeInteger(record[field], { minimum: 1 })
    }
  }
  if (complete || Object.prototype.hasOwnProperty.call(record, 'header_rules')) {
    result.header_rules = projectHeaderRules(record.header_rules)
  }
  if (complete || Object.prototype.hasOwnProperty.call(record, 'inject_usage_options')) {
    result.inject_usage_options = projectBoolean(record.inject_usage_options)
  }
  return result as GroupRuntimeConfigDto | GroupEffectiveConfigDto
}

export function projectGroupDetail(value: unknown): GroupDetailDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupDetailFields)
  const validationModel =
    record.validation_model === null ? null : projectNonBlankString(record.validation_model)
  const weightManual =
    record.weight_manual === null
      ? null
      : projectSafeInteger(record.weight_manual, { minimum: 0, maximum: 100 })
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    upstream_url: projectHTTPURL(record.upstream_url),
    protocols: projectArray(record.protocols, (protocol) =>
      projectEnum(protocol, enabledDataProtocols),
    ),
    models: projectArray(record.models, projectGroupModel),
    enabled: projectBoolean(record.enabled),
    key_count: projectSafeInteger(record.key_count, { minimum: 0 }),
    validation_model: validationModel,
    weight_manual: weightManual,
    config: projectRuntimeConfig(record.config, false),
    effective_config: projectRuntimeConfig(record.effective_config, true),
  }
}

function projectKeyCounts(value: unknown): KeyCounts {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, keyCountFields)
  const result = {
    total: projectSafeInteger(record.total, { minimum: 0 }),
    available: projectSafeInteger(record.available, { minimum: 0 }),
    cooldown: projectSafeInteger(record.cooldown, { minimum: 0 }),
    blacklisted: projectSafeInteger(record.blacklisted, { minimum: 0 }),
    disabled: projectSafeInteger(record.disabled, { minimum: 0 }),
  }
  if (result.total !== result.available + result.cooldown + result.blacklisted + result.disabled) {
    throw new InvalidResponseError()
  }
  return result
}

function projectGroupCollectionSummary(value: unknown): GroupCollectionSummaryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupCollectionSummaryFields)
  const result = {
    total: projectSafeInteger(record.total, { minimum: 0 }),
    available: projectSafeInteger(record.available, { minimum: 0 }),
    unavailable: projectSafeInteger(record.unavailable, { minimum: 0 }),
    disabled: projectSafeInteger(record.disabled, { minimum: 0 }),
  }
  if (result.total !== result.available + result.unavailable + result.disabled) {
    throw new InvalidResponseError()
  }
  return result
}

function projectGroupCollectionItem(value: unknown): GroupCollectionItemDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupCollectionItemFields)
  const protocols = projectArray(record.protocols, (protocol) =>
    projectEnum(protocol, enabledDataProtocols),
  )
  if (new Set(protocols).size !== protocols.length) throw new InvalidResponseError()
  const status = projectEnum(record.status, groupCollectionStatuses) as GroupCollectionStatus
  const keyCounts = projectKeyCounts(record.key_counts)
  if (
    (status === 'available' && keyCounts.available === 0) ||
    (status === 'unavailable' && keyCounts.available !== 0) ||
    (status === 'disabled' && keyCounts.disabled !== keyCounts.total)
  ) {
    throw new InvalidResponseError()
  }
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    status,
    upstream_url: projectHTTPURL(record.upstream_url),
    protocols,
    model_count: projectSafeInteger(record.model_count, { minimum: 0 }),
    key_counts: keyCounts,
  }
}

function projectGroupCollectionPagination(value: unknown): GroupCollectionPaginationDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupCollectionPaginationFields)
  return {
    page: projectSafeInteger(record.page, { minimum: 1 }),
    page_size: projectSafeInteger(record.page_size, { minimum: 20, maximum: 20 }) as 20,
    total_items: projectSafeInteger(record.total_items, { minimum: 0 }),
    total_pages: projectSafeInteger(record.total_pages, { minimum: 0 }),
  }
}

function expectedGroupCollectionTotalPages(totalItems: number, pageSize: number): number {
  if (totalItems === 0) return 0
  const completePages = Math.floor(totalItems / pageSize)
  return completePages + (totalItems % pageSize === 0 ? 0 : 1)
}

function expectedGroupCollectionPageItems(pagination: GroupCollectionPaginationDto): number {
  if (pagination.total_items === 0 || pagination.page > pagination.total_pages) return 0
  if (pagination.page < pagination.total_pages) return pagination.page_size
  const finalPageItems = pagination.total_items % pagination.page_size
  return finalPageItems === 0 ? pagination.page_size : finalPageItems
}

export function projectGroupCollection(value: unknown): GroupCollectionResponseDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupCollectionFields)
  const items = projectArray(record.items, projectGroupCollectionItem)
  const summary = projectGroupCollectionSummary(record.summary)
  const pagination = projectGroupCollectionPagination(record.pagination)
  if (
    pagination.total_items > summary.total ||
    pagination.total_pages !==
      expectedGroupCollectionTotalPages(pagination.total_items, pagination.page_size) ||
    items.length !== expectedGroupCollectionPageItems(pagination) ||
    new Set(items.map(({ id }) => id)).size !== items.length
  ) {
    throw new InvalidResponseError()
  }
  return {
    observed_at_ms: projectEpochMilliseconds(record.observed_at_ms),
    summary,
    items,
    pagination,
  }
}

function projectGroupOption(value: unknown): GroupOptionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupOptionFields)
  const models = projectArray(record.models, projectNonBlankString)
  if (new Set(models).size !== models.length) throw new InvalidResponseError()
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    models,
  }
}

export function projectGroupOptions(value: unknown): GroupOptionDto[] {
  const options = projectArray(value, projectGroupOption)
  if (new Set(options.map(({ id }) => id)).size !== options.length) {
    throw new InvalidResponseError()
  }
  return options
}

function projectGroupUpdateResult(value: unknown): GroupUpdateResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['group', 'model_rediscovery_recommended'])
  return {
    group: projectGroupDetail(record.group),
    model_rediscovery_recommended: projectBoolean(record.model_rediscovery_recommended),
  }
}

function projectDiscoveryResult(value: unknown): ModelDiscoveryResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['models'])
  return { models: projectArray(record.models, projectNonBlankString) }
}

function projectGroupCreateResult(value: unknown): GroupCreateResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'group_id',
    'group_name',
    'keys_added',
    'keys_duplicated',
    'models',
  ])
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    keys_added: projectSafeInteger(record.keys_added, { minimum: 0 }),
    keys_duplicated: projectSafeInteger(record.keys_duplicated, { minimum: 0 }),
    models: projectArray(record.models, projectGroupModel),
  }
}

function projectGroupKeyImportResult(value: unknown): GroupKeyImportResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['group_id', 'keys_added', 'keys_duplicated'])
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    keys_added: projectSafeInteger(record.keys_added, { minimum: 0 }),
    keys_duplicated: projectSafeInteger(record.keys_duplicated, { minimum: 0 }),
  }
}

function projectIDName(value: unknown): { id: number; name: string } {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['id', 'name'])
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
  }
}

export function isUpstreamUrlConflictData(value: unknown): value is UpstreamUrlConflictData {
  try {
    const record = projectRecord(value)
    const groups = projectArray(record.groups, projectIDName)
    return groups.length > 0
  } catch {
    return false
  }
}

export function isGroupInUseData(value: unknown): value is GroupInUseData {
  try {
    const record = projectRecord(value)
    const accessKeys = projectArray(record.access_keys, projectIDName)
    return accessKeys.length > 0
  } catch {
    return false
  }
}

export async function listGroupCollection(
  client: ApiClient,
  filters: GroupCollectionFilters,
  signal?: AbortSignal,
): Promise<GroupCollectionResponseDto> {
  const normalized = normalizeGroupCollectionFilters(filters)
  const params = new URLSearchParams()
  params.set('sort', normalized.sort)
  params.set('page', String(normalized.page))
  params.set('page_size', String(normalized.page_size))
  if (normalized.q !== undefined) params.set('q', normalized.q)
  if (normalized.status !== undefined) params.set('status', normalized.status)
  if (normalized.protocol !== undefined) params.set('protocol', normalized.protocol)
  const result = projectGroupCollection(
    await client.request(`/api/groups?${params.toString()}`, { method: 'GET', signal }),
  )
  if (result.pagination.page !== normalized.page) throw new InvalidResponseError()
  return result
}

export async function listGroupOptions(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<GroupOptionDto[]> {
  return projectGroupOptions(await client.request('/api/groups/options', { method: 'GET', signal }))
}

export async function getGroup(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<GroupDetailDto> {
  return projectGroupDetail(
    await client.request(`/api/groups/${groupID}`, { method: 'GET', signal }),
  )
}

export function groupCollectionQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<GroupCollectionFilters>,
) {
  return queryOptions({
    queryKey: computed(() => controlQueryKeys.groups.collection(toValue(filters))),
    queryFn: ({ queryKey, signal }) => listGroupCollection(client, queryKey[3], signal),
    placeholderData: keepPreviousData,
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
  })
}

export function groupOptionsQueryOptions(client: ApiClient) {
  return queryOptions({
    queryKey: controlQueryKeys.groups.options(),
    queryFn: ({ signal }) => listGroupOptions(client, signal),
  })
}

export function groupDetailQueryOptions(
  client: ApiClient,
  groupID: MaybeRefOrGetter<number | undefined>,
) {
  return queryOptions({
    queryKey: computed(() => {
      const id = toValue(groupID)
      return id === undefined
        ? controlQueryKeys.groups.details()
        : controlQueryKeys.groups.detail(id)
    }),
    queryFn: ({ signal }) => {
      const id = toValue(groupID)
      if (id === undefined) throw new InvalidResponseError()
      return getGroup(client, id, signal)
    },
    enabled: computed(() => toValue(groupID) !== undefined),
    gcTime: 0,
  })
}

export async function updateGroup(
  client: ApiClient,
  groupID: number,
  body: GroupUpdateRequest,
  signal?: AbortSignal,
): Promise<GroupUpdateResult> {
  return projectGroupUpdateResult(
    await client.request(`/api/groups/${groupID}`, {
      method: 'PUT',
      json: body,
      signal,
    }),
  )
}

export async function deleteGroup(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<void> {
  await client.request(`/api/groups/${groupID}`, { method: 'DELETE', signal })
}

export async function discoverModels(
  client: ApiClient,
  body: ModelDiscoveryRequest,
  signal?: AbortSignal,
): Promise<ModelDiscoveryResult> {
  return projectDiscoveryResult(
    await client.request('/api/models/discover', {
      method: 'POST',
      json: body,
      signal,
    }),
  )
}

export async function discoverGroupModels(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<ModelDiscoveryResult> {
  return projectDiscoveryResult(
    await client.request(`/api/groups/${groupID}/models/discover`, {
      method: 'POST',
      signal,
    }),
  )
}

export async function replaceGroupModels(
  client: ApiClient,
  groupID: number,
  body: GroupModelsReplaceRequest,
  signal?: AbortSignal,
): Promise<GroupDetailDto> {
  return projectGroupDetail(
    await client.request(`/api/groups/${groupID}/models`, {
      method: 'PUT',
      json: body,
      signal,
    }),
  )
}

export async function createGroup(
  client: ApiClient,
  body: GroupCreateRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<GroupCreateResult> {
  return projectGroupCreateResult(
    await client.request('/api/groups', {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      json: body,
      signal,
    }),
  )
}

export async function importGroupKeys(
  client: ApiClient,
  groupID: number,
  body: GroupKeyImportRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<GroupKeyImportResult> {
  return projectGroupKeyImportResult(
    await client.request(`/api/groups/${groupID}/keys/import`, {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      json: body,
      signal,
    }),
  )
}
