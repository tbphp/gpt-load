import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'
import { readModelCandidates } from './model-discovery'
import type { ProxyOverride } from './group-create'
import { sortProtocols } from '@modern/i18n/protocols'
import { readObservation, type CredentialObservation } from './credential-observation'
import { readGroupBasics, type GroupBasics } from './groups'

export const groupSettingsKey = (id: number) => ['modern', 'group-settings', id] as const
export const groupModelsKey = (id: number) => ['modern', 'group-models', id] as const
export const groupCredentialsKey = (id: number) => ['modern', 'group-credentials', id] as const
export const credentialStates = ['available', 'cooldown', 'blacklisted', 'disabled'] as const
export type CredentialState = (typeof credentialStates)[number]
export const credentialSorts = [
  'priority',
  'newest',
  'oldest',
  'name',
  'weight_desc',
  'weight_asc',
  'failures',
] as const
export interface CredentialFilters {
  credential?: string
  q: string
  status: string
  page: number
  pageSize: number
  sort: (typeof credentialSorts)[number]
  proxy: '' | 'inherit' | 'direct' | 'custom'
  reset: '' | 'available' | 'none' | 'unknown'
}
export const runtimeNumbers = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'blacklist_threshold',
] as const
export const runtimeSwitches = ['affinity_enabled', 'responses_websocket_enabled'] as const
export type RuntimeNumber = (typeof runtimeNumbers)[number]
export type RuntimeSwitch = (typeof runtimeSwitches)[number]
export interface HeaderRules {
  set: Record<string, string>
  remove: string[]
}
export interface ParameterRule {
  match: { protocol?: string; model?: string }
  set?: Record<string, unknown>
  remove?: string[]
}
export type RuntimeSettings = Partial<
  Record<RuntimeNumber, number> & Record<RuntimeSwitch, boolean>
> & {
  header_rules?: HeaderRules
  parameter_overrides?: ParameterRule[]
}
export interface GroupSettings extends GroupBasics {
  channelID: string
  params: Record<string, string>
  validationModel: string | null
  validationProtocol: string | null
  validationProtocols: string[]
  overrides: RuntimeSettings
  effective: Required<Omit<RuntimeSettings, 'parameter_overrides'>>
  proxy: { mode: 'inherit' | 'direct' | 'custom'; display: string; hasAuth: boolean }
}
export interface AdvancedSettingsPatch {
  params?: Record<string, string>
  validation_model?: string | null
  validation_protocol?: string
  overrides?: RuntimeSettings
  proxy?: ProxyOverride | null
}
function stringMap(value: unknown): Record<string, string> {
  return Object.fromEntries(Object.entries(record(value)).map(([key, value]) => [key, text(value)]))
}
function readRuntime(value: unknown): RuntimeSettings {
  const raw = record(value)
  const result: RuntimeSettings = {}
  for (const key of runtimeNumbers) if (raw[key] !== undefined) result[key] = integer(raw[key])
  for (const key of runtimeSwitches) if (raw[key] !== undefined) result[key] = boolean(raw[key])
  if (raw.header_rules !== undefined) {
    const headers = record(raw.header_rules)
    result.header_rules = { set: stringMap(headers.set), remove: list(headers.remove).map(text) }
  }
  if (raw.parameter_overrides !== undefined) {
    result.parameter_overrides = list(raw.parameter_overrides).map((value) => {
      const rule = record(value)
      const match = record(rule.match)
      return {
        match: {
          ...(match.protocol === undefined ? {} : { protocol: text(match.protocol) }),
          ...(match.model === undefined ? {} : { model: text(match.model) }),
        },
        ...(rule.set === undefined ? {} : { set: record(rule.set) }),
        ...(rule.remove === undefined ? {} : { remove: list(rule.remove).map(text) }),
      }
    })
  }
  return result
}
function readSettings(value: unknown): GroupSettings {
  const data = record(value)
  const proxy = record(data.proxy)
  const effective = readRuntime(data.effective)
  if (
    [...runtimeNumbers, ...runtimeSwitches, 'header_rules'].some(
      (key) => effective[key as keyof RuntimeSettings] === undefined,
    )
  )
    throw new InvalidResponseError()
  return {
    ...readGroupBasics(data),
    channelID: text(data.channel_id),
    params: stringMap(data.params),
    validationModel: data.validation_model === null ? null : text(data.validation_model),
    validationProtocol: data.validation_protocol === null ? null : text(data.validation_protocol),
    validationProtocols: sortProtocols(list(data.validation_protocols).map(text)),
    overrides: readRuntime(data.overrides),
    effective: effective as GroupSettings['effective'],
    proxy: {
      mode: oneOf(proxy.configured_mode, ['inherit', 'direct', 'custom']),
      display: proxy.display_url === undefined ? '' : text(proxy.display_url),
      hasAuth: boolean(proxy.has_auth),
    },
  }
}
export async function getGroupSettings(client: ApiClient, id: number, signal: AbortSignal) {
  return readSettings(await client.request(`/api/groups/${id}/settings`, { signal }))
}
export async function saveGroupSettings(
  client: ApiClient,
  id: number,
  patch: AdvancedSettingsPatch,
  signal: AbortSignal,
) {
  return readSettings(
    await client.request(`/api/groups/${id}/settings`, { method: 'PUT', json: patch, signal }),
  )
}

export interface GroupModel {
  id: string
  alias: string
  clientModel: string
  pricingStatus: 'pending' | 'configured'
}
function readModels(value: unknown): GroupModel[] {
  return list(record(value).items).map((value) => {
    const model = record(value)
    return {
      id: text(model.id),
      alias: text(model.alias),
      clientModel: text(model.client_model),
      pricingStatus: oneOf(model.pricing_status, ['pending', 'configured']),
    }
  })
}
export async function getGroupModels(client: ApiClient, id: number, signal: AbortSignal) {
  return readModels(await client.request(`/api/groups/${id}/models`, { signal }))
}
export async function saveGroupModels(
  client: ApiClient,
  id: number,
  models: { id: string; alias: string }[],
  signal: AbortSignal,
) {
  return readModels(
    await client.request(`/api/groups/${id}/models`, {
      method: 'PUT',
      signal,
      json: {
        models: models.map((model) => ({
          id: model.id.trim(),
          alias: model.alias.trim(),
          alias_enabled: Boolean(model.alias.trim()),
        })),
      },
    }),
  )
}
export async function discoverGroupModels(client: ApiClient, id: number, signal: AbortSignal) {
  const data = record(
    await client.request(`/api/groups/${id}/models/discover`, { method: 'POST', signal }),
  )
  return readModelCandidates(data.models)
}

export interface CredentialRow {
  id: number
  mask: string
  account: string
  state: CredentialState
  enabled: boolean
  weight: number
  weightManual?: number | null
  successes: number
  failures: number
  failuresInRow: number
  lastUsed: number | null
  cooldownUntil: number | null
  daily: { successes: number; failures: number; complete: boolean } | null
  authState: 'ready' | 'refreshing' | 'reauthorization_required' | 'outcome_unknown'
  secretVersion: number
  expiresAt?: number
  lastRefresh?: number
  authError?: string
  failureCategory: string
  lastStatusCode: number | null
  modelCooldowns: { model: string; until: number }[]
  recovery: {
    mode: 'none' | 'cooldown' | 'probe' | 'manual'
    automatic: boolean
    at: number | null
  }
  proxy: { mode: 'inherit' | 'direct' | 'custom'; source: string; display: string }
  observation?: CredentialObservation
}
export interface CredentialCollection {
  items: CredentialRow[]
  counts: Record<CredentialState | 'total', number>
  total: number
  page: number
  pageSize: number
}
export function readCredential(value: unknown): CredentialRow {
  const row = record(value)
  const account = row.account == null ? null : record(row.account)
  const daily = row.daily_usage == null ? null : record(row.daily_usage)
  const proxy = record(row.proxy)
  const recovery = record(row.recovery)
  return {
    id: integer(row.credential_id, 1),
    mask: text(row.mask),
    account: account ? text(account.email ?? account.email_mask ?? '') : '',
    state: oneOf(row.effective_status, credentialStates),
    enabled: oneOf(row.configured_status, ['active', 'disabled']) === 'active',
    weight: integer(row.weight),
    weightManual:
      row.weight_manual === undefined
        ? undefined
        : row.weight_manual === null
          ? null
          : integer(row.weight_manual),
    successes: integer(row.recent_success_count),
    failures: integer(row.recent_failure_count),
    failuresInRow: integer(row.consecutive_failure_count),
    lastUsed: row.last_used_at_ms == null ? null : integer(row.last_used_at_ms),
    cooldownUntil: row.cooldown_until_ms == null ? null : integer(row.cooldown_until_ms),
    daily: daily
      ? {
          successes: integer(daily.success_count),
          failures: integer(daily.failure_count),
          complete: boolean(daily.data_complete),
        }
      : null,
    authState: oneOf(row.auth_state, [
      'ready',
      'refreshing',
      'reauthorization_required',
      'outcome_unknown',
    ]),
    secretVersion: integer(row.secret_version, 1),
    expiresAt: account?.expires_at_ms === undefined ? undefined : integer(account.expires_at_ms),
    lastRefresh:
      account?.last_refresh_at_ms === undefined ? undefined : integer(account.last_refresh_at_ms),
    authError: row.auth_error_code === undefined ? undefined : text(row.auth_error_code),
    failureCategory: text(row.last_failure_category),
    lastStatusCode: row.last_status_code === null ? null : integer(row.last_status_code),
    modelCooldowns: list(row.model_cooldowns).map((value) => {
      const cooldown = record(value)
      return { model: text(cooldown.model), until: integer(cooldown.cooldown_until_ms) }
    }),
    recovery: {
      mode: oneOf(recovery.mode, ['none', 'cooldown', 'probe', 'manual']),
      automatic: boolean(recovery.automatic),
      at: recovery.at_ms === null ? null : integer(recovery.at_ms),
    },
    proxy: {
      mode: oneOf(proxy.configured_mode, ['inherit', 'direct', 'custom']),
      source: text(proxy.effective_source),
      display: proxy.display_url === undefined ? '' : text(proxy.display_url),
    },
    observation: readObservation(row.observation),
  }
}
export async function getGroupCredentials(
  client: ApiClient,
  id: number,
  filters: CredentialFilters,
  signal: AbortSignal,
): Promise<CredentialCollection> {
  const params = new URLSearchParams({
    page: String(filters.page),
    page_size: String(filters.pageSize),
  })
  if (filters.q.trim()) params.set('q', filters.q.trim())
  if (filters.credential) params.set('credential_key', filters.credential)
  if (filters.status) params.set('status', filters.status)
  if (filters.sort !== 'priority') params.set('sort', filters.sort)
  if (filters.proxy) params.set('proxy', filters.proxy)
  if (filters.reset) params.set('reset', filters.reset)
  const data = record(
    await client.request(`/api/modern/groups/${id}/credentials?${params}`, { signal }),
  )
  const summary = record(data.summary)
  const pagination = record(data.pagination)
  return {
    items: list(data.items).map(readCredential),
    counts: {
      total: integer(summary.total),
      available: integer(summary.available),
      cooldown: integer(summary.cooldown),
      blacklisted: integer(summary.blacklisted),
      disabled: integer(summary.disabled),
    },
    total: integer(pagination.total_items),
    page: integer(pagination.page, 1),
    pageSize: integer(pagination.page_size, 1),
  }
}
export async function setCredentialEnabled(
  client: ApiClient,
  groupID: number,
  id: number,
  enabled: boolean,
  signal: AbortSignal,
) {
  return readCredential(
    await client.request(`/api/groups/${groupID}/credentials/${id}`, {
      method: 'PUT',
      json: { status: enabled ? 'active' : 'disabled' },
      signal,
    }),
  )
}
export async function batchGroupCredentials(
  client: ApiClient,
  groupID: number,
  ids: number[],
  action: 'enable' | 'disable' | 'delete',
  signal: AbortSignal,
) {
  const data = record(
    await client.request(`/api/groups/${groupID}/credentials/batch`, {
      method: 'POST',
      json: { action, credential_ids: ids },
      signal,
    }),
  )
  return list(data.affected_credential_ids).map((id) => integer(id, 1))
}

export async function batchAllGroupCredentials(
  client: ApiClient,
  groupID: number,
  action: 'enable' | 'disable' | 'restore',
  signal: AbortSignal,
) {
  const data = record(
    await client.request(`/api/groups/${groupID}/credentials/batch`, {
      method: 'POST',
      json: { action, scope: 'all' },
      signal,
    }),
  )
  return list(data.affected_credential_ids).map((id) => integer(id, 1))
}
