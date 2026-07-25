import type { ApiClient } from '@/api/client'

import type { GroupModelDto, GroupSummary, Protocol } from './types'

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

export interface ModelDiscoveryRequest {
  upstream_url: string
  protocols: readonly Protocol[]
  keys: string
  config: { header_rules: HeaderRulesDto }
}

export interface ModelDiscoveryResult {
  models: string[]
}

export interface GroupCreateRequest {
  name?: string
  upstream_url: string
  protocols: readonly Protocol[]
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
    Number.isInteger(value.id) &&
    value.id > 0 &&
    typeof value.name === 'string'
  )
}

export function isUpstreamUrlConflictData(value: unknown): value is UpstreamUrlConflictData {
  return isRecord(value) && Array.isArray(value.groups) && value.groups.every(isIdName)
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
