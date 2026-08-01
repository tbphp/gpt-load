import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type {
  AccessKeyCollectionFilters,
  AccessKeyCollectionScope,
  AccessKeyCollectionStatus,
} from '@/api/control/types'

const defaultFilters: AccessKeyCollectionFilters = {
  page: 1,
  page_size: 20,
}
const statuses = new Set<AccessKeyCollectionStatus>(['active', 'disabled'])
const scopes = new Set<AccessKeyCollectionScope>(['unlimited', 'restricted'])
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

export function normalizeAccessKeyCollectionSearchQuery(
  value: string | undefined,
): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).length <= maxSearchCodePoints ? trimmed : undefined
}

export function constrainAccessKeyCollectionSearchQuery(
  value: string | undefined,
): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).slice(0, maxSearchCodePoints).join('')
}

export function parseAccessKeyCollectionRouteQuery(
  query: LocationQuery,
): AccessKeyCollectionFilters {
  const filters: AccessKeyCollectionFilters = { ...defaultFilters }
  const q = normalizeAccessKeyCollectionSearchQuery(scalar(query.q))
  const status = scalar(query.status)
  const scope = scalar(query.scope)
  const page = canonicalPositiveInteger(query.page)

  if (q) filters.q = q
  if (status !== undefined && statuses.has(status as AccessKeyCollectionStatus)) {
    filters.status = status as AccessKeyCollectionStatus
  }
  if (scope !== undefined && scopes.has(scope as AccessKeyCollectionScope)) {
    filters.scope = scope as AccessKeyCollectionScope
  }
  if (page !== undefined) filters.page = page
  return filters
}

export function serializeAccessKeyCollectionRouteQuery(
  filters: AccessKeyCollectionFilters,
): LocationQueryRaw {
  const query: LocationQueryRaw = {}
  const q = normalizeAccessKeyCollectionSearchQuery(filters.q)
  if (q) query.q = q
  if (filters.status !== undefined) query.status = filters.status
  if (filters.scope !== undefined) query.scope = filters.scope
  if (filters.page !== defaultFilters.page) query.page = String(filters.page)
  return query
}

export function isCanonicalAccessKeyCollectionRouteQuery(
  query: LocationQuery,
  filters: AccessKeyCollectionFilters,
): boolean {
  const canonical = serializeAccessKeyCollectionRouteQuery(filters)
  const actualKeys = Object.keys(query)
  const canonicalKeys = Object.keys(canonical)
  if (actualKeys.length !== canonicalKeys.length) return false
  return canonicalKeys.every((key) => query[key] === canonical[key])
}
