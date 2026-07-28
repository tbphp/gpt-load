import type { ApiClient, ApiClientWithResponse } from '@/api/client'
import { ApiError, InvalidResponseError } from '@/api/errors'
import {
  settingsResourceFromResponse,
  settingsResourceFromToken,
  type SettingsResource,
} from '@/app/resources/settings'

import type { HeaderRulesDto } from './groups'

export const runtimeSettingKeys = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'header_rules',
  'inject_usage_options',
  'request_log_retention_days',
] as const

export type RuntimeSettingKey = (typeof runtimeSettingKeys)[number]
export type TimeoutSettingKey = Exclude<
  RuntimeSettingKey,
  'header_rules' | 'inject_usage_options' | 'request_log_retention_days'
>

export interface SettingsValues {
  connect_timeout: number
  first_byte_timeout: number
  request_timeout: number
  stream_idle_timeout: number
  header_rules: HeaderRulesDto
  inject_usage_options: boolean
  request_log_retention_days: number
}

export interface SettingsDto {
  values: SettingsValues
  overrides: RuntimeSettingKey[]
}

export type SettingsPatch = Partial<{
  connect_timeout: number | null
  first_byte_timeout: number | null
  request_timeout: number | null
  stream_idle_timeout: number | null
  header_rules: HeaderRulesDto | null
  inject_usage_options: boolean | null
  request_log_retention_days: number | null
}>

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isPositiveSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function projectHeaderRules(value: unknown): HeaderRulesDto {
  if (!isRecord(value) || !isRecord(value.set) || !Array.isArray(value.remove)) {
    throw new InvalidResponseError()
  }
  const set: Record<string, string> = {}
  for (const [name, headerValue] of Object.entries(value.set)) {
    if (typeof headerValue !== 'string') throw new InvalidResponseError()
    set[name] = headerValue
  }
  if (!value.remove.every((name) => typeof name === 'string')) throw new InvalidResponseError()
  return { set, remove: [...value.remove] as string[] }
}

export function projectSettings(value: unknown): SettingsDto {
  if (!isRecord(value) || !isRecord(value.values) || !Array.isArray(value.overrides)) {
    throw new InvalidResponseError()
  }
  const values = value.values
  if (
    !isPositiveSafeInteger(values.connect_timeout) ||
    !isPositiveSafeInteger(values.first_byte_timeout) ||
    !isPositiveSafeInteger(values.request_timeout) ||
    !isPositiveSafeInteger(values.stream_idle_timeout) ||
    typeof values.inject_usage_options !== 'boolean' ||
    !Number.isSafeInteger(values.request_log_retention_days) ||
    (values.request_log_retention_days as number) < 1 ||
    (values.request_log_retention_days as number) > 365 ||
    !value.overrides.every((key) => typeof key === 'string')
  ) {
    throw new InvalidResponseError()
  }
  const allowlist = new Set<string>(runtimeSettingKeys)
  const overrides = [...new Set(value.overrides.filter((key) => allowlist.has(key)))]
  return {
    values: {
      connect_timeout: values.connect_timeout,
      first_byte_timeout: values.first_byte_timeout,
      request_timeout: values.request_timeout,
      stream_idle_timeout: values.stream_idle_timeout,
      header_rules: projectHeaderRules(values.header_rules),
      inject_usage_options: values.inject_usage_options,
      request_log_retention_days: values.request_log_retention_days as number,
    },
    overrides: overrides as RuntimeSettingKey[],
  }
}

function clientWithResponse(client: ApiClient): ApiClientWithResponse {
  if (!client.requestWithResponse) throw new InvalidResponseError()
  return client as ApiClientWithResponse
}

export async function getSettings(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<SettingsResource> {
  const response = await clientWithResponse(client).requestWithResponse<unknown>('/api/settings', {
    signal,
  })
  return settingsResourceFromResponse(projectSettings(response.data), response.headers)
}

export async function updateSettings(
  client: ApiClient,
  patch: SettingsPatch,
  settingsETag: string,
  signal?: AbortSignal,
): Promise<SettingsResource> {
  const response = await clientWithResponse(client).requestWithResponse<unknown>('/api/settings', {
    method: 'PUT',
    headers: { 'If-Match': `"${settingsETag}"` },
    json: { settings: patch },
    signal,
  })
  return settingsResourceFromResponse(projectSettings(response.data), response.headers)
}

export function projectSettingsConflict(error: unknown): SettingsResource | undefined {
  if (
    !(error instanceof ApiError) ||
    error.status !== 412 ||
    error.code !== 'SETTINGS_VERSION_CONFLICT' ||
    !isRecord(error.data) ||
    typeof error.data.settings_etag !== 'string'
  ) {
    return undefined
  }
  return settingsResourceFromToken(projectSettings(error.data.settings), error.data.settings_etag)
}
