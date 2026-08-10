import type { LocationQueryRaw } from 'vue-router'

import { enabledDataProtocols } from '@/api/control/protocols'
import type { UsageFilters } from '@/app/resources/usage'
import type { RequestLogFilters } from '@/app/resources/request-logs'
import {
  routeInspectFeatures,
  routeInspectOperations,
  type RouteInspectFeature,
  type RouteInspectOperation,
} from '@/app/resources/route-inspection'
import { defaultTimeRange } from '@/lib/time'

import { parseAppliedLogFilters, serializeAppliedLogFilters } from './log-filters'
import {
  normalizeUsageGroupID,
  normalizeUsageChannelID,
  normalizeUsageModel,
  parseAppliedUsageFilters,
} from './usage-filters'
import { normalizeMonitorText } from './filter-validation'

export type MonitorTab = 'health' | 'logs' | 'inspector' | 'usage'
export interface HealthMonitorState {
  groupsExpanded: boolean
}

export interface UsageBreakdownIdentity {
  groupID: number
  channelID: string | null
  credentialID: number | null
  model: string
}

export type UsageTrendMetric = 'requests' | 'tokens' | 'cost'

export interface UsageMonitorState {
  filtersOpen: boolean
  expandedBreakdowns: UsageBreakdownIdentity[]
  seriesExpanded: boolean
  metric: UsageTrendMetric
}

export interface LogsMonitorState {
  filtersOpen: boolean
  cursorHistory: string[]
  selectedRequestID?: string
}

export interface InspectorMonitorState {
  protocol?: string
  operation?: RouteInspectOperation
  requiredFeatures: RouteInspectFeature[]
  externalModel?: string
  accessKeyID?: string
  run: boolean
  expandedGroupIDs: number[]
}

const requestIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const logCursorPattern = /^[A-Za-z0-9_-]{1,512}$/u

export function normalizeMonitorTab(raw: unknown): MonitorTab {
  return raw === 'logs' || raw === 'inspector' || raw === 'usage' || raw === 'health'
    ? raw
    : 'health'
}

export function normalizeMonitorQuery(query: Record<string, unknown>): LocationQueryRaw {
  const tab = normalizeMonitorTab(query.tab)
  if (tab === 'health') return healthMonitorQuery(parseHealthMonitorState(query))
  if (tab === 'inspector') return inspectorMonitorQuery(parseInspectorMonitorState(query))
  if (tab === 'usage') {
    return usageMonitorQuery(parseAppliedUsageFilters(query), parseUsageMonitorState(query))
  }
  return logsMonitorQuery(parseAppliedLogFilters(query), parseLogsMonitorState(query))
}

const accessKeyForbiddenLogFilters: readonly (keyof RequestLogFilters)[] = [
  'group_id',
  'channel_id',
  'credential_id',
  'upstream_model',
  'access_key_id',
  'attempt_status_code',
  'failure_category',
  'error_code',
  'retry_state',
  'retry_count_min',
  'retry_count_max',
]

export function scopeAccessKeyUsageFilters(filters: UsageFilters): UsageFilters {
  const scoped = { ...filters }
  delete scoped.group_id
  delete scoped.channel_id
  delete scoped.credential_id
  return scoped
}

export function scopeAccessKeyLogFilters(filters: RequestLogFilters): RequestLogFilters {
  const scoped = { ...filters }
  for (const field of accessKeyForbiddenLogFilters) delete scoped[field]
  return scoped
}

export function normalizeAccessKeyMonitorQuery(query: Record<string, unknown>): LocationQueryRaw {
  const tab = normalizeMonitorTab(query.tab)
  if (tab === 'logs') {
    return logsMonitorQuery(
      scopeAccessKeyLogFilters(parseAppliedLogFilters(query)),
      parseLogsMonitorState(query),
    )
  }
  const state = parseUsageMonitorState(query)
  return usageMonitorQuery(scopeAccessKeyUsageFilters(parseAppliedUsageFilters(query)), {
    ...state,
    expandedBreakdowns: state.expandedBreakdowns.filter(
      ({ groupID, channelID, credentialID }) =>
        groupID === 0 && channelID === null && credentialID === null,
    ),
  })
}

export function parseHealthMonitorState(query: Record<string, unknown>): HealthMonitorState {
  return { groupsExpanded: query.groups === 'expanded' }
}

export function healthMonitorQuery(state: HealthMonitorState): LocationQueryRaw {
  return state.groupsExpanded ? { tab: 'health', groups: 'expanded' } : { tab: 'health' }
}

export function usageMonitorQuery(
  filters: UsageFilters = { range: defaultTimeRange },
  state: UsageMonitorState = {
    filtersOpen: false,
    expandedBreakdowns: [],
    seriesExpanded: false,
    metric: 'requests',
  },
): LocationQueryRaw {
  const normalized: LocationQueryRaw = {
    tab: 'usage',
    range: filters.range,
    metric: state.metric,
  }
  const groupID = normalizeUsageGroupID(filters.group_id)
  const channelID = normalizeUsageChannelID(filters.channel_id)
  const credentialID = normalizeUsageGroupID(filters.credential_id)
  const upstreamModel = normalizeUsageModel(filters.upstream_model)
  if (groupID !== undefined) normalized.group_id = String(groupID)
  if (channelID !== undefined) normalized.channel_id = channelID
  if (credentialID !== undefined) normalized.credential_id = String(credentialID)
  if (upstreamModel !== undefined) normalized.upstream_model = upstreamModel
  if (filters.breakdown_order === 'cost') normalized.breakdown_order = 'cost'
  if (state.filtersOpen) normalized.panel = 'filters'
  const expandedBreakdowns = serializeUsageBreakdowns(state.expandedBreakdowns)
  if (expandedBreakdowns !== undefined) normalized.expanded_breakdowns = expandedBreakdowns
  if (state.seriesExpanded) normalized.series = 'expanded'
  return normalized
}

export function parseUsageMonitorState(query: Record<string, unknown>): UsageMonitorState {
  return {
    filtersOpen: query.panel === 'filters',
    expandedBreakdowns: parseUsageBreakdowns(query.expanded_breakdowns),
    seriesExpanded: query.series === 'expanded',
    metric: normalizeUsageTrendMetric(query.metric),
  }
}

function normalizeUsageTrendMetric(raw: unknown): UsageTrendMetric {
  return raw === 'tokens' || raw === 'cost' ? raw : 'requests'
}

export function usageBreakdownIdentity(
  groupID: number,
  channelID: string | null,
  credentialID: number | null,
  model: string,
): UsageBreakdownIdentity {
  return { groupID, channelID, credentialID, model }
}

export function sameUsageBreakdownIdentity(
  left: UsageBreakdownIdentity,
  right: UsageBreakdownIdentity,
): boolean {
  return (
    left.groupID === right.groupID &&
    left.channelID === right.channelID &&
    left.credentialID === right.credentialID &&
    left.model === right.model
  )
}

export function sameMonitorQuery(left: LocationQueryRaw, right: LocationQueryRaw): boolean {
  const leftKeys = Object.keys(left)
  const rightKeys = Object.keys(right)
  if (leftKeys.length !== rightKeys.length) return false

  return leftKeys.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(right, key) &&
      typeof left[key] === 'string' &&
      left[key] === right[key],
  )
}

export function parseInspectorMonitorState(query: Record<string, unknown>): InspectorMonitorState {
  const protocol = scalarEnum(query.protocol, enabledDataProtocols)
  const operation = scalarEnum(query.operation, routeInspectOperations)
  const requiredFeatures = scalarEnumList(query.features, routeInspectFeatures)
  const externalModel = scalarText(query.external_model)
  const accessKeyID = scalarPositiveID(query.access_key_id)

  return {
    protocol,
    operation,
    requiredFeatures,
    externalModel,
    accessKeyID,
    run:
      query.run === '1' &&
      protocol !== undefined &&
      operation !== undefined &&
      accessKeyID !== undefined,
    expandedGroupIDs: parsePositiveIDList(query.expanded_groups),
  }
}

export function inspectorMonitorQuery(state: InspectorMonitorState): LocationQueryRaw {
  const normalized: LocationQueryRaw = { tab: 'inspector' }

  if (state.protocol !== undefined) normalized.protocol = state.protocol
  if (state.operation !== undefined) normalized.operation = state.operation
  if (state.requiredFeatures.length > 0) normalized.features = state.requiredFeatures.join(',')
  if (state.externalModel !== undefined) normalized.external_model = state.externalModel
  if (state.accessKeyID !== undefined) normalized.access_key_id = state.accessKeyID
  if (
    state.run &&
    state.protocol !== undefined &&
    state.operation !== undefined &&
    state.accessKeyID !== undefined
  ) {
    normalized.run = '1'
  }
  const expandedGroups = serializePositiveIDList(state.expandedGroupIDs)
  if (expandedGroups !== undefined) normalized.expanded_groups = expandedGroups
  return normalized
}

export function parseLogsMonitorState(query: Record<string, unknown>): LogsMonitorState {
  return {
    filtersOpen: query.panel === 'filters',
    cursorHistory: parseLogCursorHistory(query.log_cursors),
    selectedRequestID: parseSelectedRequestID(query),
  }
}

export function logsMonitorQuery(
  filters: ReturnType<typeof parseAppliedLogFilters>,
  state: LogsMonitorState = { filtersOpen: false, cursorHistory: [] },
): LocationQueryRaw {
  const normalized = serializeAppliedLogFilters(filters)
  if (state.filtersOpen) normalized.panel = 'filters'
  const cursorHistory = serializeLogCursorHistory(state.cursorHistory)
  if (cursorHistory !== undefined) normalized.log_cursors = cursorHistory
  if (state.selectedRequestID !== undefined) {
    normalized.selected_request_id = state.selectedRequestID
  }
  return normalized
}

export function parseSelectedRequestID(query: Record<string, unknown>): string | undefined {
  return scalarUUIDv4(query.selected_request_id)
}

function scalarText(raw: unknown): string | undefined {
  return normalizeMonitorText(raw)
}

function scalarPositiveID(raw: unknown): string | undefined {
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? String(value) : undefined
}

function scalarEnum<T extends string>(raw: unknown, values: readonly T[]): T | undefined {
  return typeof raw === 'string' && values.includes(raw as T) ? (raw as T) : undefined
}

function scalarEnumList<T extends string>(raw: unknown, values: readonly T[]): T[] {
  if (raw === undefined) return []
  if (typeof raw !== 'string' || raw === '') return []
  const entries = raw.split(',')
  if (
    entries.some((entry) => !values.includes(entry as T)) ||
    new Set(entries).size !== entries.length
  ) {
    return []
  }
  return entries.sort() as T[]
}

function scalarUUIDv4(raw: unknown): string | undefined {
  return typeof raw === 'string' && requestIDPattern.test(raw) ? raw : undefined
}

function parsePositiveIDList(raw: unknown): number[] {
  if (typeof raw !== 'string' || raw === '') return []
  const values = raw.split(',').map(Number)
  if (
    values.some((value) => !Number.isSafeInteger(value) || value <= 0) ||
    new Set(values).size !== values.length
  ) {
    return []
  }
  return [...values].sort((left, right) => left - right)
}

function serializePositiveIDList(values: readonly number[]): string | undefined {
  const normalized = [...new Set(values)]
    .filter((value) => Number.isSafeInteger(value) && value > 0)
    .sort((left, right) => left - right)
  return normalized.length > 0 ? normalized.join(',') : undefined
}

function parseUsageBreakdowns(raw: unknown): UsageBreakdownIdentity[] {
  if (typeof raw !== 'string' || raw.length > 10_000) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed) || parsed.length > 100) return []
    const identities: UsageBreakdownIdentity[] = []
    const seen = new Set<string>()
    for (const item of parsed) {
      if (!Array.isArray(item) || item.length !== 4) return []
      const [groupID, channelID, credentialID, model] = item
      if (
        !Number.isSafeInteger(groupID) ||
        Number(groupID) < 0 ||
        (channelID !== null && normalizeUsageChannelID(channelID) === undefined) ||
        (credentialID !== null &&
          (!Number.isSafeInteger(credentialID) || Number(credentialID) <= 0)) ||
        typeof model !== 'string' ||
        Array.from(model).length > 200 ||
        /[\u0000-\u001f\u007f]/u.test(model)
      ) {
        return []
      }
      const key = JSON.stringify([groupID, channelID, credentialID, model])
      if (seen.has(key)) return []
      seen.add(key)
      identities.push({
        groupID: Number(groupID),
        channelID: channelID === null ? null : String(channelID),
        credentialID: credentialID === null ? null : Number(credentialID),
        model,
      })
    }
    return identities.sort(
      (left, right) =>
        left.groupID - right.groupID ||
        (left.channelID ?? '').localeCompare(right.channelID ?? '') ||
        (left.credentialID ?? 0) - (right.credentialID ?? 0) ||
        left.model.localeCompare(right.model),
    )
  } catch {
    return []
  }
}

function serializeUsageBreakdowns(
  identities: readonly UsageBreakdownIdentity[],
): string | undefined {
  const normalized = [...identities]
    .filter(
      ({ groupID, channelID, credentialID, model }) =>
        Number.isSafeInteger(groupID) &&
        groupID >= 0 &&
        (channelID === null || normalizeUsageChannelID(channelID) !== undefined) &&
        (credentialID === null || (Number.isSafeInteger(credentialID) && credentialID > 0)) &&
        Array.from(model).length <= 200 &&
        !/[\u0000-\u001f\u007f]/u.test(model),
    )
    .sort(
      (left, right) =>
        left.groupID - right.groupID ||
        (left.channelID ?? '').localeCompare(right.channelID ?? '') ||
        (left.credentialID ?? 0) - (right.credentialID ?? 0) ||
        left.model.localeCompare(right.model),
    )
  return normalized.length > 0
    ? JSON.stringify(
        normalized.map(({ groupID, channelID, credentialID, model }) => [
          groupID,
          channelID,
          credentialID,
          model,
        ]),
      )
    : undefined
}

function parseLogCursorHistory(raw: unknown): string[] {
  if (typeof raw !== 'string' || raw.length > 30_000) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (
      !Array.isArray(parsed) ||
      parsed.length > 50 ||
      parsed.some((cursor) => typeof cursor !== 'string' || !logCursorPattern.test(cursor)) ||
      new Set(parsed).size !== parsed.length
    ) {
      return []
    }
    return parsed as string[]
  } catch {
    return []
  }
}

function serializeLogCursorHistory(cursors: readonly string[]): string | undefined {
  const normalized = cursors.filter((cursor) => logCursorPattern.test(cursor)).slice(-50)
  return normalized.length > 0 ? JSON.stringify(normalized) : undefined
}
