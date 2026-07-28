import type { UsageFilters } from '@/app/resources/usage'

export interface UsageFilterDraft {
  range: UsageFilters['range']
  group_id: string
  model: string
}

export type UsageFilterErrors = Partial<Record<Exclude<keyof UsageFilterDraft, 'range'>, string>>

const emptyDraft = (): UsageFilterDraft => ({ range: '24h', group_id: '', model: '' })

export function normalizeUsageRange(raw: unknown): UsageFilters['range'] {
  return raw === '30d' ? '30d' : '24h'
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
  if (typeof raw !== 'string' || raw === '' || raw.trim() !== raw) return undefined
  if (/\p{Cc}/u.test(raw) || new TextEncoder().encode(raw).byteLength > 255) {
    return undefined
  }
  return raw
}

export function parseAppliedUsageFilters(query: Record<string, unknown>): UsageFilters {
  const filters: UsageFilters = { range: normalizeUsageRange(query.range) }
  const groupID = normalizeUsageGroupID(query.group_id)
  const model = normalizeUsageModel(query.model)
  if (groupID !== undefined) filters.group_id = groupID
  if (model !== undefined) filters.model = model
  return filters
}

export function createUsageFilterDraft(filters: UsageFilters): UsageFilterDraft {
  return {
    ...emptyDraft(),
    range: filters.range,
    group_id: filters.group_id === undefined ? '' : String(filters.group_id),
    model: filters.model ?? '',
  }
}

export function resetUsageFilterDraft(): UsageFilterDraft {
  return emptyDraft()
}

export function applyUsageFilterDraft(draft: UsageFilterDraft): UsageFilters {
  const filters: UsageFilters = { range: normalizeUsageRange(draft.range) }
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
