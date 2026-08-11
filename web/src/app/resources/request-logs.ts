import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import {
  enabledDataProtocols,
  knownUpstreamAPIs,
  type UpstreamAPIValue,
} from '@/api/control/protocols'
import type { AccessProtocol, FailureCategory } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { projectChannelID } from '@/app/resources/channels'

import { normalizeRequestLogFilters, requestLogFilterFields } from './request-log-filters'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEpochMilliseconds,
  projectEnum,
  projectInt64String,
  projectNonNegativeInt64String,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type RequestLogStatus = 'success' | 'error' | 'incomplete' | 'canceled'
export type RequestLogModelConsistency = 'not_applicable' | 'match' | 'unknown' | 'mismatch'
export type RequestLogAction =
  'terminate' | 'retry' | 'cooldown_credential' | 'fail_credential' | 'skip_group'
export type RequestLogUsageState = 'complete' | 'partial' | 'missing' | 'not_applicable'
export type RequestLogCostState = 'priced' | 'unpriced' | 'not_applicable'
export type RequestLogPricingCompleteness =
  'complete' | 'partial' | 'unavailable' | 'not_applicable'
export type RequestLogRetryState = 'retried' | 'not_retried'
export type RequestLogReceiptLineState = 'priced' | 'unpriced'
export type RequestLogPageSize = 20 | 50 | 100
export type RequestLogOperation =
  | 'chat_completion'
  | 'responses_create'
  | 'responses_retrieve'
  | 'responses_delete'
  | 'responses_cancel'
  | 'responses_input_items'
  | 'responses_compact'
  | 'responses_input_tokens'
  | 'responses_passthrough'
  | 'list_models'
  | 'probe'
export type RequestLogRouteMode = 'native' | 'converted'
export type RequestLogDispatchState = 'not_sent' | 'maybe_sent'
export type RequestLogUpstreamAPI = UpstreamAPIValue

export type { FailureCategory } from '@/api/control/types'

export interface RequestLogFilters {
  from_ms?: number
  to_ms?: number
  limit?: RequestLogPageSize
  group_id?: number
  channel_id?: string
  credential_id?: number
  client_model?: string
  upstream_model?: string
  access_key_id?: number
  status?: RequestLogStatus
  request_id?: string
  protocol?: AccessProtocol
  stream?: boolean
  final_status_code?: number
  usage_state?: RequestLogUsageState
  cost_state?: RequestLogCostState
  pricing_completeness?: RequestLogPricingCompleteness
  cache_present?: boolean
  attempt_status_code?: number
  failure_category?: FailureCategory
  error_code?: string
  retry_state?: RequestLogRetryState
  retry_count_min?: number
  retry_count_max?: number
  first_response_min_ms?: number
  first_response_max_ms?: number
  duration_min_ms?: number
  duration_max_ms?: number
  input_tokens_min?: number
  input_tokens_max?: number
  output_tokens_min?: number
  output_tokens_max?: number
  cost_min_nano_usd?: string
  cost_max_nano_usd?: string
}

export interface RequestLogPricingLineDto {
  code: 'input' | 'cache_read' | 'cache_write_5m' | 'cache_write_1h' | 'cache_write' | 'output'
  quantity: string
  rate_nano_usd_per_million: string | null
  multiplier: { numerator: string; denominator: string }
  state: RequestLogReceiptLineState
  amount_nano_usd: string | null
}

export interface RequestLogPricingReceiptDto {
  schema_version: 1 | 2 | 3
  method: 'unit_rate_sum'
  method_version: 1
  currency: 'USD'
  rule: { scope_key?: string; channel_id?: string; model_id: string }
  context_threshold_tokens: string | null
  line_items: RequestLogPricingLineDto[]
  total_nano_usd: string
}

export interface RequestLogAttemptDto {
  sequence: number
  group_id: number
  group_name: string
  channel_id: string | null
  credential_id: number | null
  operation: RequestLogOperation | null
  route_mode: RequestLogRouteMode | null
  upstream_model: string | null
  upstream_request_id: string | null
  dispatch_state: RequestLogDispatchState | null
  response_started: boolean
  upstream_api: RequestLogUpstreamAPI | null
  reasoning: RequestLogReasoningDto | null
  status_code: number
  duration_ms: number
  failure_category: FailureCategory
  action: RequestLogAction
  will_retry: boolean
  error_code: string
  error_summary: string
  committed: boolean
  pricing_receipt: RequestLogPricingReceiptDto | null
}

export interface RequestLogReasoningDto {
  mode: string | null
  effort: string | null
  budget_tokens: string | null
}

export interface RequestLogItemDto {
  request_id: string
  completed_at_ms: number
  access_key: { id: number; name: string | null; deleted: boolean }
  protocol: AccessProtocol
  operation: RequestLogOperation | null
  upstream_api: RequestLogUpstreamAPI | null
  client_model: string | null
  upstream_model: string | null
  upstream_reported_model: string | null
  model_consistency: RequestLogModelConsistency
  reasoning: RequestLogReasoningDto | null
  status: RequestLogStatus
  status_code: number
  stream: boolean
  first_response_ms: number | null
  duration_ms: number
  attempt_count: number
  error_code: string
  error_summary: string
  affinity_hit: boolean
  group_id: number | null
  channel_id: string | null
  credential_id: number | null
  route_mode: RequestLogRouteMode | null
  usage_state: RequestLogUsageState
  cost_state: RequestLogCostState
  pricing_completeness: RequestLogPricingCompleteness
  input_tokens: string
  cache_read_tokens: string
  cache_write_5m_tokens: string
  cache_write_1h_tokens: string
  cache_write_unknown_tokens: string
  output_tokens: string
  estimated_cost_nano_usd: string
}

export interface RequestLogDetailDto extends RequestLogItemDto {
  attempts: RequestLogAttemptDto[]
}

export interface RequestLogPageDto {
  items: RequestLogItemDto[]
  next_cursor: string | null
}

const requestIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const statuses = ['success', 'error', 'incomplete', 'canceled'] as const
const modelConsistencyValues = ['not_applicable', 'match', 'unknown', 'mismatch'] as const
const failureCategories = [
  'ok',
  'rate_limited',
  'model_unavailable',
  'invalid_key',
  'upstream_host_error',
  'client_error',
  'downstream_cancel',
  'ambiguous',
] as const
const actions = [
  'terminate',
  'retry',
  'cooldown_credential',
  'fail_credential',
  'skip_group',
] as const
const operations = [
  'chat_completion',
  'responses_create',
  'responses_retrieve',
  'responses_delete',
  'responses_cancel',
  'responses_input_items',
  'responses_compact',
  'responses_input_tokens',
  'responses_passthrough',
  'list_models',
  'probe',
] as const
const routeModes = ['native', 'converted'] as const
const dispatchStates = ['not_sent', 'maybe_sent'] as const
const usageStates = ['complete', 'partial', 'missing', 'not_applicable'] as const
const costStates = ['priced', 'unpriced', 'not_applicable'] as const
const pricingCompletenessValues = ['complete', 'partial', 'unavailable', 'not_applicable'] as const
const receiptCodes = [
  'input',
  'cache_read',
  'cache_write_5m',
  'cache_write_1h',
  'cache_write',
  'output',
] as const
const receiptLineStates = ['priced', 'unpriced'] as const
const itemFields = [
  'request_id',
  'completed_at_ms',
  'access_key',
  'protocol',
  'operation',
  'upstream_api',
  'client_model',
  'upstream_model',
  'upstream_reported_model',
  'model_consistency',
  'reasoning',
  'status',
  'status_code',
  'stream',
  'first_response_ms',
  'duration_ms',
  'attempt_count',
  'error_code',
  'error_summary',
  'affinity_hit',
  'group_id',
  'channel_id',
  'credential_id',
  'route_mode',
  'usage_state',
  'cost_state',
  'pricing_completeness',
  'input_tokens',
  'cache_read_tokens',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'cache_write_unknown_tokens',
  'output_tokens',
  'estimated_cost_nano_usd',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0) invalidResponse()
  return result
}

function projectNullableModel(value: unknown): string | null {
  return value === null ? null : projectNonBlankString(value)
}

function projectRequestID(value: unknown): string {
  const result = projectString(value)
  if (!requestIDPattern.test(result)) invalidResponse()
  return result
}

function projectStatusCode(value: unknown): number {
  return projectSafeInteger(value, { minimum: 0, maximum: 999 })
}

function projectAccessKey(value: unknown): RequestLogItemDto['access_key'] {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['id', 'name', 'deleted'])
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: record.name === null ? null : projectNonBlankString(record.name),
    deleted: projectBoolean(record.deleted),
  }
}

function projectPricingReceipt(value: unknown): RequestLogPricingReceiptDto | null {
  if (value === null) return null
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'schema_version',
    'method',
    'method_version',
    'currency',
    'rule',
    'context_threshold_tokens',
    'line_items',
    'total_nano_usd',
  ])
  const rule = projectRecord(record.rule)
  assertNoSecretLikeFields(rule, ['scope_key', 'channel_id', 'model_id'])
  const lines = projectArray(record.line_items, (lineValue): RequestLogPricingLineDto => {
    const line = projectRecord(lineValue)
    assertNoSecretLikeFields(line, [
      'code',
      'quantity',
      'rate_nano_usd_per_million',
      'multiplier',
      'state',
      'amount_nano_usd',
    ])
    const multiplier = projectRecord(line.multiplier)
    assertNoSecretLikeFields(multiplier, ['numerator', 'denominator'])
    const numerator = projectNonNegativeInt64String(multiplier.numerator)
    const denominator = projectNonNegativeInt64String(multiplier.denominator)
    if (numerator === '0' || denominator === '0') invalidResponse()
    return {
      code: projectEnum(line.code, receiptCodes),
      quantity: projectNonNegativeInt64String(line.quantity),
      rate_nano_usd_per_million:
        line.rate_nano_usd_per_million === null
          ? null
          : projectNonNegativeInt64String(line.rate_nano_usd_per_million),
      multiplier: {
        numerator,
        denominator,
      },
      state: projectEnum(line.state, receiptLineStates),
      amount_nano_usd:
        line.amount_nano_usd === null ? null : projectNonNegativeInt64String(line.amount_nano_usd),
    }
  })
  const schemaVersion = projectSafeInteger(record.schema_version, { minimum: 1, maximum: 3 }) as
    1 | 2 | 3
  const scopeKey = rule.scope_key === undefined ? undefined : projectNonBlankString(rule.scope_key)
  const channelID = rule.channel_id === undefined ? undefined : projectChannelID(rule.channel_id)
  if (
    (schemaVersion === 1 && (scopeKey === undefined || channelID !== undefined)) ||
    (schemaVersion === 2 && (scopeKey !== undefined || channelID !== undefined)) ||
    (schemaVersion === 3 && (scopeKey !== undefined || channelID === undefined))
  ) {
    invalidResponse()
  }
  return {
    schema_version: schemaVersion,
    method: projectEnum(record.method, ['unit_rate_sum'] as const),
    method_version: projectSafeInteger(record.method_version, { minimum: 1, maximum: 1 }) as 1,
    currency: projectEnum(record.currency, ['USD'] as const),
    rule: {
      ...(scopeKey === undefined ? {} : { scope_key: scopeKey }),
      ...(channelID === undefined ? {} : { channel_id: channelID }),
      model_id: projectNonBlankString(rule.model_id),
    },
    context_threshold_tokens:
      record.context_threshold_tokens === null
        ? null
        : projectNonNegativeInt64String(record.context_threshold_tokens),
    line_items: lines,
    total_nano_usd: projectNonNegativeInt64String(record.total_nano_usd),
  }
}

function projectAttempt(value: unknown): RequestLogAttemptDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'sequence',
    'group_id',
    'group_name',
    'channel_id',
    'credential_id',
    'operation',
    'route_mode',
    'upstream_model',
    'upstream_request_id',
    'dispatch_state',
    'response_started',
    'upstream_api',
    'reasoning',
    'status_code',
    'duration_ms',
    'failure_category',
    'action',
    'will_retry',
    'error_code',
    'error_summary',
    'committed',
    'pricing_receipt',
  ])
  return {
    sequence: projectSafeInteger(record.sequence, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    channel_id: record.channel_id === null ? null : projectChannelID(record.channel_id),
    credential_id:
      record.credential_id === null
        ? null
        : projectSafeInteger(record.credential_id, { minimum: 1 }),
    operation: record.operation === null ? null : projectEnum(record.operation, operations),
    route_mode: record.route_mode === null ? null : projectEnum(record.route_mode, routeModes),
    upstream_model: projectNullableModel(record.upstream_model),
    upstream_request_id: projectNullableModel(record.upstream_request_id),
    dispatch_state:
      record.dispatch_state === null ? null : projectEnum(record.dispatch_state, dispatchStates),
    response_started: projectBoolean(record.response_started),
    upstream_api:
      record.upstream_api === null ? null : projectEnum(record.upstream_api, knownUpstreamAPIs),
    reasoning: projectReasoning(record.reasoning),
    status_code: projectStatusCode(record.status_code),
    duration_ms: projectSafeInteger(record.duration_ms, { minimum: 0 }),
    failure_category: projectEnum(record.failure_category, failureCategories),
    action: projectEnum(record.action, actions),
    will_retry: projectBoolean(record.will_retry),
    error_code: projectString(record.error_code, { allowEmpty: true }),
    error_summary: projectString(record.error_summary, { allowEmpty: true }),
    committed: projectBoolean(record.committed),
    pricing_receipt: projectPricingReceipt(record.pricing_receipt),
  }
}

function projectReasoning(value: unknown): RequestLogReasoningDto | null {
  if (value === null) return null
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['mode', 'effort', 'budget_tokens'])
  const mode = record.mode === null ? null : projectNonBlankString(record.mode)
  const effort = record.effort === null ? null : projectNonBlankString(record.effort)
  const budgetTokens =
    record.budget_tokens === null ? null : projectInt64String(record.budget_tokens)
  if (mode === null && effort === null && budgetTokens === null) invalidResponse()
  return { mode, effort, budget_tokens: budgetTokens }
}

function projectUsageCost(record: Record<string, unknown>) {
  const usageState = projectEnum(record.usage_state, usageStates)
  const costState = projectEnum(record.cost_state, costStates)
  const pricingCompleteness = projectEnum(record.pricing_completeness, pricingCompletenessValues)
  const validCombination =
    ((usageState === 'complete' || usageState === 'partial') &&
      ((costState === 'priced' &&
        (pricingCompleteness === 'complete' || pricingCompleteness === 'partial')) ||
        (costState === 'unpriced' && pricingCompleteness === 'unavailable'))) ||
    (usageState === 'missing' &&
      costState === 'unpriced' &&
      pricingCompleteness === 'unavailable') ||
    (usageState === 'not_applicable' &&
      costState === 'not_applicable' &&
      pricingCompleteness === 'not_applicable')
  if (!validCombination) invalidResponse()

  const estimatedCostNanoUSD = projectNonNegativeInt64String(record.estimated_cost_nano_usd)
  if (costState !== 'priced' && estimatedCostNanoUSD !== '0') invalidResponse()
  return {
    usage_state: usageState,
    cost_state: costState,
    pricing_completeness: pricingCompleteness,
    input_tokens: projectNonNegativeInt64String(record.input_tokens),
    cache_read_tokens: projectNonNegativeInt64String(record.cache_read_tokens),
    cache_write_5m_tokens: projectNonNegativeInt64String(record.cache_write_5m_tokens),
    cache_write_1h_tokens: projectNonNegativeInt64String(record.cache_write_1h_tokens),
    cache_write_unknown_tokens: projectNonNegativeInt64String(record.cache_write_unknown_tokens),
    output_tokens: projectNonNegativeInt64String(record.output_tokens),
    estimated_cost_nano_usd: estimatedCostNanoUSD,
  }
}

function projectItemRecord(record: Record<string, unknown>): RequestLogItemDto {
  const status = projectEnum(record.status, statuses)
  const upstreamModel = projectNullableModel(record.upstream_model)
  const upstreamReportedModel = projectNullableModel(record.upstream_reported_model)
  const modelConsistency = projectEnum(record.model_consistency, modelConsistencyValues)
  const validModelObservation =
    (modelConsistency === 'not_applicable' &&
      upstreamReportedModel === null &&
      (status !== 'success' || upstreamModel === null)) ||
    (modelConsistency === 'unknown' &&
      status === 'success' &&
      upstreamModel !== null &&
      upstreamReportedModel === null) ||
    (modelConsistency === 'match' &&
      status === 'success' &&
      upstreamModel !== null &&
      upstreamReportedModel !== null) ||
    (modelConsistency === 'mismatch' &&
      status === 'success' &&
      upstreamModel !== null &&
      upstreamReportedModel !== null)
  if (!validModelObservation) invalidResponse()

  return {
    request_id: projectRequestID(record.request_id),
    completed_at_ms: projectEpochMilliseconds(record.completed_at_ms),
    access_key: projectAccessKey(record.access_key),
    protocol: projectEnum(record.protocol, enabledDataProtocols),
    operation: record.operation === null ? null : projectEnum(record.operation, operations),
    upstream_api:
      record.upstream_api === null ? null : projectEnum(record.upstream_api, knownUpstreamAPIs),
    client_model: projectNullableModel(record.client_model),
    upstream_model: upstreamModel,
    upstream_reported_model: upstreamReportedModel,
    model_consistency: modelConsistency,
    reasoning: projectReasoning(record.reasoning),
    status,
    status_code: projectStatusCode(record.status_code),
    stream: projectBoolean(record.stream),
    first_response_ms:
      record.first_response_ms === null
        ? null
        : projectSafeInteger(record.first_response_ms, { minimum: 0 }),
    duration_ms: projectSafeInteger(record.duration_ms, { minimum: 0 }),
    attempt_count: projectSafeInteger(record.attempt_count, { minimum: 0 }),
    error_code: projectString(record.error_code, { allowEmpty: true }),
    error_summary: projectString(record.error_summary, { allowEmpty: true }),
    affinity_hit: projectBoolean(record.affinity_hit),
    group_id: record.group_id === null ? null : projectSafeInteger(record.group_id, { minimum: 1 }),
    channel_id: record.channel_id === null ? null : projectChannelID(record.channel_id),
    credential_id:
      record.credential_id === null
        ? null
        : projectSafeInteger(record.credential_id, { minimum: 1 }),
    route_mode: record.route_mode === null ? null : projectEnum(record.route_mode, routeModes),
    ...projectUsageCost(record),
  }
}

export function projectRequestLogItem(value: unknown): RequestLogItemDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, itemFields)
  return projectItemRecord(record)
}

export function projectRequestLogDetail(value: unknown): RequestLogDetailDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [...itemFields, 'attempts'])
  return { ...projectItemRecord(record), attempts: projectArray(record.attempts, projectAttempt) }
}

export function projectRequestLogPage(value: unknown): RequestLogPageDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['items', 'next_cursor'])
  return {
    items: projectArray(record.items, projectRequestLogItem),
    next_cursor: record.next_cursor === null ? null : projectNonBlankString(record.next_cursor),
  }
}

export { normalizeRequestLogFilters } from './request-log-filters'

export function requestLogQueryIdentity(filters: RequestLogFilters) {
  return controlQueryKeys.logs.list(normalizeRequestLogFilters(filters))
}

export async function listRequestLogs(
  client: ApiClient,
  filters: RequestLogFilters,
  cursor?: string,
  signal?: AbortSignal,
): Promise<RequestLogPageDto> {
  const params = new URLSearchParams()
  for (const field of requestLogFilterFields) {
    const value = filters[field]
    if (value !== undefined) params.append(field, String(value))
  }
  if (cursor !== undefined) params.append('cursor', cursor)
  const query = params.toString()
  const path: `/api/${string}` = query === '' ? '/api/logs' : `/api/logs?${query}`
  return projectRequestLogPage(await client.request(path, { method: 'GET', signal }))
}

export async function getRequestLog(
  client: ApiClient,
  requestID: string,
  signal?: AbortSignal,
): Promise<RequestLogDetailDto> {
  if (!requestIDPattern.test(requestID)) throw new InvalidResponseError()
  return projectRequestLogDetail(
    await client.request(`/api/logs/${encodeURIComponent(requestID)}`, { method: 'GET', signal }),
  )
}

export function requestLogQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<RequestLogFilters>,
  cursor: MaybeRefOrGetter<string | undefined>,
) {
  return queryOptions({
    queryKey: computed(() => [
      ...requestLogQueryIdentity(toValue(filters)),
      'cursor',
      toValue(cursor) ?? null,
    ]),
    queryFn: ({ signal }) => listRequestLogs(client, toValue(filters), toValue(cursor), signal),
    placeholderData: keepPreviousData,
  })
}

export function requestLogDetailQueryOptions(
  client: ApiClient,
  requestID: MaybeRefOrGetter<string | undefined>,
) {
  return queryOptions({
    queryKey: computed(() => {
      const id = toValue(requestID)
      return id === undefined ? controlQueryKeys.logs.details() : controlQueryKeys.logs.detail(id)
    }),
    queryFn: ({ signal }) => {
      const id = toValue(requestID)
      if (id === undefined) throw new InvalidResponseError()
      return getRequestLog(client, id, signal)
    },
    enabled: computed(() => toValue(requestID) !== undefined),
    gcTime: 0,
  })
}
