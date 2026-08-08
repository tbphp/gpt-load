import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import {
  isCanonicalRouteQuery,
  normalizeCollectionSearch,
  parsePositiveRouteInteger,
  scalarRouteQuery,
} from '@/app/route-query'

export type ImportMode = 'new' | 'existing'
export type ImportPanel = 'providers' | 'discovery'
export type ImportDiscoveryFilter = 'unadded' | 'all'

export interface ImportRouteState {
  mode: ImportMode
  groupID?: number
  panel?: ImportPanel
  providerSearch?: string
  modelSearch?: string
  discoverySearch?: string
  discoveryFilter: ImportDiscoveryFilter
}

export function parseImportRouteQuery(query: LocationQuery): ImportRouteState {
  const groupID = parsePositiveRouteInteger(query.group_id)
  const mode =
    scalarRouteQuery(query.mode) === 'existing' ||
    Object.prototype.hasOwnProperty.call(query, 'group_id')
      ? 'existing'
      : 'new'
  if (mode === 'existing') {
    return {
      mode,
      groupID,
      discoveryFilter: 'unadded',
    }
  }

  const rawPanel = scalarRouteQuery(query.panel)
  const panel = rawPanel === 'providers' || rawPanel === 'discovery' ? rawPanel : undefined
  return {
    mode,
    panel,
    providerSearch: normalizeCollectionSearch(scalarRouteQuery(query.provider_q)),
    modelSearch: normalizeCollectionSearch(scalarRouteQuery(query.model_q)),
    discoverySearch:
      panel === 'discovery'
        ? normalizeCollectionSearch(scalarRouteQuery(query.discovery_q))
        : undefined,
    discoveryFilter:
      panel === 'discovery' && scalarRouteQuery(query.discovery_filter) === 'all'
        ? 'all'
        : 'unadded',
  }
}

export function serializeImportRouteQuery(state: ImportRouteState): LocationQueryRaw {
  const query: LocationQueryRaw = { mode: state.mode }
  if (state.mode === 'existing') {
    if (state.groupID !== undefined) query.group_id = String(state.groupID)
    return query
  }

  const providerSearch = normalizeCollectionSearch(state.providerSearch)
  const modelSearch = normalizeCollectionSearch(state.modelSearch)
  if (providerSearch !== undefined) query.provider_q = providerSearch
  if (modelSearch !== undefined) query.model_q = modelSearch
  if (state.panel !== undefined) query.panel = state.panel
  if (state.panel === 'discovery') {
    const discoverySearch = normalizeCollectionSearch(state.discoverySearch)
    if (discoverySearch !== undefined) query.discovery_q = discoverySearch
    if (state.discoveryFilter === 'all') query.discovery_filter = 'all'
  }
  return query
}

export function isCanonicalImportRouteQuery(
  query: LocationQuery,
  state: ImportRouteState,
): boolean {
  return isCanonicalRouteQuery(query, serializeImportRouteQuery(state))
}
