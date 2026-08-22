import { queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient, ApiClientWithResponse } from '@/api/client'
import type { ProxyMutation, ProxyViewDto } from '@/api/control/types'
import { ApiError, InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import type { HeaderRulesDto } from './groups'
import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'
import { projectProxyView } from './proxy'

export const runtimeSettingKeys = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'header_rules',
  'inject_usage_options',
  'affinity_enabled',
  'affinity_ttl',
  'affinity_capacity',
  'validation_interval',
  'request_log_retention_days',
  'models_dev_auto_sync_enabled',
] as const

export type RuntimeSettingKey = (typeof runtimeSettingKeys)[number]
export type TimeoutSettingKey = Exclude<
  RuntimeSettingKey,
  | 'header_rules'
  | 'inject_usage_options'
  | 'affinity_enabled'
  | 'affinity_capacity'
  | 'request_log_retention_days'
  | 'models_dev_auto_sync_enabled'
>

export interface SettingsValues {
  first_byte_timeout: number
  request_timeout: number
  stream_idle_timeout: number
  header_rules: HeaderRulesDto
  inject_usage_options: boolean
  affinity_enabled: boolean
  affinity_ttl: number
  affinity_capacity: number
  validation_interval: number
  request_log_retention_days: number
  models_dev_auto_sync_enabled: boolean
  proxy_config: ProxyViewDto
}

export interface SettingsDto {
  values: SettingsValues
  overrides: RuntimeSettingKey[]
  read_only: RuntimeSettingKey[]
}

export type SettingsPatch = Partial<{
  first_byte_timeout: number | null
  request_timeout: number | null
  stream_idle_timeout: number | null
  header_rules: HeaderRulesDto | null
  inject_usage_options: boolean | null
  affinity_enabled: boolean | null
  affinity_ttl: number | null
  affinity_capacity: number | null
  validation_interval: number | null
  request_log_retention_days: number | null
  models_dev_auto_sync_enabled: boolean | null
  proxy_config: ProxyMutation
}>

export interface SettingsResource {
  settings: SettingsDto
  settings_etag: string
}

const strongSettingsETag = /^"(?<token>sha256-[0-9a-f]{64})"$/
const strongSettingsETagToken = /^sha256-[0-9a-f]{64}$/
const settingsFields = ['values', 'overrides', 'read_only'] as const
const settingsValueFields = [...runtimeSettingKeys, 'proxy_config'] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectHeaderRules(value: unknown): HeaderRulesDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['set', 'remove'])
  const setRecord = projectRecord(record.set)
  const set: Record<string, string> = {}
  for (const [name, headerValue] of Object.entries(setRecord)) {
    if (name.trim().length === 0 || name !== name.trim()) invalidResponse()
    set[name] = projectString(headerValue, { allowEmpty: true })
  }
  return {
    set,
    remove: projectArray(record.remove, (name) => {
      const projected = projectString(name)
      if (projected.trim().length === 0 || projected !== projected.trim()) invalidResponse()
      return projected
    }),
  }
}

export function projectSettings(value: unknown): SettingsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, settingsFields)
  const values = projectRecord(record.values)
  assertNoSecretLikeFields(values, settingsValueFields)
  const overrides = projectArray(record.overrides, (key) => {
    const projected = projectString(key)
    if (!runtimeSettingKeys.includes(projected as RuntimeSettingKey)) invalidResponse()
    return projected as RuntimeSettingKey
  })
  if (new Set(overrides).size !== overrides.length) invalidResponse()
  const readOnly =
    record.read_only === undefined
      ? []
      : projectArray(record.read_only, (key) => {
          const projected = projectString(key)
          if (!runtimeSettingKeys.includes(projected as RuntimeSettingKey)) invalidResponse()
          return projected as RuntimeSettingKey
        })
  if (new Set(readOnly).size !== readOnly.length) invalidResponse()

  return {
    values: {
      first_byte_timeout: projectSafeInteger(values.first_byte_timeout, { minimum: 1 }),
      request_timeout: projectSafeInteger(values.request_timeout, { minimum: 1 }),
      stream_idle_timeout: projectSafeInteger(values.stream_idle_timeout, { minimum: 1 }),
      header_rules: projectHeaderRules(values.header_rules),
      inject_usage_options: projectBoolean(values.inject_usage_options),
      affinity_enabled: projectBoolean(values.affinity_enabled),
      affinity_ttl: projectSafeInteger(values.affinity_ttl, { minimum: 1 }),
      affinity_capacity: projectSafeInteger(values.affinity_capacity, {
        minimum: 1,
        maximum: 1_000_000,
      }),
      validation_interval: projectSafeInteger(values.validation_interval, { minimum: 1 }),
      request_log_retention_days: projectSafeInteger(values.request_log_retention_days, {
        minimum: 1,
        maximum: 365,
      }),
      models_dev_auto_sync_enabled: projectBoolean(values.models_dev_auto_sync_enabled),
      proxy_config: projectProxyView(values.proxy_config),
    },
    overrides,
    read_only: readOnly,
  }
}

export function settingsQueryIdentity(locale: string) {
  return controlQueryKeys.settings(locale)
}

export function settingsResourceFromToken(settings: SettingsDto, token: string): SettingsResource {
  if (!strongSettingsETagToken.test(token)) invalidResponse()
  return {
    settings,
    settings_etag: token,
  }
}

export function settingsResourceFromResponse(
  settings: SettingsDto,
  headers: Headers,
): SettingsResource {
  const header = headers.get('ETag')
  const match = header?.match(strongSettingsETag)
  const token = match?.groups?.token
  if (!token) invalidResponse()
  return settingsResourceFromToken(settings, token)
}

function clientWithResponse(client: ApiClient): ApiClientWithResponse {
  if (!client.requestWithResponse) invalidResponse()
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

export function settingsQueryOptions(client: ApiClient, locale: MaybeRefOrGetter<string>) {
  return queryOptions({
    queryKey: computed(() => settingsQueryIdentity(toValue(locale))),
    queryFn: ({ signal }) => getSettings(client, signal),
    gcTime: 0,
  })
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
    typeof error.data !== 'object' ||
    error.data === null ||
    Array.isArray(error.data)
  ) {
    return undefined
  }
  const data = error.data as Record<string, unknown>
  if (typeof data.settings_etag !== 'string') return undefined
  return settingsResourceFromToken(projectSettings(data.settings), data.settings_etag)
}
