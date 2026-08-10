import type { RequestLogFilters } from '@/app/resources/request-logs'
import type { HomeRange } from '@/app/resources/home'
import type { UsageFilters } from '@/app/resources/usage'
import type { ModelPriceFilters } from '@/app/resources/model-prices'
import type { ModelCollectionFilters } from '@/app/resources/models'
import { normalizeRequestLogFilters } from '@/app/resources/request-log-filters'
import type {
  AccessKeyCollectionFilters,
  CredentialCollectionFilters,
  GroupCollectionFilters,
} from '@/api/control/types'

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
  return normalized
}

export function normalizeCredentialCollectionFilters(
  filters: CredentialCollectionFilters,
): CredentialCollectionFilters {
  const normalized: CredentialCollectionFilters = {
    page: filters.page,
    page_size: filters.page_size,
  }
  const query = filters.q?.trim()
  if (query) normalized.q = query
  if (filters.status !== undefined) normalized.status = filters.status
  return normalized
}

export function normalizeAccessKeyCollectionFilters(
  filters: AccessKeyCollectionFilters,
): AccessKeyCollectionFilters {
  const normalized: AccessKeyCollectionFilters = {
    page: filters.page,
    page_size: 20,
  }
  const query = filters.q?.trim()
  if (query) normalized.q = query
  if (filters.status !== undefined) normalized.status = filters.status
  return normalized
}

function normalizeUsageFilters(filters: UsageFilters): UsageFilters {
  const result: UsageFilters = {
    range: filters.range,
    breakdown_order: filters.breakdown_order ?? 'requests',
  }
  if (filters.group_id !== undefined) result.group_id = filters.group_id
  if (filters.channel_id !== undefined) result.channel_id = filters.channel_id
  if (filters.credential_id !== undefined) result.credential_id = filters.credential_id
  if (filters.upstream_model !== undefined) result.upstream_model = filters.upstream_model
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
    credentialsAll: (id: number) => ['control', 'groups', 'credentials', id] as const,
    credentials: (id: number, filters: CredentialCollectionFilters) =>
      [
        'control',
        'groups',
        'credentials',
        id,
        'collection',
        normalizeCredentialCollectionFilters(filters),
      ] as const,
  },
  channels: {
    all: ['control', 'channels'] as const,
    list: (search: string) => ['control', 'channels', 'list', search] as const,
  },
  health: () => ['control', 'health'] as const,
  logs: {
    all: ['control', 'logs'] as const,
    list: (filters: RequestLogFilters) =>
      ['control', 'logs', 'list', normalizeRequestLogFilters(filters)] as const,
    details: () => ['control', 'logs', 'detail'] as const,
    detail: (requestID: string) => ['control', 'logs', 'detail', requestID] as const,
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
    collectionAll: ['control', 'access-keys', 'collection'] as const,
    collection: (filters: AccessKeyCollectionFilters) =>
      [
        'control',
        'access-keys',
        'collection',
        normalizeAccessKeyCollectionFilters(filters),
      ] as const,
    options: () => ['control', 'access-keys', 'options'] as const,
  },
  settingsAll: ['control', 'settings'] as const,
  settings: (locale: string) => ['control', 'settings', locale] as const,
  systemInfo: () => ['control', 'system-info'] as const,
  models: {
    all: ['control', 'models'] as const,
    collection: (filters: ModelCollectionFilters) =>
      ['control', 'models', 'collection', filters] as const,
    detail: (priceID: number) => ['control', 'models', 'detail', priceID] as const,
  },
  modelPrices: () => ['control', 'model-prices'] as const,
  modelPriceCollection: (filters: ModelPriceFilters) =>
    ['control', 'model-prices', 'collection', filters] as const,
}
