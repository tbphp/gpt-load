import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import { accessProtocols } from '@modern/api/access-keys'
import {
  internalLogFilters,
  logFilterNames,
  logOperations,
  logRequestPattern,
  logStatuses,
  type LogFilterName,
  type LogQuery,
} from '@modern/api/logs'
import { positivePage } from '@modern/app/url-state'
import type { DateRangePreset } from '@modern/components/ui/date-time'
import { readTimeRange, timeRangeQuery } from '@modern/app/time-range'

export interface LogRouteState {
  filters: LogQuery
  page: number
  more: boolean
  detail: string
  preset?: DateRangePreset
}
export interface LogFilterDefinition {
  key: LogFilterName
  section: 'request' | 'routing' | 'result' | 'metrics'
  kind: 'text' | 'select' | 'number' | 'money'
  values?: readonly string[]
  admin?: boolean
}
export const logFilterOptions: Partial<Record<LogFilterName, readonly string[]>> = {
  status: logStatuses,
  protocol: accessProtocols,
  operation: logOperations,
  stream: ['true', 'false'],
  cache_present: ['true', 'false'],
  usage_state: ['complete', 'partial', 'missing', 'not_applicable'],
  cost_state: ['priced', 'unpriced', 'not_applicable'],
  pricing_completeness: ['complete', 'partial', 'unavailable', 'not_applicable'],
  model_consistency: ['match', 'mismatch', 'unknown'],
  retry_state: ['retried', 'not_retried'],
  failure_category: [
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
  ],
}
export const advancedLogFilters: readonly LogFilterDefinition[] = [
  { key: 'request_id', section: 'request', kind: 'text' },
  { key: 'protocol', section: 'request', kind: 'select', values: accessProtocols },
  { key: 'operation', section: 'request', kind: 'select', values: logOperations },
  { key: 'stream', section: 'request', kind: 'select', values: logFilterOptions.stream },
  {
    key: 'retry_state',
    section: 'routing',
    kind: 'select',
    values: logFilterOptions.retry_state,
    admin: true,
  },
  { key: 'retry_count_min', section: 'routing', kind: 'number', admin: true },
  { key: 'retry_count_max', section: 'routing', kind: 'number', admin: true },
  { key: 'upstream_model', section: 'routing', kind: 'text', admin: true },
  { key: 'final_status_code', section: 'result', kind: 'number' },
  {
    key: 'model_consistency',
    section: 'result',
    kind: 'select',
    values: logFilterOptions.model_consistency,
    admin: true,
  },
  { key: 'attempt_status_code', section: 'result', kind: 'number', admin: true },
  {
    key: 'failure_category',
    section: 'result',
    kind: 'select',
    values: logFilterOptions.failure_category,
    admin: true,
  },
  { key: 'error_code', section: 'result', kind: 'text', admin: true },
  { key: 'usage_state', section: 'result', kind: 'select', values: logFilterOptions.usage_state },
  { key: 'cost_state', section: 'result', kind: 'select', values: logFilterOptions.cost_state },
  {
    key: 'pricing_completeness',
    section: 'result',
    kind: 'select',
    values: logFilterOptions.pricing_completeness,
  },
  {
    key: 'cache_present',
    section: 'result',
    kind: 'select',
    values: logFilterOptions.cache_present,
  },
  ...(['first_response', 'duration', 'input_tokens', 'output_tokens', 'cost'] as const).flatMap(
    (name): LogFilterDefinition[] => [
      {
        key: (name === 'cost'
          ? 'cost_min_nano_usd'
          : name === 'first_response' || name === 'duration'
            ? `${name}_min_ms`
            : `${name}_min`) as LogFilterName,
        section: 'metrics',
        kind: name === 'cost' ? 'money' : 'number',
      },
      {
        key: (name === 'cost'
          ? 'cost_max_nano_usd'
          : name === 'first_response' || name === 'duration'
            ? `${name}_max_ms`
            : `${name}_max`) as LogFilterName,
        section: 'metrics',
        kind: name === 'cost' ? 'money' : 'number',
      },
    ],
  ),
]
const maximumInteger = 9223372036854775807n
const unsigned = /^(?:0|[1-9]\d*)$/
export const logStateKeys = [
  ...logFilterNames,
  'preset',
  'page',
  'history',
  'filters',
  'detail',
] as const
export function logFilterErrors(filters: LogQuery): Partial<Record<LogFilterName, string>> {
  const errors: Partial<Record<LogFilterName, string>> = {}
  for (const key of logFilterNames) {
    const value = filters[key]
    if (!value) continue
    if (value.length > 256 || /[\u0000-\u001f\u007f]/u.test(value)) {
      errors[key] = 'invalidText'
      continue
    }
    if (logFilterOptions[key] && !logFilterOptions[key]!.includes(value))
      errors[key] = 'invalidValue'
    if (key === 'request_id' && !logRequestPattern.test(value)) errors[key] = 'invalidRequest'
    if (key === 'channel_id' && !/^[a-z][a-z0-9_]{0,99}$/.test(value)) errors[key] = 'invalidValue'
    if (key === 'limit' && !['20', '50', '100'].includes(value)) errors[key] = 'invalidValue'
    if (['group_id', 'credential_id', 'access_key_id', 'from_ms', 'to_ms'].includes(key)) {
      if (
        !unsigned.test(value) ||
        !Number.isSafeInteger(Number(value)) ||
        (key.endsWith('_id') && Number(value) < 1)
      )
        errors[key] = 'invalidNumber'
    }
    if (key.includes('_min') || key.includes('_max')) {
      if (!unsigned.test(value) || BigInt(value) > maximumInteger) errors[key] = 'invalidNumber'
    }
    if (key === 'final_status_code' || key === 'attempt_status_code') {
      const code = Number(value)
      if (!unsigned.test(value) || !(code === 0 || (code >= 100 && code <= 599)))
        errors[key] = 'invalidStatusCode'
    }
  }
  if (filters.from_ms && filters.to_ms && Number(filters.from_ms) >= Number(filters.to_ms))
    errors.to_ms = 'invalidRange'
  for (const key of logFilterNames.filter((name) => name.includes('_min'))) {
    const upper = key.replace('_min', '_max') as LogFilterName
    if (
      filters[key] &&
      filters[upper] &&
      !errors[key] &&
      !errors[upper] &&
      BigInt(filters[key]!) > BigInt(filters[upper]!)
    )
      errors[upper] = 'invalidRange'
  }
  return errors
}
export function parseLogState(query: LocationQuery, admin: boolean): LogRouteState {
  const filters: LogQuery = { limit: '20' }
  const range = readTimeRange(query)
  for (const key of logFilterNames) {
    const raw = query[key]
    // 用量排行按上游模型聚合；跨页带入时保留精确条件，不新增独立筛选控件。
    if (typeof raw === 'string' && raw.trim() && (admin || !internalLogFilters.includes(key)))
      filters[key] = raw.trim()
  }
  for (const key of Object.keys(logFilterErrors(filters)) as LogFilterName[]) delete filters[key]
  delete filters.from_ms
  delete filters.to_ms
  if (!range.preset) {
    filters.from_ms = range.from_ms
    filters.to_ms = range.to_ms
  }
  filters.limit ??= '20'
  return {
    filters,
    page: positivePage(query.page),
    more: query.filters === '1',
    detail:
      typeof query.detail === 'string' && logRequestPattern.test(query.detail) ? query.detail : '',
    preset: range.preset,
  }
}
export function serializeLogState(state: LogRouteState): LocationQueryRaw {
  const { from_ms, to_ms, ...filters } = state.filters
  return {
    ...filters,
    ...timeRangeQuery({ preset: state.preset, from_ms, to_ms }),
    ...(state.page > 1 ? { page: String(state.page) } : {}),
    ...(state.more ? { filters: '1' } : {}),
    ...(state.detail ? { detail: state.detail } : {}),
  }
}
export function nanoToUSD(value?: string): string {
  if (!value || !unsigned.test(value)) return ''
  const digits = value.padStart(10, '0')
  const fraction = digits.slice(-9).replace(/0+$/, '')
  return digits.slice(0, -9) + (fraction ? '.' + fraction : '')
}
export function usdToNano(value: string): string | undefined {
  const match = /^(0|[1-9]\d*)(?:\.(\d{1,9}))?$/.exec(value.trim())
  if (!match) return undefined
  const amount = BigInt(match[1]!) * 1000000000n + BigInt((match[2] ?? '').padEnd(9, '0'))
  return amount <= maximumInteger ? String(amount) : undefined
}
