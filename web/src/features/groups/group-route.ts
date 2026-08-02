import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type { GroupKeyCollectionFilters, GroupKeyStatus } from '@/api/control/types'
import {
  constrainCollectionSearch,
  isCanonicalRouteQuery,
  normalizeCollectionSearch,
  parsePositiveRouteInteger,
  scalarRouteQuery,
} from '@/app/route-query'

export type GroupTab = 'keys' | 'models' | 'settings'

const keyStatuses = new Set<GroupKeyStatus>(['available', 'cooldown', 'blacklisted', 'disabled'])
const keyPageSizes = new Set<GroupKeyCollectionFilters['page_size']>([20, 50, 100])

export function parsePositiveId(raw: unknown): number | undefined {
  if (typeof raw !== 'string' || !/^\d+$/u.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function normalizeGroupTab(raw: unknown): GroupTab {
  return raw === 'models' || raw === 'settings' || raw === 'keys' ? raw : 'keys'
}

export function normalizeGroupKeySearch(value: string | undefined): string | undefined {
  return normalizeCollectionSearch(value)
}

export function constrainGroupKeySearch(value: string | undefined): string | undefined {
  return constrainCollectionSearch(value)
}

export function parseGroupKeyRouteQuery(query: LocationQuery): GroupKeyCollectionFilters {
  const status = scalarRouteQuery(query.key_status)
  const pageSize = parsePositiveRouteInteger(query.page_size)
  const filters: GroupKeyCollectionFilters = {
    page: parsePositiveRouteInteger(query.page) ?? 1,
    page_size: keyPageSizes.has(pageSize as GroupKeyCollectionFilters['page_size'])
      ? (pageSize as GroupKeyCollectionFilters['page_size'])
      : 20,
  }
  const q = normalizeGroupKeySearch(scalarRouteQuery(query.q))
  if (q !== undefined) filters.q = q
  if (status !== undefined && keyStatuses.has(status as GroupKeyStatus)) {
    filters.status = status as GroupKeyStatus
  }
  return filters
}

export function serializeGroupKeyRouteQuery(filters: GroupKeyCollectionFilters): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'keys' }
  const q = normalizeGroupKeySearch(filters.q)
  if (q !== undefined) query.q = q
  if (filters.status !== undefined) query.key_status = filters.status
  if (filters.page !== 1) query.page = String(filters.page)
  if (filters.page_size !== 20) query.page_size = String(filters.page_size)
  return query
}

export function isCanonicalGroupKeyRouteQuery(
  query: LocationQuery,
  filters: GroupKeyCollectionFilters,
): boolean {
  return isCanonicalRouteQuery(query, serializeGroupKeyRouteQuery(filters))
}

export function normalizeGroupQuery(query: LocationQuery): LocationQueryRaw {
  const tab = normalizeGroupTab(query.tab)
  return tab === 'keys' ? serializeGroupKeyRouteQuery(parseGroupKeyRouteQuery(query)) : { tab }
}
