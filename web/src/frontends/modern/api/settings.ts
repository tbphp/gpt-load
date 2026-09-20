import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import type { HeaderRules } from './group-detail'
import { boolean, integer, list, oneOf, record, text } from './response'
import { readAutoModel, readAutoEntry, type AutoModelConfig, type AutoEntry } from './auto-model'

export const settingsKey = ['modern', 'settings'] as const
export const settingNumbers = {
  first_byte_timeout: { min: 1, max: 9_223_372_036, unit: 'seconds' },
  request_timeout: { min: 1, max: 9_223_372_036, unit: 'seconds' },
  stream_idle_timeout: { min: 1, max: 9_223_372_036, unit: 'seconds' },
  retry_count: { min: 0, max: Number.MAX_SAFE_INTEGER, unit: 'times' },
  blacklist_threshold: { min: 0, max: Number.MAX_SAFE_INTEGER, unit: 'times' },
  affinity_ttl: { min: 1, max: 9_223_372_036, unit: 'seconds' },
  affinity_capacity: { min: 1, max: 1_000_000, unit: 'entries' },
  validation_interval: { min: 1, max: 9_223_372_036, unit: 'seconds' },
  request_log_retention_days: { min: 1, max: 365, unit: 'days' },
} as const
export type SettingNumber = keyof typeof settingNumbers
export const settingSwitches = [
  'affinity_enabled',
  'responses_websocket_enabled',
  'models_dev_auto_sync_enabled',
] as const
export type SettingSwitch = (typeof settingSwitches)[number]
export const routeStrategies = ['native_first', 'weighted_mix'] as const
export type RouteStrategy = (typeof routeStrategies)[number]
export interface CORSConfig {
  enabled: boolean
  allowed_origins: string[]
  allowed_methods: string[]
  allowed_headers: string[]
  exposed_headers: string[]
  allow_credentials: boolean
  max_age: number
}
export interface ProxyConfigView {
  configured_mode: 'inherit' | 'direct' | 'custom'
  effective_mode: 'direct' | 'environment' | 'custom'
  effective_source: 'credential' | 'group' | 'global' | 'environment' | 'default'
  display_url: string
  has_auth: boolean
}
export type SettingsValues = Record<SettingNumber, number> &
  Record<SettingSwitch, boolean> & {
    route_strategy: RouteStrategy
    header_rules: HeaderRules
    response_header_rules: HeaderRules
    cors: CORSConfig
    proxy_config: ProxyConfigView
    auto_model?: AutoModelConfig
  }
export type SettingKey = keyof SettingsValues
export const settingKeys: readonly SettingKey[] = [
  'route_strategy',
  ...settingSwitches,
  ...(Object.keys(settingNumbers) as SettingNumber[]),
  'header_rules',
  'response_header_rules',
  'cors',
  'proxy_config',
  'auto_model',
]
export interface SettingsData {
  autoModelTemplate?: AutoEntry
  decisionModels: string[]
  values: SettingsValues
  overrides: SettingKey[]
  readOnly: SettingKey[]
}
export type SettingsPatch = Partial<{
  [K in Exclude<SettingKey, 'proxy_config'>]: SettingsValues[K] | null
}> & { proxy_config?: { mode: 'direct' } | { mode: 'custom'; url: string } | null }

function readHeaders(value: unknown): HeaderRules {
  const row = record(value)
  return {
    set: Object.fromEntries(
      Object.entries(record(row.set)).map(([key, value]) => [key, text(value)]),
    ),
    remove: list(row.remove).map(text),
  }
}
function readCORS(value: unknown): CORSConfig {
  const row = record(value)
  return {
    enabled: boolean(row.enabled),
    allowed_origins: list(row.allowed_origins).map(text),
    allowed_methods: list(row.allowed_methods).map(text),
    allowed_headers: list(row.allowed_headers).map(text),
    exposed_headers: list(row.exposed_headers).map(text),
    allow_credentials: boolean(row.allow_credentials),
    max_age: integer(row.max_age),
  }
}
function readProxy(value: unknown): ProxyConfigView {
  const row = record(value)
  const result: ProxyConfigView = {
    configured_mode: oneOf(row.configured_mode, ['inherit', 'direct', 'custom']),
    effective_mode: oneOf(row.effective_mode, ['direct', 'environment', 'custom']),
    effective_source: oneOf(row.effective_source, [
      'credential',
      'group',
      'global',
      'environment',
      'default',
    ]),
    display_url: row.display_url === undefined ? '' : text(row.display_url),
    has_auth: boolean(row.has_auth),
  }
  if (
    (result.effective_mode === 'custom') !== Boolean(result.display_url) ||
    (result.has_auth && !result.display_url)
  )
    throw new InvalidResponseError()
  return result
}
function readSettings(value: unknown): SettingsData {
  const row = record(value)
  const values = record(row.values)
  const numbers = Object.fromEntries(
    Object.entries(settingNumbers).map(([key, rule]) => {
      const value = integer(values[key], rule.min)
      if (value > rule.max) throw new InvalidResponseError()
      return [key, value]
    }),
  ) as Record<SettingNumber, number>
  const switches = Object.fromEntries(
    settingSwitches.map((key) => [key, boolean(values[key])]),
  ) as Record<SettingSwitch, boolean>
  return {
    autoModelTemplate:
      row.auto_model_template === undefined ? undefined : readAutoEntry(row.auto_model_template),
    decisionModels: list(row.decision_models).map(text),
    values: {
      ...numbers,
      ...switches,
      route_strategy: oneOf(values.route_strategy, routeStrategies),
      header_rules: readHeaders(values.header_rules),
      response_header_rules: readHeaders(values.response_header_rules),
      cors: readCORS(values.cors),
      proxy_config: readProxy(values.proxy_config),
      auto_model: readAutoModel(values.auto_model),
    },
    overrides: list(row.overrides).map((key) => oneOf(key, settingKeys)),
    readOnly:
      row.read_only === undefined ? [] : list(row.read_only).map((key) => oneOf(key, settingKeys)),
  }
}
export async function getSettings(client: ApiClient, signal: AbortSignal): Promise<SettingsData> {
  return readSettings(await client.request('/api/settings', { signal }))
}
export async function saveSettings(
  client: ApiClient,
  settings: SettingsPatch,
  signal: AbortSignal,
): Promise<SettingsData> {
  return readSettings(
    await client.request('/api/settings', { method: 'PUT', json: { settings }, signal }),
  )
}
