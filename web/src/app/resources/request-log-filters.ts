export const requestLogFilterFields = [
  'from_ms',
  'to_ms',
  'limit',
  'group_id',
  'channel_id',
  'credential_id',
  'client_model',
  'upstream_model',
  'access_key_id',
  'status',
  'request_id',
  'protocol',
  'stream',
  'final_status_code',
  'usage_state',
  'cost_state',
  'pricing_completeness',
  'cache_present',
  'attempt_status_code',
  'failure_category',
  'error_code',
  'retry_state',
  'retry_count_min',
  'retry_count_max',
  'first_response_min_ms',
  'first_response_max_ms',
  'duration_min_ms',
  'duration_max_ms',
  'input_tokens_min',
  'input_tokens_max',
  'output_tokens_min',
  'output_tokens_max',
  'cost_min_nano_usd',
  'cost_max_nano_usd',
] as const

type RequestLogFilterField = (typeof requestLogFilterFields)[number]
type RequestLogFilterRecord = Partial<Record<RequestLogFilterField, unknown>>

export function normalizeRequestLogFilters<T extends RequestLogFilterRecord>(filters: T): T {
  const normalized: RequestLogFilterRecord = {}
  for (const field of requestLogFilterFields) {
    const value = filters[field]
    if (value !== undefined) normalized[field] = value
  }
  return normalized as T
}
