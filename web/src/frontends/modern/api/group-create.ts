import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'
import { readModelCandidates, type ModelCandidate } from './model-discovery'

export interface ChannelField {
  key: string
  label: string
  inputKind: 'text' | 'url' | 'secret'
  required: boolean
  sensitive: boolean
  defaultValue: string
}
export interface APIKeyChannel {
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
export async function getAPIKeyChannels(
  client: ApiClient,
  signal: AbortSignal,
): Promise<APIKeyChannel[]> {
  const data = record(await client.request<unknown>('/api/channels', { signal }))
  const items = list(data.items).flatMap((raw): APIKeyChannel[] => {
    const item = record(raw)
    const connection = record(item.connection)
    if (oneOf(connection.type, ['api_key', 'subscription']) !== 'api_key') return []
    if (connection.credential_input !== 'batch_text') throw new InvalidResponseError()
    const capabilities = record(item.capabilities)
    return [
      {
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
      },
    ]
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
  connection_type: 'api_key'
  params: Record<string, string>
  credentials: string
  proxy?: ProxyOverride
}
export interface GroupCreateRequest extends GroupConnectionDraft {
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
  body: GroupConnectionDraft,
  signal: AbortSignal,
): Promise<ModelCandidate[]> {
  const data = record(
    await client.request<unknown>('/api/models/discover', { method: 'POST', json: body, signal }),
  )
  return readModelCandidates(data.models)
}
export async function createAPIKeyGroup(
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
