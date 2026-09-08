import type { UsageFilters } from '@/app/resources/usage'
import {
  defaultTimeRange,
  isDateTimePreset,
  resolveDateTimePreset,
  type DateTimePreset,
} from '@/lib/time'

import { normalizeMonitorText } from './filter-validation'

export interface AppliedUsageFilters extends UsageFilters {
  preset?: DateTimePreset
}

export interface UsageFilterDraft {
  access_key_id: string
  group_id: string
  channel_id: string
  credential_id: string
  upstream_model: string
}

export type UsageFilterErrors = Partial<Record<keyof UsageFilterDraft, string>>

const emptyDraft = (): UsageFilterDraft => ({
  access_key_id: '',
  group_id: '',
  channel_id: '',
  credential_id: '',
  upstream_model: '',
})

export function defaultUsageFilters(
  preset: DateTimePreset = defaultTimeRange,
): AppliedUsageFilters {
  const now = Math.floor(Date.now() / 1000) * 1000
  const interval = resolveDateTimePreset(preset, now)
  return interval.to_ms > interval.from_ms
    ? { ...interval, preset }
    : { ...resolveDateTimePreset(defaultTimeRange, now), preset: defaultTimeRange }
}

function normalizeUsageTimestamp(raw: unknown): number | undefined {
  const value =
    typeof raw === 'number' ? raw : typeof raw === 'string' && /^\d+$/.test(raw) ? Number(raw) : NaN
  return Number.isSafeInteger(value) && value >= 0 ? value : undefined
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

export function parseAppliedUsageFilters(query: Record<string, unknown>): AppliedUsageFilters {
  const from = normalizeUsageTimestamp(query.from_ms)
  const to = normalizeUsageTimestamp(query.to_ms)
  const preset = query.preset ?? query.range
  const filters: AppliedUsageFilters =
    from !== undefined && to !== undefined && to > from
      ? { from_ms: from, to_ms: to, ...(isDateTimePreset(preset) ? { preset } : {}) }
      : defaultUsageFilters(isDateTimePreset(preset) ? preset : defaultTimeRange)
  const accessKeyID = normalizeUsageGroupID(query.access_key_id)
  const groupID = normalizeUsageGroupID(query.group_id)
  const channelID = normalizeUsageChannelID(query.channel_id)
  const credentialID = normalizeUsageGroupID(query.credential_id)
  const upstreamModel = normalizeUsageModel(query.upstream_model ?? query.model)
  if (accessKeyID !== undefined) filters.access_key_id = accessKeyID
  if (groupID !== undefined) filters.group_id = groupID
  if (channelID !== undefined) filters.channel_id = channelID
  if (credentialID !== undefined) filters.credential_id = credentialID
  if (upstreamModel !== undefined) filters.upstream_model = upstreamModel
  return filters
}

export function createUsageFilterDraft(filters: UsageFilters): UsageFilterDraft {
  return {
    ...emptyDraft(),
    access_key_id: filters.access_key_id === undefined ? '' : String(filters.access_key_id),
    group_id: filters.group_id === undefined ? '' : String(filters.group_id),
    channel_id: filters.channel_id ?? '',
    credential_id: filters.credential_id === undefined ? '' : String(filters.credential_id),
    upstream_model: filters.upstream_model ?? '',
  }
}

export function applyUsageFilterDraft(
  draft: UsageFilterDraft,
  current: AppliedUsageFilters,
): AppliedUsageFilters {
  const filters: AppliedUsageFilters = {
    from_ms: current.from_ms,
    to_ms: current.to_ms,
    preset: current.preset,
  }
  const accessKeyID = normalizeUsageGroupID(draft.access_key_id)
  const groupID = normalizeUsageGroupID(draft.group_id)
  const channelID = normalizeUsageChannelID(draft.channel_id)
  const credentialID = normalizeUsageGroupID(draft.credential_id)
  const upstreamModel = normalizeUsageModel(draft.upstream_model)
  if (accessKeyID !== undefined) filters.access_key_id = accessKeyID
  if (groupID !== undefined) filters.group_id = groupID
  if (channelID !== undefined) filters.channel_id = channelID
  if (credentialID !== undefined) filters.credential_id = credentialID
  if (upstreamModel !== undefined) filters.upstream_model = upstreamModel
  return filters
}

export function validateUsageFilterDraft(draft: UsageFilterDraft): UsageFilterErrors {
  const errors: UsageFilterErrors = {}
  if (draft.access_key_id && normalizeUsageGroupID(draft.access_key_id) === undefined)
    errors.access_key_id = 'monitor.usage.errors.positiveId'
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
