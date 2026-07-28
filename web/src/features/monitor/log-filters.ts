import type { LocationQueryRaw } from 'vue-router'

import type { RequestLogFilters, RequestLogStatus } from '@/app/resources/request-logs'

import { requestLogStatuses } from './monitor-route'

export interface LogFilterDraft {
  from: string
  to: string
  group_id: string
  model: string
  access_key_id: string
  status: string
  request_id: string
}

export type LogFilterErrors = Partial<Record<keyof LogFilterDraft, string>>

const requestIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

const emptyDraft = (): LogFilterDraft => ({
  from: '',
  to: '',
  group_id: '',
  model: '',
  access_key_id: '',
  status: '',
  request_id: '',
})

function toLocalDateTime(value: string | undefined): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (part: number) => String(part).padStart(2, '0')
  return [
    date.getFullYear(),
    '-',
    pad(date.getMonth() + 1),
    '-',
    pad(date.getDate()),
    'T',
    pad(date.getHours()),
    ':',
    pad(date.getMinutes()),
  ].join('')
}

export function createLogFilterDraft(filters: RequestLogFilters = {}): LogFilterDraft {
  return {
    ...emptyDraft(),
    from: toLocalDateTime(filters.from),
    to: toLocalDateTime(filters.to),
    group_id: filters.group_id === undefined ? '' : String(filters.group_id),
    model: filters.model ?? '',
    access_key_id: filters.access_key_id === undefined ? '' : String(filters.access_key_id),
    status: filters.status ?? '',
    request_id: filters.request_id ?? '',
  }
}

export function applyLogFilterDraft(draft: LogFilterDraft): RequestLogFilters {
  const filters: RequestLogFilters = {}
  if (draft.from) filters.from = new Date(draft.from).toISOString()
  if (draft.to) filters.to = new Date(draft.to).toISOString()
  if (draft.group_id) filters.group_id = Number(draft.group_id)
  if (draft.model) filters.model = draft.model
  if (draft.access_key_id) filters.access_key_id = Number(draft.access_key_id)
  if (draft.status) filters.status = draft.status as RequestLogStatus
  if (draft.request_id) filters.request_id = draft.request_id
  return filters
}

export function parseAppliedLogFilters(query: Record<string, unknown>): RequestLogFilters {
  const filters: RequestLogFilters = {}
  if (typeof query.from === 'string') filters.from = query.from
  if (typeof query.to === 'string') filters.to = query.to
  if (typeof query.group_id === 'string') filters.group_id = Number(query.group_id)
  if (typeof query.model === 'string') filters.model = query.model
  if (typeof query.access_key_id === 'string') filters.access_key_id = Number(query.access_key_id)
  if (typeof query.status === 'string') filters.status = query.status as RequestLogStatus
  if (typeof query.request_id === 'string') filters.request_id = query.request_id
  return filters
}

export function serializeAppliedLogFilters(filters: RequestLogFilters): LocationQueryRaw {
  const query: LocationQueryRaw = { tab: 'logs' }
  if (filters.from !== undefined) query.from = filters.from
  if (filters.to !== undefined) query.to = filters.to
  if (filters.group_id !== undefined) query.group_id = String(filters.group_id)
  if (filters.model !== undefined) query.model = filters.model
  if (filters.access_key_id !== undefined) query.access_key_id = String(filters.access_key_id)
  if (filters.status !== undefined) query.status = filters.status
  if (filters.request_id !== undefined) query.request_id = filters.request_id
  return query
}

function isPositiveID(value: string): boolean {
  if (!/^\d+$/.test(value)) return false
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed > 0
}

function parseLocalDateTime(value: string): Date | undefined {
  const match = value.match(
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?$/,
  )
  if (!match) return undefined
  const [, year, month, day, hour, minute, second = '0', milliseconds = '0'] = match
  const parts = [year, month, day, hour, minute, second, milliseconds.padEnd(3, '0')].map(Number)
  const [yearValue, monthValue, dayValue, hourValue, minuteValue, secondValue, msValue] = parts
  if (
    yearValue === undefined ||
    monthValue === undefined ||
    dayValue === undefined ||
    hourValue === undefined ||
    minuteValue === undefined ||
    secondValue === undefined ||
    msValue === undefined
  ) {
    return undefined
  }
  const date = new Date(
    yearValue,
    monthValue - 1,
    dayValue,
    hourValue,
    minuteValue,
    secondValue,
    msValue,
  )
  if (
    date.getFullYear() !== yearValue ||
    date.getMonth() !== monthValue - 1 ||
    date.getDate() !== dayValue ||
    date.getHours() !== hourValue ||
    date.getMinutes() !== minuteValue ||
    date.getSeconds() !== secondValue ||
    date.getMilliseconds() !== msValue
  ) {
    return undefined
  }
  return date
}

export function validateLogFilterDraft(draft: LogFilterDraft): LogFilterErrors {
  const errors: LogFilterErrors = {}
  const from = draft.from ? parseLocalDateTime(draft.from) : undefined
  const to = draft.to ? parseLocalDateTime(draft.to) : undefined
  if (draft.from && !from) errors.from = 'monitor.logs.errors.dateTime'
  if (draft.to && !to) errors.to = 'monitor.logs.errors.dateTime'
  if (from && to && from.getTime() >= to.getTime()) {
    errors.to = 'monitor.logs.errors.range'
  }
  if (draft.group_id && !isPositiveID(draft.group_id)) {
    errors.group_id = 'monitor.logs.errors.positiveId'
  }
  if (draft.access_key_id && !isPositiveID(draft.access_key_id)) {
    errors.access_key_id = 'monitor.logs.errors.positiveId'
  }
  if (
    draft.model &&
    (draft.model.trim() !== draft.model || /[\u0000-\u001f\u007f]/.test(draft.model))
  ) {
    errors.model = 'monitor.logs.errors.model'
  }
  if (draft.status && !requestLogStatuses.includes(draft.status as RequestLogStatus)) {
    errors.status = 'monitor.logs.errors.status'
  }
  if (draft.request_id && !requestIDPattern.test(draft.request_id)) {
    errors.request_id = 'monitor.logs.errors.requestId'
  }
  return errors
}
