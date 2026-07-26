import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

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
  revision: number
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
  if (!Number.isSafeInteger(value.revision) || (value.revision as number) < 0) {
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
    revision: value.revision as number,
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

export async function getSettings(client: ApiClient, signal?: AbortSignal): Promise<SettingsDto> {
  return projectSettings(await client.request<unknown>('/api/settings', { signal }))
}

export async function updateSettings(
  client: ApiClient,
  patch: SettingsPatch,
  signal?: AbortSignal,
): Promise<SettingsDto> {
  return projectSettings(
    await client.request<unknown>('/api/settings', {
      method: 'PUT',
      json: { settings: patch },
      signal,
    }),
  )
}
