import type { LocationQueryRaw } from 'vue-router'

import type { AccessProtocol } from '@/api/control/types'
import type { RequestLogStatus } from '@/api/control/request-logs'

export type MonitorTab = 'health' | 'logs' | 'inspector'

export const requestLogStatuses = [
  'success',
  'error',
  'incomplete',
  'canceled',
] as const satisfies readonly RequestLogStatus[]
const accessProtocols: AccessProtocol[] = ['openai', 'anthropic', 'gemini', 'openai-response']
const requestIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const rfc3339Pattern =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/

export function normalizeMonitorTab(raw: unknown): MonitorTab {
  return raw === 'logs' || raw === 'inspector' || raw === 'health' ? raw : 'health'
}

export function normalizeMonitorQuery(query: Record<string, unknown>): LocationQueryRaw {
  const tab = normalizeMonitorTab(query.tab)
  if (tab === 'health') return { tab }
  if (tab === 'inspector') return normalizeInspectorQuery(query, tab)
  return normalizeLogsQuery(query, tab)
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

function normalizeInspectorQuery(
  query: Record<string, unknown>,
  tab: MonitorTab,
): LocationQueryRaw {
  const normalized: LocationQueryRaw = { tab }
  const protocol = scalarEnum(query.protocol, accessProtocols)
  const externalModel = scalarText(query.external_model)
  const accessKeyID = scalarPositiveID(query.access_key_id)

  if (protocol !== undefined) normalized.protocol = protocol
  if (externalModel !== undefined) normalized.external_model = externalModel
  if (accessKeyID !== undefined) normalized.access_key_id = accessKeyID
  return normalized
}

function normalizeLogsQuery(query: Record<string, unknown>, tab: MonitorTab): LocationQueryRaw {
  const normalized: LocationQueryRaw = { tab }
  const from = scalarRFC3339(query.from)
  const to = scalarRFC3339(query.to)

  if (from === undefined || to === undefined || Date.parse(from) < Date.parse(to)) {
    if (from !== undefined) normalized.from = from
    if (to !== undefined) normalized.to = to
  }

  const groupID = scalarPositiveID(query.group_id)
  const model = scalarText(query.model)
  const accessKeyID = scalarPositiveID(query.access_key_id)
  const status = scalarEnum(query.status, requestLogStatuses)
  const requestID = scalarUUIDv4(query.request_id)

  if (groupID !== undefined) normalized.group_id = groupID
  if (model !== undefined) normalized.model = model
  if (accessKeyID !== undefined) normalized.access_key_id = accessKeyID
  if (status !== undefined) normalized.status = status
  if (requestID !== undefined) normalized.request_id = requestID
  return normalized
}

function scalarText(raw: unknown): string | undefined {
  if (typeof raw !== 'string' || raw.trim() !== raw || raw === '') return undefined
  return /[\u0000-\u001f\u007f]/.test(raw) ? undefined : raw
}

function scalarPositiveID(raw: unknown): string | undefined {
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? String(value) : undefined
}

function scalarEnum<T extends string>(raw: unknown, values: readonly T[]): T | undefined {
  return typeof raw === 'string' && values.includes(raw as T) ? (raw as T) : undefined
}

function scalarUUIDv4(raw: unknown): string | undefined {
  return typeof raw === 'string' && requestIDPattern.test(raw) ? raw : undefined
}

function scalarRFC3339(raw: unknown): string | undefined {
  if (typeof raw !== 'string') return undefined
  const match = raw.match(rfc3339Pattern)
  if (!match) return undefined

  const [, year, month, day, hour, minute, second] = match.map(Number)
  if (month < 1 || month > 12 || hour > 23 || minute > 59 || second > 59) return undefined
  const lastDay = new Date(Date.UTC(year, month, 0)).getUTCDate()
  if (day < 1 || day > lastDay || Number.isNaN(Date.parse(raw))) return undefined
  return raw
}
