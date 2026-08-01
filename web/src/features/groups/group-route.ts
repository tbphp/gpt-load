import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type { GroupKeyCollectionFilters, GroupKeyStatus } from '@/api/control/types'

export type GroupTab = 'keys' | 'models' | 'settings'

const keyStatuses = new Set<GroupKeyStatus>(['available', 'cooldown', 'blacklisted', 'disabled'])
const keyPageSizes = new Set<GroupKeyCollectionFilters['page_size']>([20, 50, 100])
const maxSearchCodePoints = 200

function scalar(value: LocationQuery[string]): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function positiveInteger(value: LocationQuery[string]): number | undefined {
  const candidate = scalar(value)
  if (candidate === undefined || !/^[1-9]\d*$/u.test(candidate)) return undefined
  const parsed = Number(candidate)
  return Number.isSafeInteger(parsed) ? parsed : undefined
}

export function parsePositiveId(raw: unknown): number | undefined {
  if (typeof raw !== 'string' || !/^\d+$/u.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function normalizeGroupTab(raw: unknown): GroupTab {
  return raw === 'models' || raw === 'settings' || raw === 'keys' ? raw : 'keys'
}

export function normalizeGroupKeySearch(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).length <= maxSearchCodePoints ? trimmed : undefined
}

export function constrainGroupKeySearch(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).slice(0, maxSearchCodePoints).join('')
}

export function parseGroupKeyRouteQuery(query: LocationQuery): GroupKeyCollectionFilters {
  const status = scalar(query.key_status)
  const pageSize = positiveInteger(query.page_size)
  const filters: GroupKeyCollectionFilters = {
    page: positiveInteger(query.page) ?? 1,
    page_size: keyPageSizes.has(pageSize as GroupKeyCollectionFilters['page_size'])
      ? (pageSize as GroupKeyCollectionFilters['page_size'])
      : 20,
  }
  const q = normalizeGroupKeySearch(scalar(query.q))
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

export function normalizeGroupQuery(query: LocationQuery): LocationQueryRaw {
  const tab = normalizeGroupTab(query.tab)
  return tab === 'keys' ? serializeGroupKeyRouteQuery(parseGroupKeyRouteQuery(query)) : { tab }
}
