import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { AccessProtocol } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectRecord,
  projectString,
} from './projector'

export type ChannelFieldInputKind = 'text' | 'url' | 'secret'
export type ChannelConnectionType = 'api_key' | 'subscription'
export type ChannelAuthorizationMethod = 'browser_oauth' | 'oauth_file'
export type ChannelCredentialAction = 'reset_credit'

export interface ChannelConnectionDto {
  type: ChannelConnectionType
  credential_input: 'batch_text' | 'authorization'
  authorization_methods: ChannelAuthorizationMethod[]
}

export interface ChannelCapabilitiesDto {
  model_discovery: boolean
  quota_observation: boolean
  credential_actions: ChannelCredentialAction[]
}

export type ChannelRouteMode = 'native' | 'converted'
export type ChannelOperation =
  | 'chat_completion'
  | 'responses_create'
  | 'responses_retrieve'
  | 'responses_delete'
  | 'responses_cancel'
  | 'responses_input_items'
  | 'responses_compact'
  | 'responses_input_tokens'
  | 'responses_passthrough'
  | 'list_models'
  | 'probe'

export interface ChannelRouteDto {
  client_protocol: AccessProtocol
  operation: ChannelOperation
  route_mode: ChannelRouteMode
  model_dependent: boolean
  possible_modes: ChannelRouteMode[]
}

export interface ChannelFieldDto {
  key: string
  label: string
  input_kind: ChannelFieldInputKind
  required: boolean
  sensitive: boolean
  default_value: string | null
}

export interface ChannelDto {
  channel_id: string
  name: string
  mark: string
  icon: string
  search_terms: string[]
  description: string
  default_base_url: string
  param_fields: ChannelFieldDto[]
  credential_fields: ChannelFieldDto[]
  connection: ChannelConnectionDto
  capabilities: ChannelCapabilitiesDto
  routes: ChannelRouteDto[]
  client_protocols: AccessProtocol[]
}

export interface ChannelListDto {
  items: ChannelDto[]
  total: number
}

const channelFields = [
  'channel_id',
  'name',
  'mark',
  'icon',
  'search_terms',
  'description',
  'default_base_url',
  'param_fields',
  'credential_fields',
  'connection',
  'capabilities',
  'routes',
  'client_protocols',
] as const
const fieldFields = [
  'key',
  'label',
  'input_kind',
  'required',
  'sensitive',
  'default_value',
] as const
const listFields = ['items', 'total'] as const
const inputKinds = ['text', 'url', 'secret'] as const
const connectionTypes = ['api_key', 'subscription'] as const
const credentialInputs = ['batch_text', 'authorization'] as const
const authorizationMethods = ['browser_oauth', 'oauth_file'] as const
const connectionFields = ['type', 'credential_input', 'authorization_methods'] as const
const capabilityFields = ['model_discovery', 'quota_observation', 'credential_actions'] as const
const credentialActions = ['reset_credit'] as const
const routeFields = [
  'client_protocol',
  'operation',
  'route_mode',
  'model_dependent',
  'possible_modes',
] as const
const routeModes = ['native', 'converted'] as const
const operations = [
  'chat_completion',
  'responses_create',
  'responses_retrieve',
  'responses_delete',
  'responses_cancel',
  'responses_input_items',
  'responses_compact',
  'responses_input_tokens',
  'responses_passthrough',
  'list_models',
  'probe',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

export function projectChannelID(value: unknown): string {
  const result = projectString(value)
  if (result !== result.trim() || !/^[a-z][a-z0-9_]*$/u.test(result) || result.length > 100) {
    invalidResponse()
  }
  return result
}

function projectChannelField(value: unknown): ChannelFieldDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, fieldFields)
  const key = projectString(record.key)
  const label = projectString(record.label)
  const inputKind = projectEnum(record.input_kind, inputKinds)
  const sensitive = projectBoolean(record.sensitive)
  const defaultValue = record.default_value === null ? null : projectString(record.default_value)
  if (
    key !== key.trim() ||
    !/^[a-z][a-z0-9_]*$/u.test(key) ||
    label.trim().length === 0 ||
    sensitive !== (inputKind === 'secret') ||
    (sensitive && defaultValue !== null)
  ) {
    invalidResponse()
  }
  return {
    key,
    label,
    input_kind: inputKind,
    required: projectBoolean(record.required),
    sensitive,
    default_value: defaultValue,
  }
}

function projectConnection(value: unknown): ChannelConnectionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, connectionFields)
  const type = projectEnum(record.type, connectionTypes)
  const credentialInput = projectEnum(record.credential_input, credentialInputs)
  const methods = projectArray(record.authorization_methods ?? [], (method) =>
    projectEnum(method, authorizationMethods),
  )
  if (
    new Set(methods).size !== methods.length ||
    (type === 'api_key' && (credentialInput !== 'batch_text' || methods.length !== 0)) ||
    (type === 'subscription' && (credentialInput !== 'authorization' || methods.length === 0))
  ) {
    invalidResponse()
  }
  return { type, credential_input: credentialInput, authorization_methods: methods }
}

function projectCapabilities(value: unknown): ChannelCapabilitiesDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, capabilityFields)
  const actions = projectArray(record.credential_actions, (action) =>
    projectEnum(action, credentialActions),
  )
  if (new Set(actions).size !== actions.length) invalidResponse()
  return {
    model_discovery: projectBoolean(record.model_discovery),
    quota_observation: projectBoolean(record.quota_observation),
    credential_actions: actions,
  }
}

function projectRoute(value: unknown): ChannelRouteDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, routeFields)
  const routeMode = projectEnum(record.route_mode, routeModes)
  const modelDependent = projectBoolean(record.model_dependent)
  const possibleModes = projectArray(record.possible_modes ?? [], (mode) =>
    projectEnum(mode, routeModes),
  )
  if (
    new Set(possibleModes).size !== possibleModes.length ||
    (!modelDependent && possibleModes.length !== 0) ||
    (modelDependent && (possibleModes.length === 0 || !possibleModes.includes(routeMode)))
  ) {
    invalidResponse()
  }
  return {
    client_protocol: projectEnum(record.client_protocol, enabledDataProtocols),
    operation: projectEnum(record.operation, operations),
    route_mode: routeMode,
    model_dependent: modelDependent,
    possible_modes: possibleModes,
  }
}

function projectChannel(value: unknown): ChannelDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, channelFields)
  const paramFields = projectArray(record.param_fields, projectChannelField)
  const credentialFields = projectArray(record.credential_fields, projectChannelField)
  const connection = projectConnection(record.connection)
  const capabilities = projectCapabilities(record.capabilities)
  const routes = projectArray(record.routes, projectRoute)
  const clientProtocols = projectArray(record.client_protocols, (protocol) =>
    projectEnum(protocol, enabledDataProtocols),
  )
  const routeKeys = routes.map(
    ({ client_protocol, operation }) => `${client_protocol}\u0000${operation}`,
  )
  const derivedProtocols = enabledDataProtocols.filter((protocol) =>
    routes.some(({ client_protocol }) => client_protocol === protocol),
  )
  if (
    new Set(paramFields.map(({ key }) => key)).size !== paramFields.length ||
    new Set(credentialFields.map(({ key }) => key)).size !== credentialFields.length ||
    new Set(routeKeys).size !== routeKeys.length ||
    credentialFields.some(({ sensitive }) => !sensitive) ||
    new Set(clientProtocols).size !== clientProtocols.length ||
    JSON.stringify(clientProtocols) !== JSON.stringify(derivedProtocols)
  ) {
    invalidResponse()
  }
  return {
    channel_id: projectChannelID(record.channel_id),
    name: projectString(record.name),
    mark: projectString(record.mark),
    icon: projectString(record.icon),
    search_terms: projectArray(record.search_terms, (term) => projectString(term)),
    description: projectString(record.description, { allowEmpty: true }),
    default_base_url: projectString(record.default_base_url, { allowEmpty: true }),
    param_fields: paramFields,
    credential_fields: credentialFields,
    connection,
    capabilities,
    routes,
    client_protocols: clientProtocols,
  }
}

export function projectChannelList(value: unknown): ChannelListDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, listFields)
  const items = projectArray(record.items, projectChannel)
  const total = Number(record.total)
  if (
    !Number.isSafeInteger(total) ||
    total < 0 ||
    total !== items.length ||
    new Set(items.map(({ channel_id }) => channel_id)).size !== items.length
  ) {
    invalidResponse()
  }
  return { items, total }
}

export function normalizeChannelSearch(value: string): string {
  return [...value.trim()].slice(0, 200).join('')
}

export async function listChannels(
  client: ApiClient,
  search: string,
  signal?: AbortSignal,
): Promise<ChannelListDto> {
  const normalized = normalizeChannelSearch(search)
  const params = new URLSearchParams()
  if (normalized) params.set('q', normalized)
  const suffix = params.size > 0 ? `?${params.toString()}` : ''
  return projectChannelList(
    await client.request(`/api/channels${suffix}`, { method: 'GET', signal }),
  )
}

export function channelsQueryOptions(client: ApiClient, search: MaybeRefOrGetter<string>) {
  return queryOptions({
    queryKey: computed(() =>
      controlQueryKeys.channels.list(normalizeChannelSearch(toValue(search))),
    ),
    queryFn: ({ queryKey, signal }) => listChannels(client, queryKey[3], signal),
    staleTime: 5 * 60 * 1_000,
    placeholderData: keepPreviousData,
  })
}
