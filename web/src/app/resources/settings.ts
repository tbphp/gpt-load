import { queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import type { ProxyMutation, ProxyViewDto } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
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
  'retry_count',
  'blacklist_threshold',
  'header_rules',
  'cors',
  'response_header_rules',
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
  | 'retry_count'
  | 'blacklist_threshold'
  | 'header_rules'
  | 'cors'
  | 'response_header_rules'
  | 'inject_usage_options'
  | 'affinity_enabled'
  | 'affinity_capacity'
  | 'request_log_retention_days'
  | 'models_dev_auto_sync_enabled'
>
export type PolicyCountSettingKey = 'retry_count' | 'blacklist_threshold'

export interface CORSConfigDto {
  enabled: boolean
  allowed_origins: string[]
  allowed_methods: string[]
  allowed_headers: string[]
  exposed_headers: string[]
  allow_credentials: boolean
  max_age: number
}

export interface SettingsValues {
  first_byte_timeout: number
  request_timeout: number
  stream_idle_timeout: number
  retry_count: number
  blacklist_threshold: number
  header_rules: HeaderRulesDto
  cors: CORSConfigDto
  response_header_rules: HeaderRulesDto
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
  retry_count: number | null
  blacklist_threshold: number | null
  header_rules: HeaderRulesDto | null
  cors: CORSConfigDto | null
  response_header_rules: HeaderRulesDto | null
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
}

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

function projectCORSConfig(value: unknown): CORSConfigDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'enabled',
    'allowed_origins',
    'allowed_methods',
    'allowed_headers',
    'exposed_headers',
    'allow_credentials',
    'max_age',
  ])
  const projectList = (input: unknown): string[] =>
    projectArray(input, (item) => {
      const projected = projectString(item)
      if (projected !== projected.trim()) invalidResponse()
      return projected
    })
  return {
    enabled: projectBoolean(record.enabled),
    allowed_origins: projectList(record.allowed_origins),
    allowed_methods: projectList(record.allowed_methods),
    allowed_headers: projectList(record.allowed_headers),
    exposed_headers: projectList(record.exposed_headers),
    allow_credentials: projectBoolean(record.allow_credentials),
    max_age: projectSafeInteger(record.max_age, { minimum: 0 }),
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
      retry_count: projectSafeInteger(values.retry_count, { minimum: 0 }),
      blacklist_threshold: projectSafeInteger(values.blacklist_threshold, { minimum: 0 }),
      header_rules: projectHeaderRules(values.header_rules),
      cors: projectCORSConfig(values.cors),
      response_header_rules: projectHeaderRules(values.response_header_rules),
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

export async function getSettings(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<SettingsResource> {
  return { settings: projectSettings(await client.request<unknown>('/api/settings', { signal })) }
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
  signal?: AbortSignal,
): Promise<SettingsResource> {
  return {
    settings: projectSettings(
      await client.request<unknown>('/api/settings', {
        method: 'PUT',
        json: { settings: patch },
        signal,
      }),
    ),
  }
}
