import type { ApiClient } from '@/api/client'

import type { GroupModelDto, GroupProtocol, GroupSummary } from './types'

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
}

export interface GroupEffectiveConfigDto {
  connect_timeout: number
  first_byte_timeout: number
  request_timeout: number
  stream_idle_timeout: number
  header_rules: HeaderRulesDto
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
  config: { header_rules: HeaderRulesDto }
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
  config: { header_rules: HeaderRulesDto }
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isIdName(value: unknown): value is { id: number; name: string } {
  return (
    isRecord(value) &&
    typeof value.id === 'number' &&
    Number.isSafeInteger(value.id) &&
    value.id > 0 &&
    typeof value.name === 'string' &&
    value.name.trim().length > 0
  )
}

export function isUpstreamUrlConflictData(value: unknown): value is UpstreamUrlConflictData {
  return (
    isRecord(value) &&
    Array.isArray(value.groups) &&
    value.groups.length > 0 &&
    value.groups.every(isIdName)
  )
}

export function isGroupInUseData(value: unknown): value is GroupInUseData {
  return (
    isRecord(value) &&
    Array.isArray(value.access_keys) &&
    value.access_keys.length > 0 &&
    value.access_keys.every(isIdName)
  )
}

export function listGroups(client: ApiClient, signal?: AbortSignal): Promise<GroupSummary[]> {
  return client.request<GroupSummary[]>('/api/groups', { method: 'GET', signal })
}

export function getGroup(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<GroupDetailDto> {
  return client.request<GroupDetailDto>(`/api/groups/${groupID}`, { method: 'GET', signal })
}

export function updateGroup(
  client: ApiClient,
  groupID: number,
  body: GroupUpdateRequest,
  signal?: AbortSignal,
): Promise<GroupUpdateResult> {
  return client.request<GroupUpdateResult>(`/api/groups/${groupID}`, {
    method: 'PUT',
    json: body,
    signal,
  })
}

export function deleteGroup(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<void> {
  return client.request<void>(`/api/groups/${groupID}`, { method: 'DELETE', signal })
}

export function discoverModels(
  client: ApiClient,
  body: ModelDiscoveryRequest,
  signal?: AbortSignal,
): Promise<ModelDiscoveryResult> {
  return client.request<ModelDiscoveryResult>('/api/models/discover', {
    method: 'POST',
    json: body,
    signal,
  })
}

export function discoverGroupModels(
  client: ApiClient,
  groupID: number,
  signal?: AbortSignal,
): Promise<ModelDiscoveryResult> {
  return client.request<ModelDiscoveryResult>(`/api/groups/${groupID}/models/discover`, {
    method: 'POST',
    signal,
  })
}

export function replaceGroupModels(
  client: ApiClient,
  groupID: number,
  body: GroupModelsReplaceRequest,
  signal?: AbortSignal,
): Promise<GroupDetailDto> {
  return client.request<GroupDetailDto>(`/api/groups/${groupID}/models`, {
    method: 'PUT',
    json: body,
    signal,
  })
}

export function createGroup(
  client: ApiClient,
  body: GroupCreateRequest,
  signal?: AbortSignal,
): Promise<GroupCreateResult> {
  return client.request<GroupCreateResult>('/api/groups', {
    method: 'POST',
    json: body,
    signal,
  })
}

export function importGroupKeys(
  client: ApiClient,
  groupID: number,
  body: GroupKeyImportRequest,
  signal?: AbortSignal,
): Promise<GroupKeyImportResult> {
  return client.request<GroupKeyImportResult>(`/api/groups/${groupID}/keys/import`, {
    method: 'POST',
    json: body,
    signal,
  })
}
