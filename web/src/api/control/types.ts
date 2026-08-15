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
export type GroupCollectionSort = 'status' | 'name' | 'credentials' | 'created'
export type ModelPricingStatus = 'pending' | 'configured'
export type ChannelParamsDto = Record<string, string>
export type ConnectionType = 'api_key' | 'subscription'

export interface GroupCollectionFilters {
  q?: string
  status?: GroupCollectionStatus
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
  channel_id: string
  connection_type: ConnectionType
  params: ChannelParamsDto
  status: GroupCollectionStatus
  model_count: number
  credential_counts: CredentialCounts
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
  channel_id: string
  connection_type: ConnectionType
  params: ChannelParamsDto
  service_status: GroupCollectionStatus
  credential_count: number
  model_count: number
}

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

export interface GroupSettingsDto {
  name: string
  channel_id: string
  connection_type: ConnectionType
  params: ChannelParamsDto
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

export type CredentialStatus = 'available' | 'cooldown' | 'blacklisted' | 'disabled'
export type CredentialConfiguredStatus = 'active' | 'disabled'
export type CredentialWeightMode = 'auto' | 'manual'
export type CredentialRecoveryMode = 'none' | 'cooldown' | 'probe' | 'manual'
export type CredentialAuthState =
  'ready' | 'refreshing' | 'reauthorization_required' | 'outcome_unknown'
export type CredentialObservationState = 'fresh' | 'stale' | 'refreshing' | 'error' | 'unavailable'

export interface CredentialAccountDto {
  email_mask?: string
  expires_at_ms?: number
  last_refresh_at_ms?: number
}

export interface CredentialQuotaWindowDto {
  id: string
  label: string
  scope: string
  unit: string
  used?: number
  limit?: number
  remaining?: number
  utilization?: number
  reset_at_ms?: number
  window_seconds?: number
  model_ids?: string[]
  state: 'available' | 'exhausted' | 'unknown'
  is_primary?: boolean
  observed_usage?: CredentialObservedWindowUsageDto
}

export interface CredentialObservedWindowUsageDto {
  window_start_ms: number
  window_end_ms: number
  source: 'request_logs' | 'usage_stats'
  data_complete: boolean
  usage_complete: boolean
  pricing_complete: boolean
  request_count: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  estimated_reference_cost_nano_usd: string
  last_used_at_ms?: number
}

export interface CredentialObservationSnapshotDto {
  plan_summary: { name?: string }
  quota_windows: CredentialQuotaWindowDto[]
  reset_credits_available?: number
  reset_credits?: CredentialResetCreditDto[]
}

export interface CredentialResetCreditDto {
  expires_at_ms: number
}

export interface CredentialObservationDto {
  state: CredentialObservationState
  snapshot: CredentialObservationSnapshotDto | null
  observation_version: number
  observed_at_ms: number | null
  fresh_until_ms: number | null
  last_attempt_at_ms: number | null
  next_allowed_at_ms: number | null
  last_error_code?: string
}

export interface CredentialResetCreditConsumeDto {
  status: 'succeeded'
  windows_reset: number
  redeemed_at_ms?: number
  observation?: CredentialObservationDto
  observation_pending?: boolean
  replayed: boolean
}

export interface CredentialRecoveryDto {
  mode: CredentialRecoveryMode
  automatic: boolean
  at_ms: number | null
}

export interface CredentialItemDto {
  credential_id: number
  connection_type: ConnectionType
  secret_version: number
  mask: string
  account: CredentialAccountDto
  auth_state: CredentialAuthState
  auth_error_code?: string
  observation?: CredentialObservationDto
  configured_status: CredentialConfiguredStatus
  effective_status: CredentialStatus
  weight_mode: CredentialWeightMode
  weight: number | null
  recent_success_count: number
  recent_failure_count: number
  consecutive_failure_count: number
  last_failure_category: FailureCategory
  last_status_code: number | null
  cooldown_until_ms: number | null
  daily_usage?: CredentialDailyUsageDto
  recovery: CredentialRecoveryDto
}

/** 固定 24 小时窗口的请求结果分布，来源是请求日志而非 health 的 5 分钟内存窗口。 */
export interface CredentialDailyUsageDto {
  window_seconds: number
  success_count: number
  failure_count: number
  /** false 表示请求日志留存期短于该窗口，计数偏低。 */
  data_complete: boolean
}

export interface CredentialRevealDto {
  credential_id: number
  credential: Record<string, string>
  revealed_at_ms: number
}

export interface CredentialSummaryDto {
  total: number
  available: number
  cooldown: number
  blacklisted: number
  disabled: number
}

export interface CredentialPaginationDto {
  page: number
  page_size: 20 | 50 | 100
  total_items: number
  total_pages: number
}

export interface CredentialCollectionDto {
  observed_at_ms: number
  stats_window_seconds: number
  summary: CredentialSummaryDto
  items: CredentialItemDto[]
  pagination: CredentialPaginationDto
}

export interface CredentialCollectionFilters {
  q?: string
  status?: CredentialStatus
  page: number
  page_size: 20 | 50 | 100
}

export interface CredentialBatchResultDto {
  affected_credential_ids: number[]
  summary: CredentialSummaryDto
}

export interface GroupOptionDto {
  id: number
  name: string
  channel_id: string
  connection_type: ConnectionType
  params: ChannelParamsDto
  enabled: boolean
  models: string[]
}

export interface CredentialCounts {
  total: number
  available: number
  cooldown: number
  blacklisted: number
  disabled: number
}

export interface HealthCredentialCountsDto {
  credentials: number
  available: number
  cooldown: number
  blacklisted: number
}

export interface HealthGroupDto {
  id: number
  name: string
  enabled: boolean
  counts: HealthCredentialCountsDto
}

export interface HealthRecoveryDto {
  automatic: boolean
  mode: 'cooldown_expiry' | 'validation_probe' | 'configuration_required'
  at_ms: number | null
}

export interface HealthProblemCredentialDto {
  credential_id: number
  group_id: number
  group_name: string
  cooldown_until_ms: number | null
  failure_count: number
  recent_success_count: number
  recent_problem_count: number
  consecutive_problem_count: number
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
  counts: HealthCredentialCountsDto
  groups: HealthGroupDto[]
  cooldown_credentials: HealthProblemCredentialDto[]
  blacklisted_credentials: HealthProblemCredentialDto[]
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
