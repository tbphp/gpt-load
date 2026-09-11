import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'

export const groupViews = ['all', 'serving', 'attention', 'paused'] as const
export const groupSorts = ['priority', 'recent', 'name'] as const
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
  q: string
  view: (typeof groupViews)[number]
  channel: string
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
  connectionType: 'api_key' | 'subscription'
  endpoint: string
  enabled: boolean
  availability: GroupAvailability
  weight: number
  priceMultiplier: string
  modelCount: number
  modelPreview: string[]
  credentials: CredentialCounts
  lastActiveHour: number | null
  lastActiveHourRequests: number
}
export interface GroupWorkspace {
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

function record(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value))
    throw new InvalidResponseError()
  return value as Record<string, unknown>
}
function text(value: unknown): string {
  if (typeof value !== 'string') throw new InvalidResponseError()
  return value
}
function integer(value: unknown, min = 0): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min)
    throw new InvalidResponseError()
  return value
}
function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new InvalidResponseError()
  return value
}
function oneOf<T extends string>(value: unknown, values: readonly T[]): T {
  if (!values.includes(value as T)) throw new InvalidResponseError()
  return value as T
}
function list(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value
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
      connectionType: oneOf(item.connection_type, ['api_key', 'subscription']),
      endpoint: text(item.endpoint),
      enabled: boolean(item.enabled),
      availability: oneOf(item.availability, availabilityStates),
      weight: integer(item.weight),
      priceMultiplier: text(item.price_multiplier),
      modelCount: integer(item.model_count),
      modelPreview: list(item.model_preview).map(text),
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
  return { observedAt: integer(data.observed_at_ms), items }
}

export async function getGroupModelNames(client: ApiClient, id: number, signal: AbortSignal) {
  const data = record(await client.request<unknown>(`/api/groups/${id}/models`, { signal }))
  return list(data.items).map((value) => {
    const model = record(value)
    return { id: text(model.id), name: text(model.client_model) }
  })
}

function basics(value: unknown): GroupBasics {
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
  return basics(await client.request<unknown>(`/api/groups/${id}/settings`, { signal }))
}
export async function updateGroupBasics(
  client: ApiClient,
  id: number,
  patch: GroupBasicsPatch,
  signal: AbortSignal,
) {
  return basics(
    await client.request<unknown>(`/api/groups/${id}/settings`, {
      method: 'PUT',
      json: patch,
      signal,
    }),
  )
}
