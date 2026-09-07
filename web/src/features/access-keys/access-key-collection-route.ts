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
  range: '7d',
  sort: 'updated_desc',
  page: 1,
  page_size: 20,
}
const statuses = new Set<AccessKeyCollectionStatus>(['active', 'disabled'])

export type AccessKeyDrawerRoute =
  { mode: 'create'; sourceAccessKeyID?: number } | { mode: 'edit'; accessKeyID: number }

export function parseAccessKeyDrawerRoute(query: LocationQuery): AccessKeyDrawerRoute | undefined {
  const action = scalarRouteQuery(query.action)
  if (action === 'create')
    return { mode: 'create', sourceAccessKeyID: parsePositiveRouteInteger(query.copy_from) }
  const accessKeyID = parsePositiveRouteInteger(query.access_key_id)
  return action === 'edit' && accessKeyID !== undefined ? { mode: 'edit', accessKeyID } : undefined
}

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

  for (const [key, allowed] of Object.entries({
    range: ['7d', '30d'],
    sort: ['updated_desc', 'cost_desc', 'expires_asc'],
    expiration: ['expiring', 'expired'],
    quota: ['available', 'exhausted'],
  })) {
    const value = scalarRouteQuery(query[key])
    if (value !== undefined && allowed.includes(value)) Object.assign(filters, { [key]: value })
  }
  if (q) filters.q = q
  if (status !== undefined && statuses.has(status as AccessKeyCollectionStatus)) {
    filters.status = status as AccessKeyCollectionStatus
  }
  if (page !== undefined) filters.page = page
  return filters
}

export function serializeAccessKeyCollectionRouteQuery(
  filters: AccessKeyCollectionFilters,
  drawer?: AccessKeyDrawerRoute,
): LocationQueryRaw {
  const query: LocationQueryRaw = {}
  const q = normalizeAccessKeyCollectionSearchQuery(filters.q)
  for (const key of ['range', 'sort', 'expiration', 'quota'] as const) {
    if (filters[key] !== undefined && filters[key] !== defaultFilters[key])
      query[key] = filters[key]
  }
  if (q) query.q = q
  if (filters.status !== undefined) query.status = filters.status
  if (filters.page !== defaultFilters.page) query.page = String(filters.page)
  if (drawer?.mode === 'create') {
    query.action = 'create'
    if (drawer.sourceAccessKeyID !== undefined) query.copy_from = String(drawer.sourceAccessKeyID)
  }
  if (drawer?.mode === 'edit') {
    query.action = 'edit'
    query.access_key_id = String(drawer.accessKeyID)
  }
  return query
}

export function isCanonicalAccessKeyCollectionRouteQuery(
  query: LocationQuery,
  filters: AccessKeyCollectionFilters,
  drawer?: AccessKeyDrawerRoute,
): boolean {
  return isCanonicalRouteQuery(query, serializeAccessKeyCollectionRouteQuery(filters, drawer))
}
