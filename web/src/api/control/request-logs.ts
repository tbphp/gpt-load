import type { ApiClient } from '@/api/client'

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
}

export interface RequestLogPageDto {
  items: RequestLogItemDto[]
  next_cursor: string | null
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

  return client.request<RequestLogPageDto>(path, { method: 'GET', signal })
}
