import type { ProtocolValue } from './protocols'

export type GroupProtocol = ProtocolValue
export type AccessProtocol = ProtocolValue
export type FailureCategory =
  | 'ok'
  | 'rate_limited'
  | 'model_unavailable'
  | 'invalid_key'
  | 'upstream_host_error'
  | 'client_error'
  | 'downstream_cancel'
  | 'ambiguous'

export type GroupCollectionStatus = 'available' | 'unavailable' | 'disabled'
export type GroupCollectionSort = 'status' | 'name' | 'keys' | 'created'
export type ModelPricingStatus = 'pending' | 'configured'

export interface GroupCollectionFilters {
  q?: string
  status?: GroupCollectionStatus
  protocol?: GroupProtocol
  sort: GroupCollectionSort
  page: number
  page_size: 20
}

export interface GroupCollectionSummaryDto {
  total: number
  available: number
  unavailable: number
  disabled: number
}

export interface GroupCollectionItemDto {
  id: number
  name: string
  provider_id: string | null
  status: GroupCollectionStatus
  upstream_url: string
  protocols: GroupProtocol[]
  model_count: number
  key_counts: KeyCounts
}

export interface GroupCollectionPaginationDto {
  page: number
  page_size: 20
  total_items: number
  total_pages: number
}

export interface GroupCollectionResponseDto {
  observed_at_ms: number
  summary: GroupCollectionSummaryDto
  items: GroupCollectionItemDto[]
  pagination: GroupCollectionPaginationDto
}

export interface GroupSummaryDto {
  id: number
  name: string
  provider_id: string | null
  service_status: GroupCollectionStatus
  upstream_url: string
  protocols: GroupProtocol[]
  key_count: number
  model_count: number
}

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

export interface GroupSettingsDto {
  name: string
  provider_id: string | null
  upstream_url: string
  protocols: GroupProtocol[]
  validation_model: string | null
  enabled: boolean
  weight_manual: number | null
  overrides: GroupRuntimeConfigDto
  effective: GroupEffectiveConfigDto
}

export interface GroupModelItemDto {
  id: string
  alias: string
  alias_enabled: boolean
  client_model: string
  pricing_status: ModelPricingStatus
}

export interface GroupModelsDto {
  items: GroupModelItemDto[]
  total: number
  pending: number
}

export type GroupKeyStatus = 'available' | 'cooldown' | 'blacklisted' | 'disabled'
export type GroupKeyConfiguredStatus = 'active' | 'disabled'
export type GroupKeyWeightMode = 'auto' | 'manual'
export type GroupKeyRecoveryMode = 'none' | 'cooldown' | 'probe' | 'manual'

export interface GroupKeyRecoveryDto {
  mode: GroupKeyRecoveryMode
  automatic: boolean
  at_ms: number | null
}

export interface GroupKeyItemDto {
  id: number
  mask: string
  configured_status: GroupKeyConfiguredStatus
  effective_status: GroupKeyStatus
  weight_mode: GroupKeyWeightMode
  weight: number | null
  recent_success_count: number
  recent_failure_count: number
  consecutive_failure_count: number
  last_failure_category: FailureCategory
  last_status_code: number | null
  cooldown_until_ms: number | null
  recovery: GroupKeyRecoveryDto
}

export interface GroupKeyRevealDto {
  id: number
  key: string
  revealed_at_ms: number
}

export interface GroupKeySummaryDto {
  total: number
  available: number
  cooldown: number
  blacklisted: number
  disabled: number
}

export interface GroupKeyPaginationDto {
  page: number
  page_size: 20 | 50 | 100
  total_items: number
  total_pages: number
}

export interface GroupKeyCollectionDto {
  observed_at_ms: number
  stats_window_seconds: number
  summary: GroupKeySummaryDto
  items: GroupKeyItemDto[]
  pagination: GroupKeyPaginationDto
}

export interface GroupKeyCollectionFilters {
  q?: string
  status?: GroupKeyStatus
  page: number
  page_size: 20 | 50 | 100
}

export interface GroupKeyBatchResultDto {
  affected_ids: number[]
  summary: GroupKeySummaryDto
}

export interface GroupOptionDto {
  id: number
  name: string
  enabled: boolean
  protocols: GroupProtocol[]
  models: string[]
}

export interface KeyCounts {
  total: number
  available: number
  cooldown: number
  blacklisted: number
  disabled: number
}

export interface HealthGroupDto {
  id: number
  name: string
  enabled: boolean
  counts: KeyCounts
}

export interface HealthRecoveryDto {
  automatic: boolean
  mode: string
  at_ms: number | null
}

export interface HealthProblemKeyDto {
  key_id: number
  group_id: number
  group_name: string
  cooldown_until_ms: number | null
  failure_count: number
  recent_success_count: number
  recent_failure_count: number
  consecutive_failure_count: number
  weight_manual: number | null
  weight_auto: number
  recovery: HealthRecoveryDto
  mask: string
  last_failure_category: Exclude<FailureCategory, 'ok'>
  last_status_code: number | null
}

export interface RequestLogHealthDto {
  enqueued_total: number
  persisted_total: number
  dropped_not_running_total: number
  dropped_queue_full_total: number
  dropped_stopping_total: number
  dropped_persist_failed_total: number
  dropped_shutdown_total: number
  dropped_total: number
  write_failure_total: number
  retention_delete_failure_total: number
  queue_depth: number
  queue_capacity: number
  last_write_failure_at_ms: number | null
  last_retention_failure_at_ms: number | null
}

export interface RuntimeHealthDto {
  observed_at_ms: number
  version: string
  uptime_seconds: number
  snapshot_revision: number
  stats_window_seconds: number
  counts: KeyCounts
  groups: HealthGroupDto[]
  cooldown_keys: HealthProblemKeyDto[]
  blacklisted_keys: HealthProblemKeyDto[]
  request_log: RequestLogHealthDto
}

export interface AccessKeyFiltersDto {
  groups: number[]
  protocols: AccessProtocol[]
  models: string[]
}

export interface AccessKeyDto {
  id: number
  name: string
  masked_key: string
  status: 'active' | 'disabled'
  filters: AccessKeyFiltersDto
  rpm_limit: number
  created_at_ms: number
  updated_at_ms: number
}

export type AccessKeyCollectionStatus = AccessKeyDto['status']

export interface AccessKeyCollectionFilters {
  q?: string
  status?: AccessKeyCollectionStatus
  page: number
  page_size: 20
}

export interface AccessKeyCollectionSummaryDto {
  total: number
  active: number
  disabled: number
}

export interface AccessKeyCollectionItemDto extends AccessKeyDto {
  last_request_at_ms: number | null
}

export interface AccessKeyCollectionPaginationDto {
  page: number
  page_size: 20
  total_items: number
  total_pages: number
}

export interface AccessKeyCollectionResponseDto {
  summary: AccessKeyCollectionSummaryDto
  items: AccessKeyCollectionItemDto[]
  pagination: AccessKeyCollectionPaginationDto
}

export interface AccessKeyOptionDto {
  id: number
  name: string
  status: AccessKeyDto['status']
}

export interface AccessKeyCreateResultDto extends AccessKeyDto {
  key?: string
  replayed: boolean
}

export interface AccessKeyRevealDto {
  id: number
  key: string
  revealed_at_ms: number
}
