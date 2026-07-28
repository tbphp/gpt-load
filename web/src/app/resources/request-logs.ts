import type { ApiClient } from '@/api/client'
import type { AccessProtocol } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectFiniteNumber,
  projectISOInstant,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

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

const requestIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const canonicalMask = /^(?:\*{4}|.{4}\*{4}.{4})$/u
const protocols = ['openai', 'anthropic', 'gemini'] as const
const statuses = ['success', 'error', 'incomplete', 'canceled'] as const
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
const actions = ['terminate', 'retry', 'cooldown_key', 'fail_key', 'skip_group'] as const
const usageStates = ['complete', 'partial', 'missing', 'not_applicable'] as const
const costStates = ['priced', 'unpriced', 'not_applicable'] as const
const attemptFields = [
  'sequence',
  'group_id',
  'group_name',
  'key_id',
  'key_mask',
  'upstream_model',
  'status_code',
  'duration_ms',
  'failure_category',
  'action',
  'will_retry',
  'error_code',
  'error_summary',
  'committed',
] as const
const itemFields = [
  'request_id',
  'completed_at',
  'access_key',
  'protocol',
  'client_model',
  'upstream_model',
  'status',
  'status_code',
  'duration_ms',
  'error_code',
  'error_summary',
  'affinity_hit',
  'attempts',
  'group_id',
  'usage_state',
  'cost_state',
  'uncached_input_tokens',
  'cache_read_tokens',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'output_tokens',
  'estimated_cost_usd',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0) invalidResponse()
  return result
}

function projectRequestID(value: unknown): string {
  const result = projectString(value)
  if (!requestIDPattern.test(result)) invalidResponse()
  return result
}

function projectMask(value: unknown): string {
  const result = projectString(value)
  if (!canonicalMask.test(result)) invalidResponse()
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

function projectAttempt(value: unknown): RequestLogAttemptDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, attemptFields)
  return {
    sequence: projectSafeInteger(record.sequence, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    key_id: projectSafeInteger(record.key_id, { minimum: 1 }),
    key_mask: projectMask(record.key_mask),
    upstream_model: projectNonBlankString(record.upstream_model),
    status_code: projectStatusCode(record.status_code),
    duration_ms: projectSafeInteger(record.duration_ms, { minimum: 0 }),
    failure_category: projectEnum(record.failure_category, failureCategories),
    action: projectEnum(record.action, actions),
    will_retry: projectBoolean(record.will_retry),
    error_code: projectString(record.error_code, { allowEmpty: true }),
    error_summary: projectString(record.error_summary, { allowEmpty: true }),
    committed: projectBoolean(record.committed),
  }
}

function projectUsageCost(record: Record<string, unknown>) {
  const usageState = projectEnum(record.usage_state, usageStates)
  const costState = projectEnum(record.cost_state, costStates)
  const validCombination =
    ((usageState === 'complete' || usageState === 'partial') &&
      (costState === 'priced' || costState === 'unpriced')) ||
    (usageState === 'missing' && costState === 'unpriced') ||
    (usageState === 'not_applicable' && costState === 'not_applicable')
  if (!validCombination) invalidResponse()

  const tokens = {
    uncached_input_tokens: projectSafeInteger(record.uncached_input_tokens, { minimum: 0 }),
    cache_read_tokens: projectSafeInteger(record.cache_read_tokens, { minimum: 0 }),
    cache_write_5m_tokens: projectSafeInteger(record.cache_write_5m_tokens, { minimum: 0 }),
    cache_write_1h_tokens: projectSafeInteger(record.cache_write_1h_tokens, { minimum: 0 }),
    output_tokens: projectSafeInteger(record.output_tokens, { minimum: 0 }),
  }
  const estimatedCost = projectFiniteNumber(record.estimated_cost_usd, { minimum: 0 })
  if (costState !== 'priced' && estimatedCost !== 0) invalidResponse()
  return {
    usage_state: usageState,
    cost_state: costState,
    ...tokens,
    estimated_cost_usd: estimatedCost,
  }
}

export function projectRequestLogItem(value: unknown): RequestLogItemDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, itemFields)
  return {
    request_id: projectRequestID(record.request_id),
    completed_at: projectISOInstant(record.completed_at),
    access_key: projectAccessKey(record.access_key),
    protocol: projectEnum(record.protocol, protocols),
    client_model: projectNonBlankString(record.client_model),
    upstream_model: projectString(record.upstream_model, { allowEmpty: true }),
    status: projectEnum(record.status, statuses),
    status_code: projectStatusCode(record.status_code),
    duration_ms: projectSafeInteger(record.duration_ms, { minimum: 0 }),
    error_code: projectString(record.error_code, { allowEmpty: true }),
    error_summary: projectString(record.error_summary, { allowEmpty: true }),
    affinity_hit: projectBoolean(record.affinity_hit),
    attempts: projectArray(record.attempts, projectAttempt),
    group_id: record.group_id === null ? null : projectSafeInteger(record.group_id, { minimum: 1 }),
    ...projectUsageCost(record),
  }
}

export function projectRequestLogPage(value: unknown): RequestLogPageDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['items', 'next_cursor'])
  const nextCursor = record.next_cursor === null ? null : projectNonBlankString(record.next_cursor)
  return {
    items: projectArray(record.items, projectRequestLogItem),
    next_cursor: nextCursor,
  }
}

export function normalizeRequestLogFilters(filters: RequestLogFilters): RequestLogFilters {
  const result: RequestLogFilters = {}
  for (const field of [
    'from',
    'to',
    'group_id',
    'model',
    'access_key_id',
    'status',
    'request_id',
  ] as const) {
    const value = filters[field]
    if (value !== undefined) {
      Object.assign(result, { [field]: value })
    }
  }
  return result
}

export function requestLogQueryIdentity(filters: RequestLogFilters) {
  return controlQueryKeys.logs.list(normalizeRequestLogFilters(filters))
}

export async function listRequestLogs(
  client: ApiClient,
  filters: RequestLogFilters,
  cursor?: string,
  signal?: AbortSignal,
): Promise<RequestLogPageDto> {
  const normalized = normalizeRequestLogFilters(filters)
  const params = new URLSearchParams()
  const values: Array<[string, string | number | undefined]> = [
    ['from', normalized.from],
    ['to', normalized.to],
    ['group_id', normalized.group_id],
    ['model', normalized.model],
    ['access_key_id', normalized.access_key_id],
    ['status', normalized.status],
    ['request_id', normalized.request_id],
    ['cursor', cursor],
  ]
  for (const [key, value] of values) {
    if (value !== undefined) params.append(key, String(value))
  }
  const query = params.toString()
  const path: `/api/${string}` = query === '' ? '/api/logs' : `/api/logs?${query}`
  return projectRequestLogPage(await client.request(path, { method: 'GET', signal }))
}
