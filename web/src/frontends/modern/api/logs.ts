import type { ApiClient } from '@shared/http/client'
import { ApiError, InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'

export const logStatuses = ['success', 'error', 'incomplete', 'canceled'] as const
export const logOperations = [
  'chat_completion',
  'responses_create',
  'responses_retrieve',
  'responses_delete',
  'responses_cancel',
  'responses_input_items',
  'responses_compact',
  'responses_input_tokens',
  'count_tokens',
  'responses_passthrough',
  'web_search',
  'images_generate',
  'images_edit',
  'embeddings_create',
  'rerank',
  'decisions_create',
  'list_models',
  'probe',
] as const
export const logFilterNames = [
  'from_ms',
  'to_ms',
  'limit',
  'group_id',
  'channel_id',
  'credential_id',
  'client_model',
  'upstream_model',
  'model_consistency',
  'access_key_id',
  'status',
  'request_id',
  'protocol',
  'operation',
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
export type LogFilterName = (typeof logFilterNames)[number]
export type LogQuery = Partial<Record<LogFilterName, string>>
export const internalLogFilters: readonly LogFilterName[] = [
  'group_id',
  'channel_id',
  'credential_id',
  'upstream_model',
  'model_consistency',
  'access_key_id',
  'attempt_status_code',
  'failure_category',
  'error_code',
  'retry_state',
  'retry_count_min',
  'retry_count_max',
]
export const logsKey = ['modern', 'logs'] as const
export const logDetailKey = (id: string) => ['modern', 'log-detail', id] as const
export const logRequestPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

export interface LogReasoning {
  mode: string | null
  effort: string | null
  budget_tokens: string | null
}
export interface LogEntry {
  auto_decision?: LogAutoDecision
  request_id: string
  completed_at_ms: number
  access_key: { id: number; name: string | null; deleted: boolean }
  protocol: string
  operation: string | null
  upstream_protocol: string | null
  client_model: string | null
  upstream_model: string | null
  upstream_reported_model: string | null
  model_consistency: string
  reasoning: LogReasoning | null
  status: (typeof logStatuses)[number]
  status_code: number
  stream: boolean
  first_response_ms: number | null
  duration_ms: number
  attempt_count: number
  error_code: string
  error_summary: string
  affinity_hit: boolean
  affinity_kind: string
  group_id: number | null
  channel_id: string | null
  credential_id: number | null
  credential_name: string
  // 前端内部展示标记，不读取或要求新的响应字段。
  credential_deleted: boolean
  route_mode: string | null
  usage_state: 'complete' | 'partial' | 'missing' | 'not_applicable'
  cost_state: 'priced' | 'unpriced' | 'not_applicable'
  pricing_completeness: 'complete' | 'partial' | 'unavailable' | 'not_applicable'
  pricing_mode: string | null
  context_threshold_tokens: string | null
  input_tokens: string
  cache_read_tokens: string
  cache_write_5m_tokens: string
  cache_write_1h_tokens: string
  cache_write_unknown_tokens: string
  output_tokens: string
  estimated_cost_nano_usd: string
}

export interface LogAutoDecision {
  selection: { preset_name: string; target_model: string }
  source: string
  status: string
  execution_phase: string
  reason: string
  provider: string
  group_name: string
  channel_name: string
  credential_name: string
  credential_deleted: boolean
  requested_model: string
  upstream_model: string
  reported_model: string
  duration_ms: number
  called: boolean
  confidence: number | null
  input_tokens: string | null
  output_tokens: string | null
  estimated_cost_nano_usd: string
  cost_state: string
  pricing_completeness: string
  receipt: LogReceipt | null
}
export interface LogPricingLine {
  code: string
  quantity: string
  rate_nano_usd_per_million: string | null
  multiplier: { numerator: string; denominator: string }
  state: 'priced' | 'unpriced'
  amount_nano_usd: string | null
}
export interface LogReceipt {
  schema_version: number
  currency: string
  method: string
  method_version: number
  pricing_mode: string
  price_multipliers: { group: string; access_key: string } | null
  rule: { scope_key: string | null; channel_id: string | null; model_id: string }
  context_threshold_tokens: string | null
  line_items: LogPricingLine[]
  base_total_nano_usd: string | null
  total_nano_usd: string
}
export interface LogAttempt {
  sequence: number
  group_id: number
  group_name: string
  channel_id: string | null
  credential_id: number | null
  credential_name: string
  // 前端内部展示标记，不读取或要求新的响应字段。
  credential_deleted: boolean
  operation: string | null
  route_mode: string | null
  upstream_model: string | null
  upstream_request_id: string | null
  dispatch_state: string | null
  response_started: boolean
  upstream_protocol: string | null
  reasoning: LogReasoning | null
  status_code: number
  duration_ms: number
  failure_category: string
  failure_origin: string | null
  failure_scope: string | null
  retry_directive: string | null
  effect: string | null
  cooldown_until_ms: number | null
  rule_id: string | null
  action: string
  will_retry: boolean
  error_code: string
  error_summary: string
  committed: boolean
  pricing_receipt: LogReceipt | null
}
export interface LogDetail extends LogEntry {
  attempts: LogAttempt[]
}
export interface LogPage {
  items: LogEntry[]
  pagination: { page: number; page_size: number; total_items: number; total_pages: number }
}
export interface LogAccessKeyOption {
  id: number
  name: string
  suffix: string
}

const optionalText = (value: unknown) => (value == null ? null : text(value))
const optionalNumber = (value: unknown) => (value == null ? null : integer(value))
function count(value: unknown): string {
  const result = text(value)
  if (!/^(?:0|[1-9]\d*)$/.test(result)) throw new InvalidResponseError()
  return result
}
const optionalCount = (value: unknown) => (value == null ? null : count(value))
function reasoning(value: unknown): LogReasoning | null {
  if (value == null) return null
  const row = record(value)
  const budget = optionalText(row.budget_tokens)
  if (budget !== null && !/^-?\d+$/.test(budget)) throw new InvalidResponseError()
  return { mode: optionalText(row.mode), effort: optionalText(row.effort), budget_tokens: budget }
}
function receipt(value: unknown): LogReceipt | null {
  if (value == null) return null
  const row = record(value)
  const rule = record(row.rule)
  const multipliers = row.price_multipliers == null ? null : record(row.price_multipliers)
  return {
    schema_version: integer(row.schema_version, 1),
    currency: text(row.currency),
    method: text(row.method),
    method_version: integer(row.method_version, 1),
    pricing_mode: text(row.pricing_mode),
    price_multipliers: multipliers
      ? { group: text(multipliers.group), access_key: text(multipliers.access_key) }
      : null,
    rule: {
      scope_key: optionalText(rule.scope_key),
      channel_id: optionalText(rule.channel_id),
      model_id: text(rule.model_id),
    },
    context_threshold_tokens: optionalCount(row.context_threshold_tokens),
    base_total_nano_usd: optionalCount(row.base_total_nano_usd),
    total_nano_usd: count(row.total_nano_usd),
    line_items: list(row.line_items).map((value) => {
      const line = record(value)
      const multiplier = record(line.multiplier)
      const denominator = count(multiplier.denominator)
      if (denominator === '0') throw new InvalidResponseError()
      return {
        code: text(line.code),
        quantity: count(line.quantity),
        rate_nano_usd_per_million: optionalCount(line.rate_nano_usd_per_million),
        multiplier: { numerator: count(multiplier.numerator), denominator },
        state: oneOf(line.state, ['priced', 'unpriced'] as const),
        amount_nano_usd: optionalCount(line.amount_nano_usd),
      }
    }),
  }
}
function entry(value: unknown): LogEntry {
  const row = record(value)
  const key = record(row.access_key)
  const id = text(row.request_id)
  if (!logRequestPattern.test(id)) throw new InvalidResponseError()
  return {
    request_id: id,
    completed_at_ms: integer(row.completed_at_ms),
    access_key: {
      id: integer(key.id),
      name: optionalText(key.name),
      deleted: boolean(key.deleted),
    },
    protocol: text(row.protocol),
    operation: optionalText(row.operation),
    upstream_protocol: optionalText(row.upstream_protocol),
    client_model: optionalText(row.client_model),
    upstream_model: optionalText(row.upstream_model),
    upstream_reported_model: optionalText(row.upstream_reported_model),
    model_consistency: text(row.model_consistency),
    reasoning: reasoning(row.reasoning),
    status: oneOf(row.status, logStatuses),
    status_code: integer(row.status_code),
    stream: boolean(row.stream),
    first_response_ms: optionalNumber(row.first_response_ms),
    duration_ms: integer(row.duration_ms),
    attempt_count: integer(row.attempt_count),
    error_code: text(row.error_code),
    error_summary: text(row.error_summary),
    affinity_hit: boolean(row.affinity_hit),
    affinity_kind: text(row.affinity_kind),
    group_id: optionalNumber(row.group_id),
    channel_id: optionalText(row.channel_id),
    credential_id: optionalNumber(row.credential_id),
    credential_name: text(row.credential_name),
    credential_deleted: row.credential_id != null && row.credential_name === '',
    route_mode: optionalText(row.route_mode),
    usage_state: oneOf(row.usage_state, [
      'complete',
      'partial',
      'missing',
      'not_applicable',
    ] as const),
    auto_decision: row.auto_decision === undefined ? undefined : autoDecision(row.auto_decision),
    cost_state: oneOf(row.total_cost_state ?? row.cost_state, [
      'priced',
      'unpriced',
      'not_applicable',
    ] as const),
    pricing_completeness: oneOf(row.total_pricing_completeness ?? row.pricing_completeness, [
      'complete',
      'partial',
      'unavailable',
      'not_applicable',
    ] as const),
    pricing_mode: optionalText(row.pricing_mode),
    context_threshold_tokens: optionalCount(row.context_threshold_tokens),
    input_tokens: count(row.input_tokens),
    cache_read_tokens: count(row.cache_read_tokens),
    cache_write_5m_tokens: count(row.cache_write_5m_tokens),
    cache_write_1h_tokens: count(row.cache_write_1h_tokens),
    cache_write_unknown_tokens: count(row.cache_write_unknown_tokens),
    output_tokens: count(row.output_tokens),
    estimated_cost_nano_usd: count(
      row.total_estimated_cost_nano_usd ?? row.estimated_cost_nano_usd,
    ),
  }
}

function autoDecision(value: unknown): LogAutoDecision {
  const row = record(value),
    selection = record(row.selection)
  const confidence = row.confidence ?? null
  if (
    confidence !== null &&
    (typeof confidence !== 'number' ||
      !Number.isFinite(confidence) ||
      confidence < 0 ||
      confidence > 1)
  )
    throw new InvalidResponseError()
  return {
    selection: {
      preset_name: text(selection.preset_name),
      target_model: text(selection.target_model),
    },
    source: text(row.source),
    status: text(row.status),
    execution_phase: text(row.execution_phase ?? ''),
    reason: text(row.reason ?? ''),
    provider: text(row.provider ?? ''),
    group_name: text(row.group_name ?? ''),
    channel_name: text(row.channel_name ?? ''),
    credential_name: text(row.credential_name ?? ''),
    credential_deleted: boolean(row.credential_deleted ?? false),
    requested_model: text(row.requested_model ?? ''),
    upstream_model: text(row.upstream_model ?? ''),
    reported_model: text(row.reported_model ?? ''),
    duration_ms: integer(row.duration_ms),
    called: boolean(row.called),
    confidence,
    input_tokens: row.input_tokens === undefined ? null : count(row.input_tokens),
    output_tokens: row.output_tokens === undefined ? null : count(row.output_tokens),
    estimated_cost_nano_usd: count(row.estimated_cost_nano_usd),
    cost_state: text(row.cost_state),
    pricing_completeness: text(row.pricing_completeness),
    receipt: row.receipt === undefined ? null : receipt(row.receipt),
  }
}
export async function getLogs(
  client: ApiClient,
  filters: LogQuery,
  page: number,
  signal: AbortSignal,
): Promise<LogPage> {
  const params = new URLSearchParams()
  for (const key of logFilterNames)
    if (key !== 'limit' && filters[key]) params.set(key, filters[key]!)
  params.set('page', String(page))
  params.set('page_size', filters.limit ?? '20')
  const data = record(await client.request(`/api/logs?${params}`, { signal }))
  const pagination = record(data.pagination)
  const items = list(data.items).map(entry)
  if (new Set(items.map((row) => row.request_id)).size !== items.length)
    throw new InvalidResponseError()
  return {
    items,
    pagination: {
      page: integer(pagination.page, 1),
      page_size: integer(pagination.page_size, 1),
      total_items: integer(pagination.total_items),
      total_pages: integer(pagination.total_pages),
    },
  }
}
export async function getLogDetail(
  client: ApiClient,
  id: string,
  signal: AbortSignal,
): Promise<LogDetail | null> {
  if (!logRequestPattern.test(id)) throw new InvalidResponseError()
  let data: Record<string, unknown>
  try {
    data = record(await client.request(`/api/logs/${id}`, { signal }))
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 404) return null
    throw cause
  }
  const row = entry(data)
  if (row.request_id !== id) throw new InvalidResponseError()
  return {
    ...row,
    attempts: list(data.attempts).map((value) => {
      const item = record(value)
      return {
        sequence: integer(item.sequence),
        group_id: integer(item.group_id),
        group_name: text(item.group_name),
        channel_id: optionalText(item.channel_id),
        credential_id: optionalNumber(item.credential_id),
        credential_name: text(item.credential_name),
        credential_deleted: item.credential_id != null && item.credential_name === '',
        operation: optionalText(item.operation),
        route_mode: optionalText(item.route_mode),
        upstream_model: optionalText(item.upstream_model),
        upstream_request_id: optionalText(item.upstream_request_id),
        dispatch_state: optionalText(item.dispatch_state),
        response_started: boolean(item.response_started),
        upstream_protocol: optionalText(item.upstream_protocol),
        reasoning: reasoning(item.reasoning),
        status_code: integer(item.status_code),
        duration_ms: integer(item.duration_ms),
        failure_category: text(item.failure_category),
        failure_origin: optionalText(item.failure_origin),
        failure_scope: optionalText(item.failure_scope),
        retry_directive: optionalText(item.retry_directive),
        effect: optionalText(item.effect),
        cooldown_until_ms: optionalNumber(item.cooldown_until_ms),
        rule_id: optionalText(item.rule_id),
        action: text(item.action),
        will_retry: boolean(item.will_retry),
        error_code: text(item.error_code),
        error_summary: text(item.error_summary),
        committed: boolean(item.committed),
        pricing_receipt: receipt(item.pricing_receipt),
      }
    }),
  }
}
export async function getLogAccessKeys(
  client: ApiClient,
  signal: AbortSignal,
): Promise<LogAccessKeyOption[]> {
  return list(await client.request('/api/access-keys/options', { signal })).map((value) => {
    const row = record(value)
    return { id: integer(row.id, 1), name: text(row.name), suffix: text(row.key_suffix) }
  })
}
