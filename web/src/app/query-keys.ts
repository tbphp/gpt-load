import type { RequestLogFilters } from '@/app/resources/request-logs'
import type { HomeRange } from '@/app/resources/home'
import type { UsageFilters } from '@/app/resources/usage'

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
    list: () => ['control', 'groups', 'list'] as const,
    details: () => ['control', 'groups', 'detail'] as const,
    detail: (id: number) => ['control', 'groups', 'detail', id] as const,
    keyLists: () => ['control', 'groups', 'keys'] as const,
    keys: (id: number) => ['control', 'groups', 'keys', id] as const,
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
