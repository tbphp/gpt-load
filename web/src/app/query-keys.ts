import type { RequestLogFilters } from '@/app/resources/request-logs'
import type { HomeRange } from '@/app/resources/home'
import type { UsageFilters } from '@/app/resources/usage'
import type { GroupCollectionFilters, GroupKeyCollectionFilters } from '@/api/control/types'

export function normalizeGroupCollectionFilters(
  filters: GroupCollectionFilters,
): GroupCollectionFilters {
  const normalized: GroupCollectionFilters = {
    sort: filters.sort,
    page: filters.page,
    page_size: 20,
  }
  const query = filters.q?.trim()
  if (query) normalized.q = query
  if (filters.status !== undefined) normalized.status = filters.status
  if (filters.protocol !== undefined) normalized.protocol = filters.protocol
  return normalized
}

export function normalizeGroupKeyCollectionFilters(
  filters: GroupKeyCollectionFilters,
): GroupKeyCollectionFilters {
  const normalized: GroupKeyCollectionFilters = {
    page: filters.page,
    page_size: filters.page_size,
  }
  const query = filters.q?.trim()
  if (query) normalized.q = query
  if (filters.status !== undefined) normalized.status = filters.status
  return normalized
}

function normalizeLogFilters(filters: RequestLogFilters): RequestLogFilters {
  const result: RequestLogFilters = {}
  for (const field of [
    'from_ms',
    'to_ms',
    'group_id',
    'model',
    'access_key_id',
    'status',
    'request_id',
  ] as const) {
    const value = filters[field]
    if (value !== undefined) Object.assign(result, { [field]: value })
  }
  return result
}

function normalizeUsageFilters(filters: UsageFilters): UsageFilters {
  const result: UsageFilters = {
    range: filters.range,
    breakdown_order: filters.breakdown_order ?? 'requests',
  }
  if (filters.group_id !== undefined) result.group_id = filters.group_id
  if (filters.model !== undefined) result.model = filters.model
  return result
}

export const controlQueryKeys = {
  all: ['control'] as const,
  groups: {
    all: ['control', 'groups'] as const,
    collectionAll: ['control', 'groups', 'collection'] as const,
    collection: (filters: GroupCollectionFilters) =>
      ['control', 'groups', 'collection', normalizeGroupCollectionFilters(filters)] as const,
    options: () => ['control', 'groups', 'options'] as const,
    summaries: () => ['control', 'groups', 'summary'] as const,
    summary: (id: number) => ['control', 'groups', 'summary', id] as const,
    settingsAll: () => ['control', 'groups', 'settings'] as const,
    settings: (id: number) => ['control', 'groups', 'settings', id] as const,
    modelsAll: () => ['control', 'groups', 'models'] as const,
    models: (id: number) => ['control', 'groups', 'models', id] as const,
    keysAll: (id: number) => ['control', 'groups', 'keys', id] as const,
    keys: (id: number, filters: GroupKeyCollectionFilters) =>
      [
        'control',
        'groups',
        'keys',
        id,
        'collection',
        normalizeGroupKeyCollectionFilters(filters),
      ] as const,
    // Temporary compile bridge for the pre-ledger detail surface. These keys do
    // not represent a supported wire contract and will be removed with that UI.
    details: () => ['control', 'groups', 'legacy-detail'] as const,
    detail: (id: number) => ['control', 'groups', 'legacy-detail', id] as const,
    keyLists: () => ['control', 'groups', 'legacy-keys'] as const,
    legacyKeys: (id: number) => ['control', 'groups', 'legacy-keys', id] as const,
  },
  health: () => ['control', 'health'] as const,
  logs: {
    all: ['control', 'logs'] as const,
    list: (filters: RequestLogFilters) =>
      ['control', 'logs', 'list', normalizeLogFilters(filters)] as const,
  },
  usage: {
    all: ['control', 'usage'] as const,
    report: (filters: UsageFilters) =>
      ['control', 'usage', 'report', normalizeUsageFilters(filters)] as const,
  },
  home: {
    all: ['control', 'home'] as const,
    base: () => ['control', 'home', 'base'] as const,
    statisticsAll: () => ['control', 'home', 'statistics'] as const,
    statistics: (range: HomeRange) => ['control', 'home', 'statistics', range] as const,
  },
  accessKeys: {
    all: ['control', 'access-keys'] as const,
    list: () => ['control', 'access-keys', 'list'] as const,
    options: () => ['control', 'access-keys', 'options'] as const,
  },
  settingsAll: ['control', 'settings'] as const,
  settings: (locale: string) => ['control', 'settings', locale] as const,
  systemInfo: () => ['control', 'system-info'] as const,
  modelPrices: () => ['control', 'model-prices'] as const,
}
