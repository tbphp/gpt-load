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
import {
  defaultTimeRange,
  isDateTimePreset,
  resolveDateTimePreset,
  type DateTimePreset,
} from '@/lib/time'

import { isValidMonitorText, maxSignedInt64 } from './filter-validation'

export interface AppliedLogFilters extends RequestLogFilters {
  from_ms: number
  to_ms: number
  preset?: DateTimePreset
}

export interface LogFilterDraft {
  group_id: string
  channel_id: string
  credential_id: string
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
  'conversion_unsupported',
  'downstream_cancel',
  'authentication_required',
  'ambiguous',
] as const
export const requestLogRetryStates = ['retried', 'not_retried'] as const

const integerRangePairs = [
  ['retry_count_min', 'retry_count_max'],
  ['first_response_min_ms', 'first_response_max_ms'],
  ['duration_min_ms', 'duration_max_ms'],
  ['input_tokens_min', 'input_tokens_max'],
  ['output_tokens_min', 'output_tokens_max'],
] as const
const integerRangeFields = integerRangePairs.flat()

const requestIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const channelIDPattern = /^[a-z][a-z0-9_]{0,99}$/u
const canonicalNonNegativeInteger = /^(?:0|[1-9]\d*)$/

function defaultRequestLogFilters(preset: DateTimePreset = defaultTimeRange): AppliedLogFilters {
  const now = Math.floor(Date.now() / 1000) * 1000
  const interval = resolveDateTimePreset(preset, now)
  return interval.to_ms > interval.from_ms
    ? { ...interval, preset, limit: 20 }
    : { ...resolveDateTimePreset(defaultTimeRange, now), preset: defaultTimeRange, limit: 20 }
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

export function createLogFilterDraft(filters: RequestLogFilters): LogFilterDraft {
  return {
    group_id: filters.group_id === undefined ? '' : String(filters.group_id),
    channel_id: filters.channel_id ?? '',
    credential_id: filters.credential_id === undefined ? '' : String(filters.credential_id),
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

export function applyLogFilterDraft(
  draft: LogFilterDraft,
  current: AppliedLogFilters,
): AppliedLogFilters {
  const filters: AppliedLogFilters = {
    from_ms: current.from_ms,
    to_ms: current.to_ms,
    preset: current.preset,
    limit: current.limit,
  }
  if (draft.group_id) filters.group_id = Number(draft.group_id)
  if (draft.channel_id) filters.channel_id = draft.channel_id
  if (draft.credential_id) filters.credential_id = Number(draft.credential_id)
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
  if (draft.attempt_status_code) filters.attempt_status_code = Number(draft.attempt_status_code)
  if (draft.failure_category) {
    filters.failure_category = draft.failure_category as RequestLogFilters['failure_category']
  }
  if (draft.error_code) filters.error_code = draft.error_code
  if (draft.retry_state) filters.retry_state = draft.retry_state as RequestLogRetryState
  for (const field of integerRangeFields) {
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

export function parseAppliedLogFilters(query: Record<string, unknown>): AppliedLogFilters {
  const from = parseSafeInteger(query.from_ms)
  const to = parseSafeInteger(query.to_ms)
  const preset = query.preset ?? query.usage_preset
  const filters: AppliedLogFilters =
    from !== undefined && to !== undefined && from < to
      ? { from_ms: from, to_ms: to, ...(isDateTimePreset(preset) ? { preset } : {}) }
      : defaultRequestLogFilters(isDateTimePreset(preset) ? preset : defaultTimeRange)
  const limit = parseSafeInteger(query.limit)
  filters.limit = limit === 20 || limit === 50 || limit === 100 ? (limit as RequestLogPageSize) : 20
  const ids = ['group_id', 'access_key_id', 'credential_id'] as const
  for (const field of ids) {
    const value = parseSafeInteger(query[field], 1)
    if (value !== undefined) Object.assign(filters, { [field]: value })
  }
  for (const field of integerRangeFields) {
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
  const channelID = scalar(query.channel_id)
  if (channelID && channelIDPattern.test(channelID)) filters.channel_id = channelID
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

export function serializeAppliedLogFilters(filters: AppliedLogFilters): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'logs' }
  for (const field of requestLogFilterFields) {
    if (filters.preset && (field === 'from_ms' || field === 'to_ms')) continue
    const value = filters[field]
    if (value !== undefined) query[field] = String(value)
  }
  if (filters.preset) query.preset = filters.preset
  return query
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
  for (const field of ['group_id', 'access_key_id', 'credential_id'] as const) {
    validateIntegerField(errors, draft, field)
    if (draft[field] === '0') errors[field] = 'monitor.logs.errors.positiveId'
  }
  if (draft.channel_id && !channelIDPattern.test(draft.channel_id)) {
    errors.channel_id = 'monitor.logs.errors.channelId'
  }
  for (const field of integerRangeFields) {
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
  for (const [minimum, maximum] of integerRangePairs) {
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
