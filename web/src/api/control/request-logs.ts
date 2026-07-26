import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import type { AccessProtocol } from './types'

export type RequestLogStatus = 'success' | 'error' | 'incomplete' | 'canceled'

export type FailureCategory =
  | 'ok'
  | 'rate_limited'
  | 'model_unavailable'
  | 'invalid_key'
  | 'upstream_host_error'
  | 'client_error'
  | 'downstream_cancel'
  | 'ambiguous'

export type RequestLogAction = 'terminate' | 'retry' | 'cooldown_key' | 'fail_key' | 'skip_group'
export type RequestLogUsageState = 'complete' | 'partial' | 'missing' | 'not_applicable'
export type RequestLogCostState = 'priced' | 'unpriced' | 'not_applicable'

export interface RequestLogFilters {
  from?: string
  to?: string
  group_id?: number
  model?: string
  access_key_id?: number
  status?: RequestLogStatus
  request_id?: string
}

export interface RequestLogAttemptDto {
  sequence: number
  group_id: number
  group_name: string
  key_id: number
  key_mask: string
  upstream_model: string
  status_code: number
  duration_ms: number
  failure_category: FailureCategory
  action: RequestLogAction
  will_retry: boolean
  error_code: string
  error_summary: string
  committed: boolean
}

export interface RequestLogItemDto {
  request_id: string
  completed_at: string
  access_key: {
    id: number
    name: string | null
    deleted: boolean
  }
  protocol: AccessProtocol
  client_model: string
  upstream_model: string
  status: RequestLogStatus
  status_code: number
  duration_ms: number
  error_code: string
  error_summary: string
  affinity_hit: boolean
  attempts: RequestLogAttemptDto[]
  group_id: number | null
  usage_state: RequestLogUsageState
  cost_state: RequestLogCostState
  uncached_input_tokens: number
  cache_read_tokens: number
  cache_write_5m_tokens: number
  cache_write_1h_tokens: number
  output_tokens: number
  estimated_cost_usd: number
}

export interface RequestLogPageDto {
  items: RequestLogItemDto[]
  next_cursor: string | null
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isSafeNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

const tokenKeys = [
  'uncached_input_tokens',
  'cache_read_tokens',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'output_tokens',
] as const

function isValidUsageCostCombination(usageState: unknown, costState: unknown): boolean {
  return (
    ((usageState === 'complete' || usageState === 'partial') &&
      (costState === 'priced' || costState === 'unpriced')) ||
    (usageState === 'missing' && costState === 'unpriced') ||
    (usageState === 'not_applicable' && costState === 'not_applicable')
  )
}

function projectRequestLogItem(value: unknown): RequestLogItemDto {
  if (!isRecord(value)) throw new InvalidResponseError()
  if (
    (value.group_id !== null &&
      (!isSafeNonNegativeInteger(value.group_id) || value.group_id === 0)) ||
    !isValidUsageCostCombination(value.usage_state, value.cost_state) ||
    typeof value.estimated_cost_usd !== 'number' ||
    !Number.isFinite(value.estimated_cost_usd) ||
    value.estimated_cost_usd < 0 ||
    (value.cost_state !== 'priced' && value.estimated_cost_usd !== 0)
  ) {
    throw new InvalidResponseError()
  }
  for (const key of tokenKeys) {
    if (!isSafeNonNegativeInteger(value[key])) throw new InvalidResponseError()
  }
  return value as unknown as RequestLogItemDto
}

export function projectRequestLogPage(value: unknown): RequestLogPageDto {
  if (
    !isRecord(value) ||
    !Array.isArray(value.items) ||
    (value.next_cursor !== null && typeof value.next_cursor !== 'string')
  ) {
    throw new InvalidResponseError()
  }
  return { items: value.items.map(projectRequestLogItem), next_cursor: value.next_cursor }
}

export function listRequestLogs(
  client: ApiClient,
  filters: RequestLogFilters,
  cursor?: string,
  signal?: AbortSignal,
): Promise<RequestLogPageDto> {
  const params = new URLSearchParams()
  const values: Array<[string, string | number | undefined]> = [
    ['from', filters.from],
    ['to', filters.to],
    ['group_id', filters.group_id],
    ['model', filters.model],
    ['access_key_id', filters.access_key_id],
    ['status', filters.status],
    ['request_id', filters.request_id],
    ['cursor', cursor],
  ]
  for (const [key, value] of values) {
    if (value !== undefined) params.append(key, String(value))
  }
  const query = params.toString()
  const path: `/api/${string}` = query === '' ? '/api/logs' : `/api/logs?${query}`

  return client.request<unknown>(path, { method: 'GET', signal }).then(projectRequestLogPage)
}
