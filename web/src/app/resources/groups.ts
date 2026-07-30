import { queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { GroupModelDto, GroupProtocol, GroupSummary } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectHTTPURL,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

const groupSummaryFields = [
  'id',
  'name',
  'upstream_url',
  'protocols',
  'models',
  'enabled',
  'key_count',
] as const
const groupDetailFields = [
  ...groupSummaryFields,
  'validation_model',
  'weight_manual',
  'config',
  'effective_config',
] as const
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

export interface GroupDetailDto extends GroupSummary {
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

export function projectGroupSummary(value: unknown): GroupSummary {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupSummaryFields)
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
  }
}

export function projectGroupList(value: unknown): GroupSummary[] {
  return projectArray(value, projectGroupSummary)
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
    ...projectGroupSummary(record),
    validation_model: validationModel,
    weight_manual: weightManual,
    config: projectRuntimeConfig(record.config, false),
    effective_config: projectRuntimeConfig(record.effective_config, true),
  }
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

export async function listGroups(client: ApiClient, signal?: AbortSignal): Promise<GroupSummary[]> {
  return projectGroupList(await client.request('/api/groups', { method: 'GET', signal }))
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

export function groupListQueryOptions(client: ApiClient) {
  return queryOptions({
    queryKey: controlQueryKeys.groups.list(),
    queryFn: ({ signal }) => listGroups(client, signal),
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
