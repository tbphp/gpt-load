export type GroupProtocol = 'openai' | 'anthropic' | 'gemini'
export type AccessProtocol = GroupProtocol | 'openai-response'

export interface GroupModelDto {
  id: string
  alias: string
}

export interface GroupSummary {
  id: number
  name: string
  upstream_url: string
  protocols: GroupProtocol[]
  models: GroupModelDto[]
  enabled: boolean
  key_count: number
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
  at: string | null
}

export interface HealthProblemKeyDto {
  key_id: number
  group_id: number
  group_name: string
  cooldown_until?: string
  failure_count: number
  recent_success_count: number
  recent_failure_count: number
  consecutive_failure_count: number
  weight_manual: number | null
  weight_auto: number
  recovery: HealthRecoveryDto
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
  last_write_failure_at: string | null
  last_retention_failure_at: string | null
}

export interface RuntimeHealthDto {
  observed_at: string
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
  key: string
  status: 'active' | 'disabled'
  filters: AccessKeyFiltersDto
  rpm_limit: number
}

export interface AccessKeyOptionDto {
  id: number
  name: string
  status: AccessKeyDto['status']
}
