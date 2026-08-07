import type { UsageFilters } from '@/app/resources/usage'
import { defaultTimeRange, isTimeRange } from '@/lib/time'

import { normalizeMonitorText } from './filter-validation'

export interface UsageFilterDraft {
  range: UsageFilters['range']
  breakdown_order: NonNullable<UsageFilters['breakdown_order']>
  group_id: string
  model: string
}

export type UsageFilterErrors = Partial<
  Record<Exclude<keyof UsageFilterDraft, 'range' | 'breakdown_order'>, string>
>

const emptyDraft = (): UsageFilterDraft => ({
  range: defaultTimeRange,
  breakdown_order: 'requests',
  group_id: '',
  model: '',
})

export function normalizeUsageRange(raw: unknown): UsageFilters['range'] {
  return isTimeRange(raw) ? raw : defaultTimeRange
}

export function normalizeUsageGroupID(raw: unknown): number | undefined {
  if (typeof raw === 'number') {
    return Number.isSafeInteger(raw) && raw > 0 ? raw : undefined
  }
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return undefined

  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function normalizeUsageModel(raw: unknown): string | undefined {
  return normalizeMonitorText(raw)
}

export function parseAppliedUsageFilters(query: Record<string, unknown>): UsageFilters {
  const filters: UsageFilters = { range: normalizeUsageRange(query.range) }
  const groupID = normalizeUsageGroupID(query.group_id)
  const model = normalizeUsageModel(query.model)
  if (groupID !== undefined) filters.group_id = groupID
  if (model !== undefined) filters.model = model
  if (query.breakdown_order === 'cost') filters.breakdown_order = 'cost'
  return filters
}

export function createUsageFilterDraft(filters: UsageFilters): UsageFilterDraft {
  return {
    ...emptyDraft(),
    range: filters.range,
    breakdown_order: filters.breakdown_order ?? 'requests',
    group_id: filters.group_id === undefined ? '' : String(filters.group_id),
    model: filters.model ?? '',
  }
}

export function resetUsageFilterDraft(): UsageFilterDraft {
  return emptyDraft()
}

export function applyUsageFilterDraft(draft: UsageFilterDraft): UsageFilters {
  const filters: UsageFilters = {
    range: normalizeUsageRange(draft.range),
    breakdown_order: draft.breakdown_order,
  }
  const groupID = normalizeUsageGroupID(draft.group_id)
  const model = normalizeUsageModel(draft.model)
  if (groupID !== undefined) filters.group_id = groupID
  if (model !== undefined) filters.model = model
  return filters
}

export function validateUsageFilterDraft(draft: UsageFilterDraft): UsageFilterErrors {
  const errors: UsageFilterErrors = {}
  if (draft.group_id && normalizeUsageGroupID(draft.group_id) === undefined) {
    errors.group_id = 'monitor.usage.errors.positiveId'
  }
  if (draft.model && normalizeUsageModel(draft.model) === undefined) {
    errors.model = 'monitor.usage.errors.model'
  }
  return errors
}
