import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type {
  GroupCollectionFilters,
  GroupCollectionSort,
  GroupCollectionStatus,
  GroupProtocol,
} from '@/api/control/types'

const defaultFilters: GroupCollectionFilters = {
  sort: 'status',
  page: 1,
  page_size: 20,
}
const statuses = new Set<GroupCollectionStatus>(['available', 'unavailable', 'disabled'])
const protocols = new Set<GroupProtocol>([
  'openai-completions',
  'openai-responses',
  'anthropic',
  'gemini',
])
const sorts = new Set<GroupCollectionSort>(['status', 'name', 'keys', 'created'])
const maxSearchCodePoints = 200

function scalar(queryValue: LocationQuery[string]): string | undefined {
  return typeof queryValue === 'string' ? queryValue : undefined
}

function canonicalPositiveInteger(value: LocationQuery[string]): number | undefined {
  const candidate = scalar(value)
  if (candidate === undefined || !/^[1-9]\d*$/.test(candidate)) return undefined
  const parsed = Number(candidate)
  return Number.isSafeInteger(parsed) ? parsed : undefined
}

export function normalizeGroupCollectionSearchQuery(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).length <= maxSearchCodePoints ? trimmed : undefined
}

export function constrainGroupCollectionSearchQuery(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).slice(0, maxSearchCodePoints).join('')
}

export function parseGroupCollectionRouteQuery(query: LocationQuery): GroupCollectionFilters {
  const filters: GroupCollectionFilters = { ...defaultFilters }
  const q = normalizeGroupCollectionSearchQuery(scalar(query.q))
  const status = scalar(query.status)
  const protocol = scalar(query.protocol)
  const sort = scalar(query.sort)
  const page = canonicalPositiveInteger(query.page)

  if (q) filters.q = q
  if (status !== undefined && statuses.has(status as GroupCollectionStatus)) {
    filters.status = status as GroupCollectionStatus
  }
  if (protocol !== undefined && protocols.has(protocol as GroupProtocol)) {
    filters.protocol = protocol as GroupProtocol
  }
  if (sort !== undefined && sorts.has(sort as GroupCollectionSort)) {
    filters.sort = sort as GroupCollectionSort
  }
  if (page !== undefined) filters.page = page

  return filters
}

export function serializeGroupCollectionRouteQuery(
  filters: GroupCollectionFilters,
): LocationQueryRaw {
  const query: LocationQueryRaw = {}
  const q = normalizeGroupCollectionSearchQuery(filters.q)
  if (q) query.q = q
  if (filters.status !== undefined) query.status = filters.status
  if (filters.protocol !== undefined) query.protocol = filters.protocol
  if (filters.sort !== defaultFilters.sort) query.sort = filters.sort
  if (filters.page !== defaultFilters.page) query.page = String(filters.page)
  return query
}

export function isCanonicalGroupCollectionRouteQuery(
  query: LocationQuery,
  filters: GroupCollectionFilters,
): boolean {
  const canonical = serializeGroupCollectionRouteQuery(filters)
  const actualKeys = Object.keys(query)
  const canonicalKeys = Object.keys(canonical)
  if (actualKeys.length !== canonicalKeys.length) return false
  return canonicalKeys.every((key) => query[key] === canonical[key])
}
