import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type { AccessKeyCollectionFilters, AccessKeyCollectionStatus } from '@/api/control/types'
import {
  constrainCollectionSearch,
  isCanonicalRouteQuery,
  normalizeCollectionSearch,
  parsePositiveRouteInteger,
  scalarRouteQuery,
} from '@/app/route-query'

const defaultFilters: AccessKeyCollectionFilters = {
  page: 1,
  page_size: 20,
}
const statuses = new Set<AccessKeyCollectionStatus>(['active', 'disabled'])

export function normalizeAccessKeyCollectionSearchQuery(
  value: string | undefined,
): string | undefined {
  return normalizeCollectionSearch(value)
}

export function constrainAccessKeyCollectionSearchQuery(
  value: string | undefined,
): string | undefined {
  return constrainCollectionSearch(value)
}

export function parseAccessKeyCollectionRouteQuery(
  query: LocationQuery,
): AccessKeyCollectionFilters {
  const filters: AccessKeyCollectionFilters = { ...defaultFilters }
  const q = normalizeAccessKeyCollectionSearchQuery(scalarRouteQuery(query.q))
  const status = scalarRouteQuery(query.status)
  const page = parsePositiveRouteInteger(query.page)

  if (q) filters.q = q
  if (status !== undefined && statuses.has(status as AccessKeyCollectionStatus)) {
    filters.status = status as AccessKeyCollectionStatus
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
  if (filters.page !== defaultFilters.page) query.page = String(filters.page)
  return query
}

export function isCanonicalAccessKeyCollectionRouteQuery(
  query: LocationQuery,
  filters: AccessKeyCollectionFilters,
): boolean {
  return isCanonicalRouteQuery(query, serializeAccessKeyCollectionRouteQuery(filters))
}
