import type { ProtocolValue } from './protocols'

export type GroupProtocol = ProtocolValue
export type AccessProtocol = ProtocolValue
export const routeStrategies = ['native_first', 'weighted_mix'] as const
export type RouteStrategy = (typeof routeStrategies)[number]
export type FailureCategory =
  | 'ok'
  | 'rate_limited'
  | 'model_unavailable'
  | 'invalid_key'
  | 'upstream_host_error'
  | 'client_error'
  | 'downstream_cancel'
  | 'authentication_required'
  | 'ambiguous'

export type GroupCollectionStatus = 'available' | 'unavailable' | 'disabled'
export type GroupUnavailableReason = 'no_available_credentials' | 'no_models'
export type GroupCollectionSort = 'recent' | 'status' | 'name' | 'credentials' | 'created'
export type ModelPricingStatus = 'pending' | 'configured'
export type ChannelParamsDto = Record<string, string>
export type ConnectionType = 'api_key' | 'subscription'
export type ProxyConfiguredMode = 'inherit' | 'direct' | 'custom'
export type ProxyEffectiveMode = 'direct' | 'environment' | 'custom'
export type ProxyEffectiveSource = 'credential' | 'group' | 'global' | 'environment' | 'default'

export interface ProxyViewDto {
  configured_mode: ProxyConfiguredMode
  effective_mode: ProxyEffectiveMode
  effective_source: ProxyEffectiveSource
  display_url?: string
  has_auth: boolean
}

export type ProxyConfigInput = { mode: 'direct' } | { mode: 'custom'; url: string }
export type ProxyMutation = ProxyConfigInput | null

export interface GroupCollectionFilters {
  q?: string
  status?: GroupCollectionStatus
  connection_type?: ConnectionType
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
  service_status_reason: GroupUnavailableReason | null
  credential_count: number
  model_count: number
}

export interface HeaderRulesDto {
  set: Record<string, string>
  remove: string[]
}

export type ParameterJSONValue =
  null | boolean | number | string | unknown[] | Record<string, unknown>

export interface ParameterOverrideMatchDto {
  protocol?: AccessProtocol
  model?: string
}

export interface ParameterOverrideRuleDto {
  match: ParameterOverrideMatchDto
  set?: Record<string, ParameterJSONValue>
  remove?: string[]
}

export interface GroupRuntimeConfigDto {
  first_byte_timeout?: number
  request_timeout?: number
  stream_idle_timeout?: number
  retry_count?: number
  blacklist_threshold?: number
  header_rules?: HeaderRulesDto
  inject_usage_options?: boolean
  affinity_enabled?: boolean
  parameter_overrides?: ParameterOverrideRuleDto[]
}

export interface GroupEffectiveConfigDto {
  first_byte_timeout: number
  request_timeout: number
  stream_idle_timeout: number
  retry_count: number
  blacklist_threshold: number
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
  proxy: ProxyViewDto
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
  email?: string
  email_mask?: string
  expires_at_ms?: number
  last_refresh_at_ms?: number
}

export interface CredentialQuotaWindowDto {
  id: string
  label: string
  label_key?: CredentialQuotaLabelKey
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

export type CredentialQuotaLabelKey =
  'session' | 'weekly' | 'extra_usage' | 'included_usage' | 'pay_as_you_go' | 'oauth_apps'

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

export interface CredentialObservationAccountSummaryDto {
  display_name?: string
  email?: string
  organization_name?: string
  organization_type?: string
  organization_role?: string
  workspace_role?: string
  organization_rate_limit_tier?: string
  user_rate_limit_tier?: string
  seat_tier?: string
  billing_type?: string
  extra_usage_enabled?: boolean
  extra_usage_disabled_reason?: string
  account_created_at_ms?: number
  subscription_created_at_ms?: number
}

export type CredentialPlanLevel = 'free' | 'standard' | 'premium' | 'elite'

export interface CredentialObservationSnapshotDto {
  plan_summary: { name?: string; level?: CredentialPlanLevel }
  account_summary?: CredentialObservationAccountSummaryDto
  quota_windows: CredentialQuotaWindowDto[]
  reset_credits_available?: number
  reset_credits?: CredentialResetCreditDto[]
}

export interface CredentialResetCreditDto {
  expires_at_ms?: number
}

export interface CredentialObservationDto {
  state: CredentialObservationState
  snapshot: CredentialObservationSnapshotDto | null
  observation_version: number
  observed_at_ms: number | null
  last_attempt_at_ms: number | null
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
  last_used_at_ms?: number
  daily_usage?: CredentialDailyUsageDto
  recovery: CredentialRecoveryDto
  proxy: ProxyViewDto
}

/** 固定 24 小时窗口的上游尝试结果分布，来源是小时聚合而非 health 的 5 分钟内存窗口。 */
export interface CredentialDailyUsageDto {
  window_seconds: number
  success_count: number
  failure_count: number
  /** false 表示统计数据未覆盖完整窗口，计数可能偏低。 */
  data_complete: boolean
}

export interface CredentialDetailDto {
  credential: CredentialItemDto
  observation: CredentialObservationDto
}

export interface CredentialRevealDto {
  credential_id: number
  credential: Record<string, string>
  revealed_at_ms: number
}

export interface CredentialDownloadDto {
  filename: string
  credential: Record<string, unknown>
}

export interface CredentialDownloadAllDto {
  files: CredentialDownloadDto[]
}

export type CredentialTestOutcome = 'passed' | 'failed' | 'inconclusive'

export type CredentialTestReason =
  | 'invalid_credential'
  | 'model_unavailable'
  | 'rate_limited'
  | 'timeout'
  | 'upstream_error'
  | 'probe_incompatible'
  | 'unknown'

export interface CredentialTestResultDto {
  outcome: CredentialTestOutcome
  model: string
  protocol: ProtocolValue
  latency_ms: number
  reason: CredentialTestReason | null
  can_restore: boolean
  restore_proof: string | null
  tested_at_ms: number
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
  /** API 密钥仍是掩码，订阅账号给完整邮箱，与凭据卡片、日志的展示约定一致。 */
  identity: string
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
  access_quota_checkpoint_write_failure_total: number
  access_quota_checkpoint_degraded: boolean
  retention_delete_failure_total: number
  queue_depth: number
  queue_capacity: number
  last_write_failure_at_ms: number | null
  last_access_quota_checkpoint_write_failure_at_ms: number | null
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
  low_quota_credentials: HealthQuotaCredentialDto[]
  expiring_reset_credits: HealthExpiringResetCreditDto[]
  blocked_access_keys: HealthAccessKeyCostLimitDto[]
  request_log: RequestLogHealthDto
}

export interface HealthExpiringResetCreditDto {
  credential_id: number
  group_id: number
  group_name: string
  count: number
  nearest_expires_at_ms: number
}

export interface HealthQuotaCredentialDto {
  credential_id: number
  group_id: number
  group_name: string
  /** 剩余额度比例，0..1 */
  remaining: number
  reset_at_ms: number
}

export interface AccessKeyFiltersDto {
  groups: number[]
  protocols: AccessProtocol[]
  models: string[]
  allowed_cidrs: string[]
}

export type AccessKeyCostLimitKind = 'total' | 'periodic'
export type AccessKeyCostLimitRuleState = 'available' | 'inactive' | 'exhausted'

export interface AccessKeyCostLimitRuleDto {
  id: number
  kind: AccessKeyCostLimitKind
  limit_usd: string
  period_seconds: number
}

export interface AccessKeyCostLimitRuleStatusDto extends AccessKeyCostLimitRuleDto {
  used_usd: string
  remaining_usd: string
  status: AccessKeyCostLimitRuleState
  window_started_at_ms: number | null
  window_ends_at_ms: number | null
}

export interface AccessKeyCostLimitStatusDto {
  observed_at_ms: number
  allowed: boolean
  recoverable: boolean
  next_available_at_ms: number | null
  rules: AccessKeyCostLimitRuleStatusDto[]
}

export interface HealthAccessKeyCostLimitDto {
  access_key_id: number
  name: string
  masked_key: string
  recoverable: boolean
  next_available_at_ms: number | null
  blocking_rules: AccessKeyCostLimitRuleStatusDto[]
}

export interface AccessKeyDto {
  id: number
  name: string
  masked_key: string
  status: 'active' | 'disabled'
  filters: AccessKeyFiltersDto
  expires_at_ms: number | null
  rpm_limit: number
  cost_limit_rules: AccessKeyCostLimitRuleDto[]
  cost_limit_status: AccessKeyCostLimitStatusDto | null
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
  expired: boolean
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

export interface AccessKeyRotateResultDto extends AccessKeyDto {
  key?: string
  replayed: boolean
}

export interface AccessKeyRevealDto {
  id: number
  key: string
  revealed_at_ms: number
}
