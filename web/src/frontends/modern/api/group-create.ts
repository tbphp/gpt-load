import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'
import { readModelCandidates, type ModelCandidate } from './model-discovery'
import { sortProtocols } from '@modern/i18n/protocols'

export interface ChannelField {
  key: string
  label: string
  inputKind: 'text' | 'url' | 'secret'
  required: boolean
  sensitive: boolean
  defaultValue: string
}
export type AuthorizationMethod = 'browser_oauth' | 'device_oauth' | 'oauth_file'
export interface GroupChannel {
  id: string
  name: string
  icon: string
  mark: string
  keywords: string[]
  defaultBaseURL: string
  fields: ChannelField[]
  credentialFields: ChannelField[]
  discovery: boolean
  proxy: boolean
  quotaObservation: boolean
  resetCredit: boolean
  parameterProtocols: string[]
  nativeProtocols: string[]
  connectionType: 'api_key' | 'subscription'
  authorizationMethods: AuthorizationMethod[]
  notices: ('claude_oauth_risk' | 'antigravity_oauth_risk')[]
}
function field(value: unknown): ChannelField {
  const data = record(value)
  const key = text(data.key)
  if (!/^[a-z][a-z0-9_]*$/u.test(key)) throw new InvalidResponseError()
  return {
    key,
    label: text(data.label),
    inputKind: oneOf(data.input_kind, ['text', 'url', 'secret']),
    required: boolean(data.required),
    sensitive: boolean(data.sensitive),
    defaultValue: data.default_value === null ? '' : text(data.default_value),
  }
}
export async function getGroupChannels(
  client: ApiClient,
  signal: AbortSignal,
): Promise<GroupChannel[]> {
  const data = record(await client.request<unknown>('/api/channels', { signal }))
  const items = list(data.items).map((raw): GroupChannel => {
    const item = record(raw)
    const connection = record(item.connection)
    const connectionType = oneOf(connection.type, ['api_key', 'subscription'] as const)
    const authorizationMethods = list(connection.authorization_methods ?? []).map((method) =>
      oneOf(method, ['browser_oauth', 'device_oauth', 'oauth_file'] as const),
    )
    if (
      connection.credential_input !==
        (connectionType === 'api_key' ? 'batch_text' : 'authorization') ||
      (connectionType === 'subscription' && !authorizationMethods.length) ||
      (connectionType === 'api_key' && authorizationMethods.length) ||
      new Set(authorizationMethods).size !== authorizationMethods.length
    )
      throw new InvalidResponseError()
    const capabilities = record(item.capabilities)
    const protocolOperations = [
      'chat_completion',
      'responses_create',
      'images_generate',
      'embeddings_create',
      'rerank',
      'decisions_create',
    ]
    const routes = list(item.routes).map((raw) => {
      const route = record(raw)
      return {
        protocol: text(route.client_protocol),
        operation: text(route.operation),
        modes: [
          oneOf(route.route_mode, ['native', 'converted'] as const),
          ...list(route.possible_modes ?? []).map((mode) =>
            oneOf(mode, ['native', 'converted'] as const),
          ),
        ],
      }
    })
    const requestRoutes = routes.filter((route) => protocolOperations.includes(route.operation))
    return {
      id: text(item.channel_id),
      name: text(item.name),
      icon: text(item.icon),
      mark: text(item.mark),
      keywords: list(item.search_terms).map(text),
      defaultBaseURL: text(item.default_base_url),
      fields: list(item.param_fields).map(field),
      credentialFields: list(item.credential_fields).map(field),
      discovery: boolean(capabilities.model_discovery),
      proxy: boolean(capabilities.outbound_proxy),
      quotaObservation: boolean(capabilities.quota_observation),
      resetCredit: list(capabilities.credential_actions).includes('reset_credit'),
      parameterProtocols: sortProtocols(requestRoutes.map((route) => route.protocol)),
      nativeProtocols: sortProtocols(
        requestRoutes
          .filter((route) => route.modes.includes('native'))
          .map((route) => route.protocol),
      ),
      connectionType,
      authorizationMethods,
      notices: list(item.notices).map((raw) =>
        oneOf(record(raw).id, ['claude_oauth_risk', 'antigravity_oauth_risk'] as const),
      ),
    }
  })
  if (new Set(items.map((item) => item.id)).size !== items.length) throw new InvalidResponseError()
  return items
}
export interface ModelDraft {
  id: string
  alias: string
}
export type ProxyOverride = { mode: 'direct' } | { mode: 'custom'; url: string }
export interface GroupConnectionDraft {
  channel_id: string
  params: Record<string, string>
  proxy?: ProxyOverride
}
export type GroupCreateCredentials =
  | { connection_type: 'api_key'; credentials: string }
  | { connection_type: 'subscription'; staged_credential_ids: string[] }
export type GroupCreateRequest = GroupConnectionDraft &
  GroupCreateCredentials & {
    name?: string
    price_multiplier: string
    models: { id: string; alias: string; alias_enabled: boolean }[]
    confirm_same_target: boolean
  }
export interface GroupCreateResult {
  id: number
  name: string
  added: number
  duplicated: number
}
export async function discoverGroupDraftModels(
  client: ApiClient,
  body: GroupConnectionDraft &
    (
      | { connection_type: 'api_key'; credentials: string }
      | { connection_type: 'subscription'; staged_credential_id: string }
    ),
  signal: AbortSignal,
): Promise<ModelCandidate[]> {
  const data = record(
    await client.request<unknown>('/api/models/discover', { method: 'POST', json: body, signal }),
  )
  return readModelCandidates(data.models)
}
export async function createGroup(
  client: ApiClient,
  body: GroupCreateRequest,
  key: string,
  signal: AbortSignal,
): Promise<GroupCreateResult> {
  const result = record(
    await client.request<unknown>('/api/groups', {
      method: 'POST',
      json: body,
      headers: { 'Idempotency-Key': key },
      signal,
    }),
  )
  return {
    id: integer(result.group_id, 1),
    name: text(result.group_name),
    added: integer(result.credentials_added),
    duplicated: integer(result.credentials_duplicated),
  }
}
export async function appendAPIKeyCredentials(
  client: ApiClient,
  group: { id: number; name: string },
  credentials: string,
  key: string,
  signal: AbortSignal,
): Promise<GroupCreateResult> {
  const result = record(
    await client.request<unknown>(`/api/groups/${group.id}/credentials/import`, {
      method: 'POST',
      json: { credentials },
      headers: { 'Idempotency-Key': key },
      signal,
    }),
  )
  if (integer(result.group_id, 1) !== group.id) throw new InvalidResponseError()
  return {
    ...group,
    added: integer(result.credentials_added),
    duplicated: integer(result.credentials_duplicated),
  }
}
