import type { UsageFilters } from '@/app/resources/usage'
import { defaultTimeRange, isTimeRange } from '@/lib/time'

import { normalizeMonitorText } from './filter-validation'

export interface UsageFilterDraft {
  range: UsageFilters['range']
  distribution: NonNullable<UsageFilters['distribution']>
  distribution_metric: NonNullable<UsageFilters['distribution_metric']>
  group_id: string
  channel_id: string
  credential_id: string
  upstream_model: string
}

export type UsageFilterErrors = Partial<
  Record<Exclude<keyof UsageFilterDraft, 'range' | 'distribution' | 'distribution_metric'>, string>
>

const emptyDraft = (): UsageFilterDraft => ({
  range: defaultTimeRange,
  distribution: 'group',
  distribution_metric: 'requests',
  group_id: '',
  channel_id: '',
  credential_id: '',
  upstream_model: '',
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

export function normalizeUsageChannelID(raw: unknown): string | undefined {
  if (typeof raw !== 'string' || !/^[a-z][a-z0-9_]{0,99}$/u.test(raw)) return undefined
  return raw
}

export function parseAppliedUsageFilters(query: Record<string, unknown>): UsageFilters {
  const filters: UsageFilters = { range: normalizeUsageRange(query.range) }
  const groupID = normalizeUsageGroupID(query.group_id)
  const channelID = normalizeUsageChannelID(query.channel_id)
  const credentialID = normalizeUsageGroupID(query.credential_id)
  const upstreamModel = normalizeUsageModel(query.upstream_model ?? query.model)
  if (groupID !== undefined) filters.group_id = groupID
  if (channelID !== undefined) filters.channel_id = channelID
  if (credentialID !== undefined) filters.credential_id = credentialID
  if (upstreamModel !== undefined) filters.upstream_model = upstreamModel
  if (query.distribution === 'model') filters.distribution = 'model'
  if (query.distribution_metric === 'cost') filters.distribution_metric = 'cost'
  return filters
}

export function createUsageFilterDraft(filters: UsageFilters): UsageFilterDraft {
  return {
    ...emptyDraft(),
    range: filters.range,
    distribution: filters.distribution ?? 'group',
    distribution_metric: filters.distribution_metric ?? 'requests',
    group_id: filters.group_id === undefined ? '' : String(filters.group_id),
    channel_id: filters.channel_id ?? '',
    credential_id: filters.credential_id === undefined ? '' : String(filters.credential_id),
    upstream_model: filters.upstream_model ?? '',
  }
}

export function applyUsageFilterDraft(draft: UsageFilterDraft): UsageFilters {
  const filters: UsageFilters = {
    range: normalizeUsageRange(draft.range),
    distribution: draft.distribution,
    distribution_metric: draft.distribution_metric,
  }
  const groupID = normalizeUsageGroupID(draft.group_id)
  const channelID = normalizeUsageChannelID(draft.channel_id)
  const credentialID = normalizeUsageGroupID(draft.credential_id)
  const upstreamModel = normalizeUsageModel(draft.upstream_model)
  if (groupID !== undefined) filters.group_id = groupID
  if (channelID !== undefined) filters.channel_id = channelID
  if (credentialID !== undefined) filters.credential_id = credentialID
  if (upstreamModel !== undefined) filters.upstream_model = upstreamModel
  return filters
}

export function validateUsageFilterDraft(draft: UsageFilterDraft): UsageFilterErrors {
  const errors: UsageFilterErrors = {}
  if (draft.group_id && normalizeUsageGroupID(draft.group_id) === undefined) {
    errors.group_id = 'monitor.usage.errors.positiveId'
  }
  if (draft.channel_id && normalizeUsageChannelID(draft.channel_id) === undefined) {
    errors.channel_id = 'monitor.usage.errors.channelId'
  }
  if (draft.credential_id && normalizeUsageGroupID(draft.credential_id) === undefined) {
    errors.credential_id = 'monitor.usage.errors.credentialId'
  }
  if (draft.upstream_model && normalizeUsageModel(draft.upstream_model) === undefined) {
    errors.upstream_model = 'monitor.usage.errors.model'
  }
  return errors
}
