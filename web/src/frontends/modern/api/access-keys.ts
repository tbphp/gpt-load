import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'
import { protocolOrder, sortProtocols } from '@modern/i18n/protocols'

export const accessKeySorts = ['updated_desc', 'cost_desc', 'expires_asc'] as const
export const accessProtocols = protocolOrder
export interface AccessScope {
  groups: number[]
  protocols: string[]
  models: string[]
  allowed_cidrs: string[]
}
export interface CostRule {
  id?: number
  kind: 'total' | 'periodic'
  limit_usd: string
  period_seconds?: number
}
export interface CostWindow extends CostRule {
  id: number
  used_usd: string
  remaining_usd: string
  status: 'available' | 'inactive' | 'exhausted'
  window_started_at_ms: number | null
  window_ends_at_ms: number | null
}
export interface AccessKey {
  id: number
  name: string
  masked_key: string
  status: 'active' | 'disabled'
  filters: AccessScope
  expires_at_ms: number | null
  rpm_limit: number
  price_multiplier: string
  cost_limit_rules: CostRule[]
  cost_limit_status: {
    allowed: boolean
    recoverable: boolean
    next_available_at_ms: number | null
    observed_at_ms: number
    rules: CostWindow[]
  } | null
  created_at_ms: number
  updated_at_ms: number
}
export interface AccessKeyRow extends AccessKey {
  expired: boolean
  last_request_at_ms: number | null
  usage?: { request_count: number; total_tokens: number; estimated_cost_nano_usd: string }
}
export interface AccessFilters {
  q: string
  status: '' | 'active' | 'disabled'
  sort: (typeof accessKeySorts)[number]
  page: number
  pageSize: number
  group: string
  expiry: '' | 'never' | 'active' | 'expired'
}
export interface AccessCollection {
  items: AccessKeyRow[]
  summary: { total: number; active: number; disabled: number }
  pagination: { page: number; page_size: number; total_items: number; total_pages: number }
  usage_window: { from_ms: number; to_ms: number; observed_at_ms: number }
}
export interface AccessInput {
  key?: string
  name: string
  status: AccessKey['status']
  filters: AccessScope
  expires_at_ms: number | null
  rpm_limit: number
  price_multiplier: string
  cost_limit_rules: CostRule[]
}
export const accessKeysKey = ['modern', 'access-keys'] as const
export const accessDetailKey = (id: number) => ['modern', 'access-key-detail', id] as const
const timestamp = (value: unknown) => (value == null ? null : integer(value))
const decimal = (value: unknown) => {
  const result = text(value)
  if (!/^(0|[1-9]\d*)(\.\d{1,9})?$/.test(result)) throw new InvalidResponseError()
  return result
}
function readRule(value: unknown): CostRule {
  const row = record(value)
  const kind = oneOf(row.kind, ['total', 'periodic'] as const)
  return {
    id: integer(row.id, 1),
    kind,
    limit_usd: decimal(row.limit_usd),
    ...(kind === 'periodic' ? { period_seconds: integer(row.period_seconds, 60) } : {}),
  }
}
function readWindow(value: unknown): CostWindow {
  const row = record(value)
  return {
    ...readRule(row),
    id: integer(row.id, 1),
    used_usd: decimal(row.used_usd),
    remaining_usd: decimal(row.remaining_usd),
    status: oneOf(row.status, ['available', 'inactive', 'exhausted'] as const),
    window_started_at_ms: timestamp(row.window_started_at_ms),
    window_ends_at_ms: timestamp(row.window_ends_at_ms),
  }
}
function readAccessKey(value: unknown): AccessKey {
  const row = record(value)
  const scope = record(row.filters)
  const runtime = row.cost_limit_status == null ? null : record(row.cost_limit_status)
  return {
    id: integer(row.id, 1),
    name: text(row.name),
    masked_key: text(row.masked_key),
    status: oneOf(row.status, ['active', 'disabled'] as const),
    filters: {
      groups: list(scope.groups).map((id) => integer(id, 1)),
      protocols: sortProtocols(list(scope.protocols).map(text)),
      models: list(scope.models).map(text),
      allowed_cidrs: list(scope.allowed_cidrs).map(text),
    },
    expires_at_ms: timestamp(row.expires_at_ms),
    rpm_limit: integer(row.rpm_limit),
    price_multiplier: decimal(row.price_multiplier),
    cost_limit_rules: list(row.cost_limit_rules).map(readRule),
    cost_limit_status: runtime
      ? {
          allowed: boolean(runtime.allowed),
          recoverable: boolean(runtime.recoverable),
          next_available_at_ms: timestamp(runtime.next_available_at_ms),
          observed_at_ms: integer(runtime.observed_at_ms),
          rules: list(runtime.rules).map(readWindow),
        }
      : null,
    created_at_ms: integer(row.created_at_ms),
    updated_at_ms: integer(row.updated_at_ms),
  }
}
export function readAccessKeyRow(value: unknown): AccessKeyRow {
  const row = record(value)
  const usage = row.usage == null ? undefined : record(row.usage)
  const cost = usage ? text(usage.estimated_cost_nano_usd) : undefined
  if (cost !== undefined && !/^\d+$/.test(cost)) throw new InvalidResponseError()
  return {
    ...readAccessKey(row),
    expired: boolean(row.expired),
    last_request_at_ms: timestamp(row.last_request_at_ms),
    ...(usage
      ? {
          usage: {
            request_count: integer(usage.request_count),
            total_tokens: integer(usage.total_tokens),
            estimated_cost_nano_usd: cost!,
          },
        }
      : {}),
  }
}
export async function getAccessKeys(
  client: ApiClient,
  filters: AccessFilters,
  signal: AbortSignal,
): Promise<AccessCollection> {
  const params = new URLSearchParams({
    page: String(filters.page),
    page_size: String(filters.pageSize),
    sort: filters.sort,
  })
  if (filters.q) params.set('q', filters.q)
  if (filters.status) params.set('status', filters.status)
  if (filters.group) params.set('group_id', filters.group)
  if (filters.expiry) params.set('expiry', filters.expiry)
  const data = record(await client.request(`/api/access-keys?${params}`, { signal }))
  const summary = record(data.summary)
  const pagination = record(data.pagination)
  const window = record(data.usage_window)
  return {
    items: list(data.items).map(readAccessKeyRow),
    summary: {
      total: integer(summary.total),
      active: integer(summary.active),
      disabled: integer(summary.disabled),
    },
    pagination: {
      page: integer(pagination.page, 1),
      page_size: integer(pagination.page_size, 1),
      total_items: integer(pagination.total_items),
      total_pages: integer(pagination.total_pages),
    },
    usage_window: {
      from_ms: integer(window.from_ms),
      to_ms: integer(window.to_ms),
      observed_at_ms: integer(window.observed_at_ms),
    },
  }
}
export async function getAccessKey(
  client: ApiClient,
  id: number,
  signal: AbortSignal,
  hint?: AccessFilters,
): Promise<AccessKeyRow | null> {
  // 没有单项 GET 接口；先查当前页，直接链接不在当前页时复用集合分页定位。
  if (hint) {
    const page = await getAccessKeys(client, hint, signal)
    const item = page.items.find((row) => row.id === id)
    if (item) return item
  }
  const filters: AccessFilters = {
    q: '',
    status: '',
    sort: 'updated_desc',
    page: 1,
    pageSize: 100,
    group: '',
    expiry: '',
  }
  let page = await getAccessKeys(client, filters, signal)
  const total = page.pagination.total_items
  const pages = page.pagination.total_pages
  for (let index = 1; index <= Math.max(1, pages); index++) {
    if (index > 1) page = await getAccessKeys(client, { ...filters, page: index }, signal)
    if (page.pagination.total_items !== total || page.pagination.total_pages !== pages)
      throw new InvalidResponseError()
    const item = page.items.find((row) => row.id === id)
    if (item) return item
  }
  return null
}
export async function createAccessKey(
  client: ApiClient,
  input: AccessInput,
  operation: string,
  signal: AbortSignal,
): Promise<AccessKey> {
  return readAccessKey(
    await client.request('/api/access-keys', {
      method: 'POST',
      json: input,
      headers: { 'Idempotency-Key': operation },
      signal,
    }),
  )
}
export async function updateAccessKey(
  client: ApiClient,
  id: number,
  patch: Partial<AccessInput>,
  signal: AbortSignal,
  operation?: string,
): Promise<AccessKey> {
  return readAccessKey(
    await client.request(`/api/access-keys/${id}`, {
      method: 'PUT',
      json: patch,
      ...(operation ? { headers: { 'Idempotency-Key': operation } } : {}),
      signal,
    }),
  )
}
export async function revealAccessKey(
  client: ApiClient,
  id: number,
  signal: AbortSignal,
): Promise<string> {
  const row = record(
    await client.request(`/api/access-keys/${id}/reveal`, { method: 'POST', signal }),
  )
  if (integer(row.id, 1) !== id) throw new InvalidResponseError()
  return text(row.key)
}
export async function deleteAccessKey(
  client: ApiClient,
  id: number,
  signal: AbortSignal,
): Promise<void> {
  await client.request(`/api/access-keys/${id}`, { method: 'DELETE', signal })
}
export async function resetAccessQuota(
  client: ApiClient,
  id: number,
  ruleIDs: number[],
  signal: AbortSignal,
): Promise<void> {
  await client.request(`/api/access-keys/${id}/cost-limits/reset`, {
    method: 'POST',
    json: { rule_ids: ruleIDs },
    signal,
  })
}
