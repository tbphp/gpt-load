import type { LocationQueryRaw } from 'vue-router'

import { enabledDataProtocols } from '@/api/control/protocols'
import type {
  RequestLogCostState,
  RequestLogFilters,
  RequestLogPageSize,
  RequestLogPricingCompleteness,
  RequestLogRetryState,
  RequestLogStatus,
  RequestLogUsageState,
} from '@/app/resources/request-logs'
import { requestLogFilterFields } from '@/app/resources/request-log-filters'

import { isValidMonitorText, maxSignedInt64 } from './filter-validation'

export interface LogFilterDraft {
  from: string
  to: string
  group_id: string
  status: string
  client_model: string
  upstream_model: string
  access_key_id: string
  request_id: string
  protocol: string
  stream: string
  final_status_code: string
  usage_state: string
  cost_state: string
  pricing_completeness: string
  cache_present: string
  upstream_key_id: string
  attempt_status_code: string
  failure_category: string
  error_code: string
  retry_state: string
  retry_count_min: string
  retry_count_max: string
  first_response_min_ms: string
  first_response_max_ms: string
  duration_min_ms: string
  duration_max_ms: string
  input_tokens_min: string
  input_tokens_max: string
  output_tokens_min: string
  output_tokens_max: string
  cost_min_usd: string
  cost_max_usd: string
}

export type LogFilterErrors = Partial<Record<keyof LogFilterDraft, string>>

export const requestLogStatuses = ['success', 'error', 'incomplete', 'canceled'] as const
export const requestLogUsageStates = ['complete', 'partial', 'missing', 'not_applicable'] as const
export const requestLogCostStates = ['priced', 'unpriced', 'not_applicable'] as const
export const requestLogPricingCompleteness = [
  'complete',
  'partial',
  'unavailable',
  'not_applicable',
] as const
export const requestLogFailureCategories = [
  'ok',
  'rate_limited',
  'model_unavailable',
  'invalid_key',
  'upstream_host_error',
  'client_error',
  'downstream_cancel',
  'ambiguous',
] as const
export const requestLogRetryStates = ['retried', 'not_retried'] as const

const requestIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const canonicalNonNegativeInteger = /^(?:0|[1-9]\d*)$/
function defaultRange(): Pick<RequestLogFilters, 'from_ms' | 'to_ms'> {
  const now = Math.floor(Date.now() / 1000) * 1000
  return {
    from_ms: Math.max(0, now - 60 * 60 * 1000),
    to_ms: now,
  }
}

export function defaultRequestLogFilters(): RequestLogFilters {
  return { ...defaultRange(), limit: 20 }
}

function emptyDraft(): LogFilterDraft {
  return {
    from: '',
    to: '',
    group_id: '',
    status: '',
    client_model: '',
    upstream_model: '',
    access_key_id: '',
    request_id: '',
    protocol: '',
    stream: '',
    final_status_code: '',
    usage_state: '',
    cost_state: '',
    pricing_completeness: '',
    cache_present: '',
    upstream_key_id: '',
    attempt_status_code: '',
    failure_category: '',
    error_code: '',
    retry_state: '',
    retry_count_min: '',
    retry_count_max: '',
    first_response_min_ms: '',
    first_response_max_ms: '',
    duration_min_ms: '',
    duration_max_ms: '',
    input_tokens_min: '',
    input_tokens_max: '',
    output_tokens_min: '',
    output_tokens_max: '',
    cost_min_usd: '',
    cost_max_usd: '',
  }
}

function toLocalDateTime(value: number | undefined): string {
  if (value === undefined || !Number.isSafeInteger(value) || value < 0) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function nanoUSDToUSD(value: string | undefined): string {
  if (value === undefined || !canonicalNonNegativeInteger.test(value)) return ''
  const padded = value.padStart(10, '0')
  const whole = padded.slice(0, -9)
  const fraction = padded.slice(-9).replace(/0+$/u, '')
  return fraction ? `${whole}.${fraction}` : whole
}

function usdToNanoUSD(value: string): string | undefined {
  const match = value.match(/^(0|[1-9]\d*)(?:\.(\d{1,9}))?$/u)
  if (!match) return undefined
  const whole = match[1]
  const fraction = (match[2] ?? '').padEnd(9, '0')
  if (whole === undefined) return undefined
  const nanoUSD = BigInt(whole) * 1_000_000_000n + BigInt(fraction || '0')
  return nanoUSD <= maxSignedInt64 ? nanoUSD.toString() : undefined
}

export function createLogFilterDraft(filters: RequestLogFilters = defaultRange()): LogFilterDraft {
  return {
    ...emptyDraft(),
    from: toLocalDateTime(filters.from_ms),
    to: toLocalDateTime(filters.to_ms),
    group_id: filters.group_id === undefined ? '' : String(filters.group_id),
    status: filters.status ?? '',
    client_model: filters.client_model ?? '',
    upstream_model: filters.upstream_model ?? '',
    access_key_id: filters.access_key_id === undefined ? '' : String(filters.access_key_id),
    request_id: filters.request_id ?? '',
    protocol: filters.protocol ?? '',
    stream: filters.stream === undefined ? '' : String(filters.stream),
    final_status_code:
      filters.final_status_code === undefined ? '' : String(filters.final_status_code),
    usage_state: filters.usage_state ?? '',
    cost_state: filters.cost_state ?? '',
    pricing_completeness: filters.pricing_completeness ?? '',
    cache_present: filters.cache_present === undefined ? '' : String(filters.cache_present),
    upstream_key_id: filters.upstream_key_id === undefined ? '' : String(filters.upstream_key_id),
    attempt_status_code:
      filters.attempt_status_code === undefined ? '' : String(filters.attempt_status_code),
    failure_category: filters.failure_category ?? '',
    error_code: filters.error_code ?? '',
    retry_state: filters.retry_state ?? '',
    retry_count_min: filters.retry_count_min === undefined ? '' : String(filters.retry_count_min),
    retry_count_max: filters.retry_count_max === undefined ? '' : String(filters.retry_count_max),
    first_response_min_ms:
      filters.first_response_min_ms === undefined ? '' : String(filters.first_response_min_ms),
    first_response_max_ms:
      filters.first_response_max_ms === undefined ? '' : String(filters.first_response_max_ms),
    duration_min_ms: filters.duration_min_ms === undefined ? '' : String(filters.duration_min_ms),
    duration_max_ms: filters.duration_max_ms === undefined ? '' : String(filters.duration_max_ms),
    input_tokens_min:
      filters.input_tokens_min === undefined ? '' : String(filters.input_tokens_min),
    input_tokens_max:
      filters.input_tokens_max === undefined ? '' : String(filters.input_tokens_max),
    output_tokens_min:
      filters.output_tokens_min === undefined ? '' : String(filters.output_tokens_min),
    output_tokens_max:
      filters.output_tokens_max === undefined ? '' : String(filters.output_tokens_max),
    cost_min_usd: nanoUSDToUSD(filters.cost_min_nano_usd),
    cost_max_usd: nanoUSDToUSD(filters.cost_max_nano_usd),
  }
}

export function applyLogFilterDraft(draft: LogFilterDraft): RequestLogFilters {
  const filters: RequestLogFilters = {}
  if (draft.from) filters.from_ms = new Date(draft.from).getTime()
  if (draft.to) filters.to_ms = new Date(draft.to).getTime()
  if (draft.group_id) filters.group_id = Number(draft.group_id)
  if (draft.status) filters.status = draft.status as RequestLogStatus
  if (draft.client_model) filters.client_model = draft.client_model
  if (draft.upstream_model) filters.upstream_model = draft.upstream_model
  if (draft.access_key_id) filters.access_key_id = Number(draft.access_key_id)
  if (draft.request_id) filters.request_id = draft.request_id
  if (draft.protocol) filters.protocol = draft.protocol as RequestLogFilters['protocol']
  if (draft.stream) filters.stream = draft.stream === 'true'
  if (draft.final_status_code) filters.final_status_code = Number(draft.final_status_code)
  if (draft.usage_state) filters.usage_state = draft.usage_state as RequestLogUsageState
  if (draft.cost_state) filters.cost_state = draft.cost_state as RequestLogCostState
  if (draft.pricing_completeness) {
    filters.pricing_completeness = draft.pricing_completeness as RequestLogPricingCompleteness
  }
  if (draft.cache_present) filters.cache_present = draft.cache_present === 'true'
  if (draft.upstream_key_id) filters.upstream_key_id = Number(draft.upstream_key_id)
  if (draft.attempt_status_code) filters.attempt_status_code = Number(draft.attempt_status_code)
  if (draft.failure_category) {
    filters.failure_category = draft.failure_category as RequestLogFilters['failure_category']
  }
  if (draft.error_code) filters.error_code = draft.error_code
  if (draft.retry_state) filters.retry_state = draft.retry_state as RequestLogRetryState
  for (const field of [
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
  ] as const) {
    if (draft[field]) Object.assign(filters, { [field]: Number(draft[field]) })
  }
  const costMin = usdToNanoUSD(draft.cost_min_usd)
  const costMax = usdToNanoUSD(draft.cost_max_usd)
  if (costMin !== undefined) filters.cost_min_nano_usd = costMin
  if (costMax !== undefined) filters.cost_max_nano_usd = costMax
  return filters
}

function scalar(raw: unknown): string | undefined {
  return typeof raw === 'string' ? raw : undefined
}

function parseSafeInteger(raw: unknown, minimum = 0, maximum = Number.MAX_SAFE_INTEGER) {
  const value = scalar(raw)
  if (value === undefined || !canonicalNonNegativeInteger.test(value)) return undefined
  const number = Number(value)
  return Number.isSafeInteger(number) && number >= minimum && number <= maximum ? number : undefined
}

function parseBoolean(raw: unknown): boolean | undefined {
  return raw === 'true' ? true : raw === 'false' ? false : undefined
}

function parseText(raw: unknown): string | undefined {
  const value = scalar(raw)
  return value && isValidMonitorText(value) ? value : undefined
}

function parseEnum<T extends string>(raw: unknown, values: readonly T[]): T | undefined {
  return typeof raw === 'string' && values.includes(raw as T) ? (raw as T) : undefined
}

function parseNanoUSD(raw: unknown): string | undefined {
  const value = scalar(raw)
  if (value === undefined || !canonicalNonNegativeInteger.test(value)) return undefined
  try {
    return BigInt(value) <= maxSignedInt64 ? value : undefined
  } catch {
    return undefined
  }
}

export function parseAppliedLogFilters(query: Record<string, unknown>): RequestLogFilters {
  const filters: RequestLogFilters = {}
  const from = parseSafeInteger(query.from_ms)
  const to = parseSafeInteger(query.to_ms)
  if (from !== undefined && to !== undefined && from < to) {
    filters.from_ms = from
    filters.to_ms = to
  } else {
    Object.assign(filters, defaultRange())
  }
  const limit = parseSafeInteger(query.limit)
  filters.limit = limit === 20 || limit === 50 || limit === 100 ? (limit as RequestLogPageSize) : 20
  const ids = ['group_id', 'access_key_id', 'upstream_key_id'] as const
  for (const field of ids) {
    const value = parseSafeInteger(query[field], 1)
    if (value !== undefined) Object.assign(filters, { [field]: value })
  }
  const numbers = [
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
  ] as const
  for (const field of numbers) {
    const value = parseSafeInteger(query[field])
    if (value !== undefined) Object.assign(filters, { [field]: value })
  }
  for (const field of ['final_status_code', 'attempt_status_code'] as const) {
    const value = parseSafeInteger(query[field], 0, 999)
    if (value !== undefined) Object.assign(filters, { [field]: value })
  }
  for (const field of ['client_model', 'upstream_model', 'error_code'] as const) {
    const value = parseText(query[field])
    if (value !== undefined) Object.assign(filters, { [field]: value })
  }
  const requestID = scalar(query.request_id)
  if (requestID && requestIDPattern.test(requestID)) filters.request_id = requestID
  const status = parseEnum(query.status, requestLogStatuses)
  if (status) filters.status = status
  const protocol = parseEnum(query.protocol, enabledDataProtocols)
  if (protocol) filters.protocol = protocol
  const usageState = parseEnum(query.usage_state, requestLogUsageStates)
  if (usageState) filters.usage_state = usageState
  const costState = parseEnum(query.cost_state, requestLogCostStates)
  if (costState) filters.cost_state = costState
  const completeness = parseEnum(query.pricing_completeness, requestLogPricingCompleteness)
  if (completeness) filters.pricing_completeness = completeness
  const failure = parseEnum(query.failure_category, requestLogFailureCategories)
  if (failure) filters.failure_category = failure
  const retryState = parseEnum(query.retry_state, requestLogRetryStates)
  if (retryState) filters.retry_state = retryState
  for (const field of ['stream', 'cache_present'] as const) {
    const value = parseBoolean(query[field])
    if (value !== undefined) Object.assign(filters, { [field]: value })
  }
  const minCost = parseNanoUSD(query.cost_min_nano_usd)
  const maxCost = parseNanoUSD(query.cost_max_nano_usd)
  if (minCost !== undefined) filters.cost_min_nano_usd = minCost
  if (maxCost !== undefined) filters.cost_max_nano_usd = maxCost
  return filters
}

export function serializeAppliedLogFilters(filters: RequestLogFilters): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'logs' }
  for (const field of requestLogFilterFields) {
    const value = filters[field]
    if (value !== undefined) query[field] = String(value)
  }
  return query
}

function parseLocalDateTime(value: string): Date | undefined {
  const match = value.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})$/u)
  if (!match) return undefined
  const values = match.slice(1).map(Number)
  const [year, month, day, hour, minute, second] = values
  if ([year, month, day, hour, minute, second].some((part) => part === undefined)) return undefined
  const date = new Date(year!, month! - 1, day!, hour!, minute!, second!)
  return date.getFullYear() === year &&
    date.getMonth() === month! - 1 &&
    date.getDate() === day &&
    date.getHours() === hour &&
    date.getMinutes() === minute &&
    date.getSeconds() === second
    ? date
    : undefined
}

function validateIntegerField(
  errors: LogFilterErrors,
  draft: LogFilterDraft,
  field: keyof LogFilterDraft,
  maximum = Number.MAX_SAFE_INTEGER,
): void {
  const value = draft[field]
  if (!value) return
  const parsed = Number(value)
  if (
    !canonicalNonNegativeInteger.test(value) ||
    !Number.isSafeInteger(parsed) ||
    parsed > maximum
  ) {
    errors[field] = 'monitor.logs.errors.nonNegativeInteger'
  }
}

export function validateLogFilterDraft(draft: LogFilterDraft): LogFilterErrors {
  const errors: LogFilterErrors = {}
  const from = parseLocalDateTime(draft.from)
  const to = parseLocalDateTime(draft.to)
  if (!from) errors.from = 'monitor.logs.errors.dateTime'
  if (!to) errors.to = 'monitor.logs.errors.dateTime'
  if (from && to && from.getTime() >= to.getTime()) errors.to = 'monitor.logs.errors.range'
  for (const field of ['group_id', 'access_key_id', 'upstream_key_id'] as const) {
    validateIntegerField(errors, draft, field)
    if (draft[field] === '0') errors[field] = 'monitor.logs.errors.positiveId'
  }
  for (const field of [
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
  ] as const) {
    validateIntegerField(errors, draft, field)
  }
  for (const field of ['final_status_code', 'attempt_status_code'] as const) {
    validateIntegerField(errors, draft, field, 999)
  }
  for (const field of ['client_model', 'upstream_model', 'error_code'] as const) {
    if (draft[field] && !isValidMonitorText(draft[field])) {
      errors[field] = 'monitor.logs.errors.text'
    }
  }
  if (draft.request_id && !requestIDPattern.test(draft.request_id)) {
    errors.request_id = 'monitor.logs.errors.requestId'
  }
  if (draft.cost_min_usd && usdToNanoUSD(draft.cost_min_usd) === undefined) {
    errors.cost_min_usd = 'monitor.logs.errors.usd'
  }
  if (draft.cost_max_usd && usdToNanoUSD(draft.cost_max_usd) === undefined) {
    errors.cost_max_usd = 'monitor.logs.errors.usd'
  }
  for (const [minimum, maximum] of [
    ['retry_count_min', 'retry_count_max'],
    ['first_response_min_ms', 'first_response_max_ms'],
    ['duration_min_ms', 'duration_max_ms'],
    ['input_tokens_min', 'input_tokens_max'],
    ['output_tokens_min', 'output_tokens_max'],
  ] as const) {
    if (!errors[minimum] && !errors[maximum] && draft[minimum] && draft[maximum]) {
      if (Number(draft[minimum]) > Number(draft[maximum])) {
        errors[maximum] = 'monitor.logs.errors.numericRange'
      }
    }
  }
  const minCost = usdToNanoUSD(draft.cost_min_usd)
  const maxCost = usdToNanoUSD(draft.cost_max_usd)
  if (minCost !== undefined && maxCost !== undefined && BigInt(minCost) > BigInt(maxCost)) {
    errors.cost_max_usd = 'monitor.logs.errors.numericRange'
  }
  return errors
}
