import { keepPreviousData, queryOptions, type QueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import type {
  ChannelParamsDto,
  ConnectionType,
  CredentialCounts,
  GroupCollectionFilters,
  GroupCollectionItemDto,
  GroupCollectionPaginationDto,
  GroupCollectionResponseDto,
  GroupCollectionStatus,
  GroupCollectionSummaryDto,
  GroupModelItemDto,
  GroupModelsDto,
  GroupOptionDto,
  GroupSettingsDto,
  GroupSummaryDto,
} from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys, normalizeGroupCollectionFilters } from '@/app/query-keys'
import { projectModelCandidate, type ModelCandidate } from '@/app/resources/providers'
import { projectChannelID } from '@/app/resources/channels'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEpochMilliseconds,
  projectEnum,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

const groupSummaryFields = [
  'id',
  'name',
  'channel_id',
  'connection_type',
  'params',
  'service_status',
  'credential_count',
  'model_count',
] as const
const groupSettingsFields = [
  'name',
  'channel_id',
  'connection_type',
  'params',
  'validation_model',
  'enabled',
  'weight_manual',
  'overrides',
  'effective',
] as const
const groupModelsFields = ['items', 'total', 'pending'] as const
const groupModelItemFields = [
  'id',
  'alias',
  'alias_enabled',
  'client_model',
  'pricing_status',
] as const
const groupCollectionFields = ['observed_at_ms', 'summary', 'items', 'pagination'] as const
const groupCollectionSummaryFields = ['total', 'available', 'unavailable', 'disabled'] as const
const groupCollectionItemFields = [
  'id',
  'name',
  'channel_id',
  'connection_type',
  'params',
  'status',
  'model_count',
  'credential_counts',
] as const
const groupCollectionPaginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const groupOptionFields = [
  'id',
  'name',
  'channel_id',
  'connection_type',
  'params',
  'enabled',
  'models',
] as const
const credentialCountFields = ['total', 'available', 'cooldown', 'blacklisted', 'disabled'] as const
const groupCollectionStatuses = ['available', 'unavailable', 'disabled'] as const
const connectionTypes = ['api_key', 'subscription'] as const
const runtimeSettingFields = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'header_rules',
  'inject_usage_options',
  'affinity_enabled',
] as const

export interface HeaderRulesDto {
  set: Record<string, string>
  remove: string[]
}

export interface GroupRuntimeConfigDto {
  first_byte_timeout?: number
  request_timeout?: number
  stream_idle_timeout?: number
  header_rules?: HeaderRulesDto
  inject_usage_options?: boolean
  affinity_enabled?: boolean
}

export interface GroupEffectiveConfigDto {
  first_byte_timeout: number
  request_timeout: number
  stream_idle_timeout: number
  header_rules: HeaderRulesDto
  inject_usage_options: boolean
  affinity_enabled: boolean
}

export type {
  GroupModelItemDto,
  GroupModelsDto,
  GroupSettingsDto,
  GroupSummaryDto,
} from '@/api/control/types'

export type GroupSettingsUpdateRequest = Partial<{
  name: string
  params: ChannelParamsDto
  validation_model: string | null
  enabled: boolean
  weight_manual: number | null
  overrides: GroupRuntimeConfigDto
}>

export interface AccessKeyReferenceDto {
  id: number
  name: string
}

export interface GroupInUseData {
  access_keys: AccessKeyReferenceDto[]
}

export interface ModelDiscoveryRequest {
  channel_id: string
  connection_type: ConnectionType
  params: ChannelParamsDto
  credentials?: string
  staged_credential_id?: string
}

export interface ModelDiscoveryResult {
  models: ModelCandidate[]
}

export interface GroupModelUpdateDto {
  id: string
  alias: string
  alias_enabled: boolean
}

export interface GroupModelsReplaceRequest {
  models: GroupModelUpdateDto[]
}

export interface GroupCreateRequest {
  name?: string
  channel_id: string
  connection_type: ConnectionType
  params: ChannelParamsDto
  models: GroupModelUpdateDto[]
  credentials?: string
  staged_credential_ids?: string[]
  confirm_same_target: boolean
}

export interface GroupCreateResult {
  group_id: number
  group_name: string
  credentials_added: number
  credentials_duplicated: number
}

export interface CredentialImportRequest {
  credentials: string
}

export interface CredentialImportResult {
  group_id: number
  credentials_added: number
  credentials_duplicated: number
}

const credentialValidationReasonCodes = [
  'unknown_field',
  'required',
  'invalid_type',
  'invalid_json',
  'missing_required_field',
  'conflicting_auth_methods',
  'incomplete_auth_method',
  'invalid_value',
] as const

export interface CredentialValidationData {
  entry: number
  field: string
  reason_code: (typeof credentialValidationReasonCodes)[number]
}

export interface SameTargetConflictData {
  groups: Array<{ id: number; name: string }>
}

export function readCredentialValidationData(value: unknown): CredentialValidationData | null {
  try {
    const record = projectRecord(value)
    assertNoSecretLikeFields(record, ['entry', 'field', 'reason_code'])
    return {
      entry: projectSafeInteger(record.entry, { minimum: 1, maximum: 1_000 }),
      field: projectNonBlankString(record.field),
      reason_code: projectEnum(record.reason_code, credentialValidationReasonCodes),
    }
  } catch {
    return null
  }
}

function projectNonBlankString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0) throw new InvalidResponseError()
  return result
}

function projectChannelParams(value: unknown): ChannelParamsDto {
  const record = projectRecord(value)
  const params: ChannelParamsDto = {}
  for (const [key, paramValue] of Object.entries(record)) {
    if (key !== key.trim() || !/^[a-z][a-z0-9_]*$/u.test(key)) throw new InvalidResponseError()
    params[key] = projectString(paramValue)
  }
  return params
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

  for (const field of ['first_byte_timeout', 'request_timeout', 'stream_idle_timeout'] as const) {
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
  if (complete || Object.prototype.hasOwnProperty.call(record, 'affinity_enabled')) {
    result.affinity_enabled = projectBoolean(record.affinity_enabled)
  }
  return result as GroupRuntimeConfigDto | GroupEffectiveConfigDto
}

export function projectGroupSummary(value: unknown): GroupSummaryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupSummaryFields)
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    channel_id: projectChannelID(record.channel_id),
    connection_type: projectEnum(record.connection_type, connectionTypes),
    params: projectChannelParams(record.params),
    service_status: projectEnum(record.service_status, groupCollectionStatuses),
    credential_count: projectSafeInteger(record.credential_count, { minimum: 0 }),
    model_count: projectSafeInteger(record.model_count, { minimum: 0 }),
  }
}

export function projectGroupSettings(value: unknown): GroupSettingsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupSettingsFields)
  return {
    name: projectNonBlankString(record.name),
    channel_id: projectChannelID(record.channel_id),
    connection_type: projectEnum(record.connection_type, connectionTypes),
    params: projectChannelParams(record.params),
    validation_model:
      record.validation_model === null ? null : projectNonBlankString(record.validation_model),
    enabled: projectBoolean(record.enabled),
    weight_manual:
      record.weight_manual === null
        ? null
        : projectSafeInteger(record.weight_manual, { minimum: 1, maximum: 100 }),
    overrides: projectRuntimeConfig(record.overrides, false),
    effective: projectRuntimeConfig(record.effective, true),
  }
}

function projectGroupModelItem(value: unknown): GroupModelItemDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupModelItemFields)
  const alias = projectString(record.alias, { allowEmpty: true })
  const aliasEnabled = projectBoolean(record.alias_enabled)
  const id = projectNonBlankString(record.id)
  const clientModel = projectNonBlankString(record.client_model)
  if ((alias !== '') !== aliasEnabled || clientModel !== (aliasEnabled ? alias : id)) {
    throw new InvalidResponseError()
  }
  return {
    id,
    alias,
    alias_enabled: aliasEnabled,
    client_model: clientModel,
    pricing_status: projectEnum(record.pricing_status, ['pending', 'configured'] as const),
  }
}

export function projectGroupModels(value: unknown): GroupModelsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupModelsFields)
  const items = projectArray(record.items, projectGroupModelItem)
  const total = projectSafeInteger(record.total, { minimum: 0 })
  const pending = projectSafeInteger(record.pending, { minimum: 0 })
  if (
    items.length !== total ||
    pending > total ||
    new Set(items.map(({ client_model }) => client_model)).size !== items.length ||
    items.filter(({ pricing_status }) => pricing_status === 'pending').length !== pending
  ) {
    throw new InvalidResponseError()
  }
  return { items, total, pending }
}

function projectCredentialCounts(value: unknown): CredentialCounts {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, credentialCountFields)
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
  const status = projectEnum(record.status, groupCollectionStatuses) as GroupCollectionStatus
  const credentialCounts = projectCredentialCounts(record.credential_counts)
  const modelCount = projectSafeInteger(record.model_count, { minimum: 0 })
  if (status === 'disabled' && credentialCounts.disabled !== credentialCounts.total) {
    throw new InvalidResponseError()
  }
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    channel_id: projectChannelID(record.channel_id),
    connection_type: projectEnum(record.connection_type, connectionTypes),
    params: projectChannelParams(record.params),
    status,
    model_count: modelCount,
    credential_counts: credentialCounts,
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
    channel_id: projectChannelID(record.channel_id),
    connection_type: projectEnum(record.connection_type, connectionTypes),
    params: projectChannelParams(record.params),
    enabled: projectBoolean(record.enabled),
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

function projectDiscoveryResult(value: unknown): ModelDiscoveryResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['models'])
  const models = projectArray(record.models, projectModelCandidate)
  if (new Set(models.map(({ id }) => id)).size !== models.length) throw new InvalidResponseError()
  return { models }
}

function projectGroupCreateResult(value: unknown): GroupCreateResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'group_id',
    'group_name',
    'credentials_added',
    'credentials_duplicated',
  ])
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    credentials_added: projectSafeInteger(record.credentials_added, { minimum: 0 }),
    credentials_duplicated: projectSafeInteger(record.credentials_duplicated, { minimum: 0 }),
  }
}

function projectCredentialImportResult(value: unknown): CredentialImportResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['group_id', 'credentials_added', 'credentials_duplicated'])
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    credentials_added: projectSafeInteger(record.credentials_added, { minimum: 0 }),
    credentials_duplicated: projectSafeInteger(record.credentials_duplicated, { minimum: 0 }),
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

export function isSameTargetConflictData(value: unknown): value is SameTargetConflictData {
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
  if (normalized.connection_type !== undefined) {
    params.set('connection_type', normalized.connection_type)
  }
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

export async function getGroupSummary(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<GroupSummaryDto> {
  return projectGroupSummary(
    await client.request(`/api/groups/${groupID}`, { method: 'GET', signal }),
  )
}

export async function getGroupSettings(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<GroupSettingsDto> {
  return projectGroupSettings(
    await client.request(`/api/groups/${groupID}/settings`, { method: 'GET', signal }),
  )
}

export async function getGroupModels(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<GroupModelsDto> {
  return projectGroupModels(
    await client.request(`/api/groups/${groupID}/models`, { method: 'GET', signal }),
  )
}

const manualGroupQueryOptions = {
  staleTime: Number.POSITIVE_INFINITY,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
} as const

export function groupCollectionQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<GroupCollectionFilters>,
) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: computed(() => controlQueryKeys.groups.collection(toValue(filters))),
    queryFn: ({ queryKey, signal }) => listGroupCollection(client, queryKey[3], signal),
    placeholderData: keepPreviousData,
  })
}

export function groupOptionsQueryOptions(
  client: ApiClient,
  enabled: MaybeRefOrGetter<boolean> = true,
) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: controlQueryKeys.groups.options(),
    queryFn: ({ signal }) => listGroupOptions(client, signal),
    enabled: computed(() => toValue(enabled)),
  })
}

export function groupSummaryQueryOptions(
  client: ApiClient,
  groupID: MaybeRefOrGetter<number | undefined>,
) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: computed(() => {
      const id = toValue(groupID)
      return id === undefined
        ? controlQueryKeys.groups.summaries()
        : controlQueryKeys.groups.summary(id)
    }),
    queryFn: ({ signal }) => {
      const id = toValue(groupID)
      if (id === undefined) throw new InvalidResponseError()
      return getGroupSummary(client, id, signal)
    },
    enabled: computed(() => toValue(groupID) !== undefined),
  })
}

export function groupSettingsQueryOptions(
  client: ApiClient,
  groupID: MaybeRefOrGetter<number | undefined>,
) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: computed(() => {
      const id = toValue(groupID)
      return id === undefined
        ? controlQueryKeys.groups.settingsAll()
        : controlQueryKeys.groups.settings(id)
    }),
    queryFn: ({ signal }) => {
      const id = toValue(groupID)
      if (id === undefined) throw new InvalidResponseError()
      return getGroupSettings(client, id, signal)
    },
    enabled: computed(() => toValue(groupID) !== undefined),
  })
}

export function groupModelsQueryOptions(
  client: ApiClient,
  groupID: MaybeRefOrGetter<number | undefined>,
) {
  return queryOptions({
    ...manualGroupQueryOptions,
    queryKey: computed(() => {
      const id = toValue(groupID)
      return id === undefined
        ? controlQueryKeys.groups.modelsAll()
        : controlQueryKeys.groups.models(id)
    }),
    queryFn: ({ signal }) => {
      const id = toValue(groupID)
      if (id === undefined) throw new InvalidResponseError()
      return getGroupModels(client, id, signal)
    },
    enabled: computed(() => toValue(groupID) !== undefined),
  })
}

export async function updateGroupSettings(
  client: ApiClient,
  groupID: number,
  body: GroupSettingsUpdateRequest,
  signal?: AbortSignal,
): Promise<GroupSettingsDto> {
  return projectGroupSettings(
    await client.request(`/api/groups/${groupID}/settings`, {
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

export async function replaceGroupModelsResource(
  client: ApiClient,
  groupID: number,
  body: GroupModelsReplaceRequest,
  signal?: AbortSignal,
): Promise<GroupModelsDto> {
  return projectGroupModels(
    await client.request(`/api/groups/${groupID}/models`, {
      method: 'PUT',
      json: body,
      signal,
    }),
  )
}

/** Cache writes are exact and never cause a background refetch. */
export function cacheGroupSettings(
  queryClient: QueryClient,
  groupID: number,
  settings: GroupSettingsDto,
): void {
  queryClient.setQueryData(controlQueryKeys.groups.settings(groupID), settings)
}

/** A settings mutation can change summary status; leave only that resource stale for manual refresh. */
export async function invalidateGroupSummary(
  queryClient: QueryClient,
  groupID: number,
): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.groups.summary(groupID),
      exact: true,
      refetchType: 'none',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.modelPrices(),
      refetchType: 'none',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.models.all,
      refetchType: 'none',
    }),
  ])
}

/** Settings can change every Group representation; refresh active consumers and stale the rest. */
export async function invalidateGroupSettingsDependents(
  queryClient: QueryClient,
  groupID: number,
): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.groups.summary(groupID),
      exact: true,
      refetchType: 'active',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.groups.models(groupID),
      exact: true,
      refetchType: 'active',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.groups.credentialsAll(groupID),
      refetchType: 'active',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.groups.collectionAll,
      refetchType: 'active',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.groups.options(),
      exact: true,
      refetchType: 'active',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.modelPrices(),
      refetchType: 'active',
    }),
    queryClient.invalidateQueries({
      queryKey: controlQueryKeys.models.all,
      refetchType: 'active',
    }),
  ])
}

export function cacheGroupModels(
  queryClient: QueryClient,
  groupID: number,
  models: GroupModelsDto,
): void {
  queryClient.setQueryData(controlQueryKeys.groups.models(groupID), models)
  queryClient.setQueryData<GroupSummaryDto>(controlQueryKeys.groups.summary(groupID), (summary) =>
    summary === undefined ? summary : { ...summary, model_count: models.total },
  )
}

/** Remove every cache entry whose identity is the deleted Group, including filtered key pages. */
export function clearGroupResourceCaches(queryClient: QueryClient, groupID: number): void {
  queryClient.removeQueries({ queryKey: controlQueryKeys.groups.summary(groupID), exact: true })
  queryClient.removeQueries({ queryKey: controlQueryKeys.groups.models(groupID), exact: true })
  queryClient.removeQueries({ queryKey: controlQueryKeys.groups.settings(groupID), exact: true })
  queryClient.removeQueries({
    queryKey: controlQueryKeys.groups.credentialsAll(groupID),
    exact: false,
  })
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

export async function importGroupCredentials(
  client: ApiClient,
  groupID: number,
  body: CredentialImportRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<CredentialImportResult> {
  return projectCredentialImportResult(
    await client.request(`/api/groups/${groupID}/credentials/import`, {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      json: body,
      signal,
    }),
  )
}
