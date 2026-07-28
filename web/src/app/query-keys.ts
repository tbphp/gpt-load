import type { RequestLogFilters } from '@/app/resources/request-logs'
import type { UsageFilters } from '@/app/resources/usage'

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
    list: (filters: RequestLogFilters) => ['control', 'logs', 'list', filters] as const,
  },
  usage: {
    report: (filters: UsageFilters) => {
      const normalized: UsageFilters = { range: filters.range }
      if (filters.group_id !== undefined) normalized.group_id = filters.group_id
      if (filters.model !== undefined) normalized.model = filters.model
      return ['control', 'usage', 'report', normalized] as const
    },
  },
  accessKeys: {
    list: () => ['control', 'access-keys', 'list'] as const,
    options: () => ['control', 'access-keys', 'options'] as const,
  },
  settings: (locale: string) => ['control', 'settings', locale] as const,
  systemInfo: () => ['control', 'system-info'] as const,
  modelPrices: () => ['control', 'model-prices'] as const,
}
