import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type {
  ConnectionType,
  GroupCollectionFilters,
  GroupCollectionSort,
  GroupCollectionStatus,
} from '@/api/control/types'
import {
  constrainCollectionSearch,
  isCanonicalRouteQuery,
  normalizeCollectionSearch,
  parsePositiveRouteInteger,
  scalarRouteQuery,
} from '@/app/route-query'

const defaultFilters: GroupCollectionFilters = {
  sort: 'status',
  page: 1,
  page_size: 20,
}
const statuses = new Set<GroupCollectionStatus>(['available', 'unavailable', 'disabled'])
const connectionTypes = new Set<ConnectionType>(['api_key', 'subscription'])
const sorts = new Set<GroupCollectionSort>(['status', 'name', 'credentials', 'created'])

export function normalizeGroupCollectionSearchQuery(value: string | undefined): string | undefined {
  return normalizeCollectionSearch(value)
}

export function constrainGroupCollectionSearchQuery(value: string | undefined): string | undefined {
  return constrainCollectionSearch(value)
}

export function parseGroupCollectionRouteQuery(query: LocationQuery): GroupCollectionFilters {
  const filters: GroupCollectionFilters = { ...defaultFilters }
  const q = normalizeGroupCollectionSearchQuery(scalarRouteQuery(query.q))
  const status = scalarRouteQuery(query.status)
  const connectionType = scalarRouteQuery(query.connection_type)
  const sort = scalarRouteQuery(query.sort)
  const page = parsePositiveRouteInteger(query.page)

  if (q) filters.q = q
  if (status !== undefined && statuses.has(status as GroupCollectionStatus)) {
    filters.status = status as GroupCollectionStatus
  }
  if (connectionType !== undefined && connectionTypes.has(connectionType as ConnectionType)) {
    filters.connection_type = connectionType as ConnectionType
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
  if (filters.connection_type !== undefined) query.connection_type = filters.connection_type
  if (filters.sort !== defaultFilters.sort) query.sort = filters.sort
  if (filters.page !== defaultFilters.page) query.page = String(filters.page)
  return query
}

export function isCanonicalGroupCollectionRouteQuery(
  query: LocationQuery,
  filters: GroupCollectionFilters,
): boolean {
  return isCanonicalRouteQuery(query, serializeGroupCollectionRouteQuery(filters))
}
