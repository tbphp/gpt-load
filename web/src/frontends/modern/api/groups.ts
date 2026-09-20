import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'

export const groupViews = ['all', 'serving', 'attention', 'paused'] as const
export const groupSorts = ['recent', 'priority', 'name'] as const
export const availabilityStates = [
  'ready',
  'limited',
  'paused',
  'no_credentials',
  'no_models',
  'unavailable',
] as const
export type GroupAvailability = (typeof availabilityStates)[number]
export interface GroupFilters {
  page: number
  pageSize: number
  q: string
  view: (typeof groupViews)[number]
  channel: string
  connection: '' | 'api_key' | 'subscription'
  model: string
  credential: string
  protocol: string
  sort: (typeof groupSorts)[number]
}
export interface CredentialCounts {
  total: number
  available: number
  cooldown: number
  blacklisted: number
  disabled: number
  modelCooldown: number
}
export interface GroupRow {
  id: number
  name: string
  channelID: string
  channelName: string
  channelMark: string
  channelIcon: string
  connectionType: 'api_key' | 'subscription'
  endpoint: string
  enabled: boolean
  availability: GroupAvailability
  weight: number
  priceMultiplier: string
  modelCount: number
  modelNames: string[]
  credentials: CredentialCounts
  lastActiveHour: number | null
  lastActiveHourRequests: number
}
export interface GroupWorkspace {
  autoModels?: string[]
  observedAt: number
  items: GroupRow[]
}
export interface GroupBasics {
  name: string
  enabled: boolean
  weight: number | null
  priceMultiplier: string
}
export type GroupBasicsPatch = Partial<{
  name: string
  enabled: boolean
  weight_manual: number | null
  price_multiplier: string
}>
export const groupQueryKey = ['modern', 'groups', 'workspace'] as const
export const credentialOptionsKey = ['modern', 'credential-options'] as const

export interface CredentialOption {
  key: string
  channelID: string
  label: string
  groupIDs: number[]
}

export function readCredentialFilterKey(value: unknown): string {
  const key = text(value)
  if (!/^[a-f0-9]{64}$/u.test(key)) throw new InvalidResponseError()
  return key
}

export async function getCredentialOptions(client: ApiClient, signal: AbortSignal) {
  const data = record(await client.request('/api/modern/credentials/options', { signal }))
  return list(data.items).map((value): CredentialOption => {
    const option = record(value)
    return {
      key: readCredentialFilterKey(option.key),
      channelID: text(option.channel_id),
      label: text(option.label),
      groupIDs: list(option.group_ids).map((value) => integer(value, 1)),
    }
  })
}

export async function deleteGroup(
  client: ApiClient,
  id: number,
  signal: AbortSignal,
): Promise<void> {
  await client.request(`/api/groups/${id}`, { method: 'DELETE', signal })
}

export function needsAttention(group: GroupRow): boolean {
  return !isPaused(group) && group.availability !== 'ready'
}
export function isPaused(group: GroupRow): boolean {
  return group.availability === 'paused'
}
export function isServing(group: GroupRow): boolean {
  return group.availability === 'ready' || group.availability === 'limited'
}

export async function getGroupWorkspace(
  client: ApiClient,
  signal: AbortSignal,
): Promise<GroupWorkspace> {
  const data = record(await client.request<unknown>('/api/modern/groups', { signal }))
  const items = list(data.items).map((value): GroupRow => {
    const item = record(value)
    const counts = record(item.credentials)
    return {
      id: integer(item.id, 1),
      name: text(item.name),
      channelID: text(item.channel_id),
      channelName: text(item.channel_name),
      channelMark: text(item.channel_mark),
      channelIcon: text(item.channel_icon ?? ''),
      connectionType: oneOf(item.connection_type, ['api_key', 'subscription']),
      endpoint: text(item.endpoint),
      enabled: boolean(item.enabled),
      availability: oneOf(item.availability, availabilityStates),
      weight: integer(item.weight),
      priceMultiplier: text(item.price_multiplier),
      modelCount: integer(item.model_count),
      modelNames: list(item.model_names).map(text),
      lastActiveHour: item.last_active_hour_ms === null ? null : integer(item.last_active_hour_ms),
      lastActiveHourRequests: integer(item.last_active_hour_requests),
      credentials: {
        total: integer(counts.total),
        available: integer(counts.available),
        cooldown: integer(counts.cooldown),
        blacklisted: integer(counts.blacklisted),
        disabled: integer(counts.disabled),
        modelCooldown: integer(counts.model_cooldown),
      },
    }
  })
  if (new Set(items.map((item) => item.id)).size !== items.length) throw new InvalidResponseError()
  return {
    observedAt: integer(data.observed_at_ms),
    autoModels: data.auto_models === undefined ? [] : list(data.auto_models).map(text),
    items,
  }
}

export async function getGroupModelNames(client: ApiClient, id: number, signal: AbortSignal) {
  const data = record(await client.request<unknown>(`/api/groups/${id}/models`, { signal }))
  return list(data.items).map((value) => {
    const model = record(value)
    return { id: text(model.id), name: text(model.client_model) }
  })
}

export function readGroupBasics(value: unknown): GroupBasics {
  const data = record(value)
  const weight = data.weight_manual === null ? null : integer(data.weight_manual)
  if (weight !== null && weight > 100) throw new InvalidResponseError()
  return {
    name: text(data.name),
    enabled: boolean(data.enabled),
    weight,
    priceMultiplier: text(data.price_multiplier),
  }
}
export async function getGroupBasics(client: ApiClient, id: number, signal: AbortSignal) {
  return readGroupBasics(await client.request<unknown>(`/api/groups/${id}/settings`, { signal }))
}
export async function updateGroupBasics(
  client: ApiClient,
  id: number,
  patch: GroupBasicsPatch,
  signal: AbortSignal,
) {
  return readGroupBasics(
    await client.request<unknown>(`/api/groups/${id}/settings`, {
      method: 'PUT',
      json: patch,
      signal,
    }),
  )
}

export interface GroupUsage {
  id: number
  requests: number
  successes: number
  tokens: number
  costNanoUSD: string
  incomplete: boolean
}

export interface GroupUsageBatch {
  from: number
  to: number
  observedAt: number
  incomplete: boolean
  items: GroupUsage[]
}

export async function getGroupUsage(
  client: ApiClient,
  ids: number[],
  signal: AbortSignal,
): Promise<GroupUsageBatch> {
  const params = new URLSearchParams({ group_ids: ids.join(',') })
  const data = record(
    await client.request<unknown>(`/api/modern/groups/usage?${params}`, { signal }),
  )
  const from = integer(data.from_ms)
  const to = integer(data.to_ms)
  if (to <= from) throw new InvalidResponseError()
  const health = record(data.collection_health)
  const incomplete =
    !boolean(data.data_complete) ||
    integer(health.dropped_total) > 0 ||
    integer(health.write_failure_total) > 0
  const items = list(data.items).map((raw): GroupUsage => {
    const item = record(raw)
    const cost = text(item.estimated_cost_nano_usd)
    if (!/^(0|[1-9]\d*)$/u.test(cost)) throw new InvalidResponseError()
    const requests = integer(item.request_count)
    const successes = integer(item.success_count)
    if (successes > requests) throw new InvalidResponseError()
    return {
      id: integer(item.group_id, 1),
      requests,
      successes,
      tokens: integer(item.total_tokens),
      costNanoUSD: cost,
      incomplete:
        integer(item.usage_missing_count) > 0 ||
        integer(item.partial_count) > 0 ||
        integer(item.unpriced_request_count) > 0 ||
        integer(item.pricing_partial_count) > 0,
    }
  })
  const returnedIDs = new Set(items.map((item) => item.id))
  if (
    items.length !== ids.length ||
    returnedIDs.size !== ids.length ||
    ids.some((id) => !returnedIDs.has(id))
  )
    throw new InvalidResponseError()
  return { from, to, observedAt: integer(data.observed_at_ms), incomplete, items }
}
