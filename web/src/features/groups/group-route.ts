import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import type { CredentialCollectionFilters, CredentialStatus } from '@/api/control/types'
import {
  constrainCollectionSearch,
  isCanonicalRouteQuery,
  normalizeCollectionSearch,
  parsePositiveRouteInteger,
  parsePositiveRouteIntegerList,
  scalarRouteQuery,
  serializePositiveRouteIntegerList,
} from '@/app/route-query'

export type GroupTab = 'credentials' | 'models' | 'settings'
export type GroupSettingsSection = 'general' | 'routing' | 'runtime' | 'headers' | 'danger'
export type GroupModelDiscoveryFilter = 'unadded' | 'all'

export interface CredentialRouteState {
  expandedCredentialIDs: number[]
  weightCredentialID?: number
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
}

const credentialStatuses = new Set<CredentialStatus>([
  'available',
  'cooldown',
  'blacklisted',
  'disabled',
])
const credentialPageSizes = new Set<CredentialCollectionFilters['page_size']>([20, 50, 100])
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
  return raw === 'models' || raw === 'settings' || raw === 'credentials' ? raw : 'credentials'
}

export function normalizeCredentialSearch(value: string | undefined): string | undefined {
  return normalizeCollectionSearch(value)
}

export function constrainCredentialSearch(value: string | undefined): string | undefined {
  return constrainCollectionSearch(value)
}

export function parseCredentialRouteQuery(query: LocationQuery): CredentialCollectionFilters {
  const status = scalarRouteQuery(query.credential_status)
  const pageSize = parsePositiveRouteInteger(query.page_size)
  const filters: CredentialCollectionFilters = {
    page: parsePositiveRouteInteger(query.page) ?? 1,
    page_size: credentialPageSizes.has(pageSize as CredentialCollectionFilters['page_size'])
      ? (pageSize as CredentialCollectionFilters['page_size'])
      : 20,
  }
  const q = normalizeCredentialSearch(scalarRouteQuery(query.q))
  if (q !== undefined) filters.q = q
  if (status !== undefined && credentialStatuses.has(status as CredentialStatus)) {
    filters.status = status as CredentialStatus
  }
  return filters
}

export function parseCredentialRouteState(query: LocationQuery): CredentialRouteState {
  return {
    expandedCredentialIDs: parsePositiveRouteIntegerList(query.expanded_credential_ids),
    weightCredentialID: parsePositiveRouteInteger(query.weight_credential_id),
  }
}

export function serializeCredentialRouteQuery(
  filters: CredentialCollectionFilters,
  state: CredentialRouteState = { expandedCredentialIDs: [] },
): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'credentials' }
  const q = normalizeCredentialSearch(filters.q)
  if (q !== undefined) query.q = q
  if (filters.status !== undefined) query.credential_status = filters.status
  if (filters.page !== 1) query.page = String(filters.page)
  if (filters.page_size !== 20) query.page_size = String(filters.page_size)
  const expanded = serializePositiveRouteIntegerList(state.expandedCredentialIDs)
  if (expanded !== undefined) query.expanded_credential_ids = expanded
  if (state.weightCredentialID !== undefined)
    query.weight_credential_id = String(state.weightCredentialID)
  return query
}

export function isCanonicalCredentialRouteQuery(
  query: LocationQuery,
  filters: CredentialCollectionFilters,
  state: CredentialRouteState = { expandedCredentialIDs: [] },
): boolean {
  return isCanonicalRouteQuery(query, serializeCredentialRouteQuery(filters, state))
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
  }
}

export function serializeGroupSettingsRouteQuery(state: GroupSettingsRouteState): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'settings' }
  if (state.section !== 'general') query.section = state.section
  if (state.headerRulesExpanded) query.headers = 'expanded'
  return query
}

export function normalizeGroupQuery(query: LocationQuery): LocationQueryRaw {
  const tab = normalizeGroupTab(query.tab)
  if (tab === 'credentials') {
    return serializeCredentialRouteQuery(
      parseCredentialRouteQuery(query),
      parseCredentialRouteState(query),
    )
  }
  if (tab === 'models') return serializeGroupModelsRouteQuery(parseGroupModelsRouteQuery(query))
  return serializeGroupSettingsRouteQuery(parseGroupSettingsRouteQuery(query))
}
