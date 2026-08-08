import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type { GroupKeyCollectionFilters, GroupKeyStatus } from '@/api/control/types'
import {
  constrainCollectionSearch,
  isCanonicalRouteQuery,
  normalizeCollectionSearch,
  parsePositiveRouteInteger,
  parsePositiveRouteIntegerList,
  scalarRouteQuery,
  serializePositiveRouteIntegerList,
} from '@/app/route-query'

export type GroupTab = 'keys' | 'models' | 'settings'
export type GroupSettingsSection = 'general' | 'routing' | 'runtime' | 'headers' | 'danger'
export type GroupModelDiscoveryFilter = 'unadded' | 'all'

export interface GroupKeyRouteState {
  expandedKeyIDs: number[]
  weightKeyID?: number
}

export interface GroupModelsRouteState {
  search?: string
  discoveryOpen: boolean
  discoverySearch?: string
  discoveryFilter: GroupModelDiscoveryFilter
}

export interface GroupSettingsRouteState {
  section: GroupSettingsSection
  headerRulesExpanded: boolean
  providerSearch?: string
}

const keyStatuses = new Set<GroupKeyStatus>(['available', 'cooldown', 'blacklisted', 'disabled'])
const keyPageSizes = new Set<GroupKeyCollectionFilters['page_size']>([20, 50, 100])
const settingsSections = new Set<GroupSettingsSection>([
  'general',
  'routing',
  'runtime',
  'headers',
  'danger',
])

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

export function parseGroupKeyRouteState(query: LocationQuery): GroupKeyRouteState {
  return {
    expandedKeyIDs: parsePositiveRouteIntegerList(query.expanded_key_ids),
    weightKeyID: parsePositiveRouteInteger(query.weight_key_id),
  }
}

export function serializeGroupKeyRouteQuery(
  filters: GroupKeyCollectionFilters,
  state: GroupKeyRouteState = { expandedKeyIDs: [] },
): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'keys' }
  const q = normalizeGroupKeySearch(filters.q)
  if (q !== undefined) query.q = q
  if (filters.status !== undefined) query.key_status = filters.status
  if (filters.page !== 1) query.page = String(filters.page)
  if (filters.page_size !== 20) query.page_size = String(filters.page_size)
  const expanded = serializePositiveRouteIntegerList(state.expandedKeyIDs)
  if (expanded !== undefined) query.expanded_key_ids = expanded
  if (state.weightKeyID !== undefined) query.weight_key_id = String(state.weightKeyID)
  return query
}

export function isCanonicalGroupKeyRouteQuery(
  query: LocationQuery,
  filters: GroupKeyCollectionFilters,
  state: GroupKeyRouteState = { expandedKeyIDs: [] },
): boolean {
  return isCanonicalRouteQuery(query, serializeGroupKeyRouteQuery(filters, state))
}

export function parseGroupModelsRouteQuery(query: LocationQuery): GroupModelsRouteState {
  const discoveryOpen = scalarRouteQuery(query.panel) === 'discovery'
  return {
    search: normalizeCollectionSearch(scalarRouteQuery(query.q)),
    discoveryOpen,
    discoverySearch: discoveryOpen
      ? normalizeCollectionSearch(scalarRouteQuery(query.discovery_q))
      : undefined,
    discoveryFilter:
      discoveryOpen && scalarRouteQuery(query.discovery_filter) === 'all' ? 'all' : 'unadded',
  }
}

export function serializeGroupModelsRouteQuery(state: GroupModelsRouteState): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'models' }
  const search = normalizeCollectionSearch(state.search)
  if (search !== undefined) query.q = search
  if (state.discoveryOpen) {
    query.panel = 'discovery'
    const discoverySearch = normalizeCollectionSearch(state.discoverySearch)
    if (discoverySearch !== undefined) query.discovery_q = discoverySearch
    if (state.discoveryFilter === 'all') query.discovery_filter = 'all'
  }
  return query
}

export function parseGroupSettingsRouteQuery(query: LocationQuery): GroupSettingsRouteState {
  const rawSection = scalarRouteQuery(query.section)
  return {
    section:
      rawSection !== undefined && settingsSections.has(rawSection as GroupSettingsSection)
        ? (rawSection as GroupSettingsSection)
        : 'general',
    headerRulesExpanded: scalarRouteQuery(query.headers) === 'expanded',
    providerSearch: normalizeCollectionSearch(scalarRouteQuery(query.provider_q)),
  }
}

export function serializeGroupSettingsRouteQuery(state: GroupSettingsRouteState): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'settings' }
  if (state.section !== 'general') query.section = state.section
  if (state.headerRulesExpanded) query.headers = 'expanded'
  const providerSearch = normalizeCollectionSearch(state.providerSearch)
  if (providerSearch !== undefined) query.provider_q = providerSearch
  return query
}

export function normalizeGroupQuery(query: LocationQuery): LocationQueryRaw {
  const tab = normalizeGroupTab(query.tab)
  if (tab === 'keys') {
    return serializeGroupKeyRouteQuery(
      parseGroupKeyRouteQuery(query),
      parseGroupKeyRouteState(query),
    )
  }
  if (tab === 'models') return serializeGroupModelsRouteQuery(parseGroupModelsRouteQuery(query))
  return serializeGroupSettingsRouteQuery(parseGroupSettingsRouteQuery(query))
}
