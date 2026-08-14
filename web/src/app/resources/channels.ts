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

export interface ChannelConnectionTypeDto {
  id: ChannelConnectionType
  credential_input: 'batch_text' | 'authorization'
  authorization_methods: Array<'browser_oauth' | 'oauth_file'>
}

export interface ChannelFieldDto {
  key: string
  label: string
  input_kind: ChannelFieldInputKind
  required: boolean
  sensitive: boolean
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
  connection_types: ChannelConnectionTypeDto[]
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
  'connection_types',
  'client_protocols',
] as const
const fieldFields = ['key', 'label', 'input_kind', 'required', 'sensitive'] as const
const listFields = ['items', 'total'] as const
const inputKinds = ['text', 'url', 'secret'] as const
const connectionTypes = ['api_key', 'subscription'] as const
const credentialInputs = ['batch_text', 'authorization'] as const
const authorizationMethods = ['browser_oauth', 'oauth_file'] as const
const connectionTypeFields = ['id', 'credential_input', 'authorization_methods'] as const

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
  if (
    key !== key.trim() ||
    !/^[a-z][a-z0-9_]*$/u.test(key) ||
    label.trim().length === 0 ||
    sensitive !== (inputKind === 'secret')
  ) {
    invalidResponse()
  }
  return {
    key,
    label,
    input_kind: inputKind,
    required: projectBoolean(record.required),
    sensitive,
  }
}

function projectConnectionType(value: unknown): ChannelConnectionTypeDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, connectionTypeFields)
  const id = projectEnum(record.id, connectionTypes)
  const credentialInput = projectEnum(record.credential_input, credentialInputs)
  const methods = projectArray(record.authorization_methods ?? [], (method) =>
    projectEnum(method, authorizationMethods),
  )
  if (
    new Set(methods).size !== methods.length ||
    (id === 'api_key' && (credentialInput !== 'batch_text' || methods.length !== 0)) ||
    (id === 'subscription' && credentialInput !== 'authorization')
  ) {
    invalidResponse()
  }
  return { id, credential_input: credentialInput, authorization_methods: methods }
}

function projectChannel(value: unknown): ChannelDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, channelFields)
  const paramFields = projectArray(record.param_fields, projectChannelField)
  const credentialFields = projectArray(record.credential_fields, projectChannelField)
  const projectedConnectionTypes = projectArray(record.connection_types, projectConnectionType)
  const clientProtocols = projectArray(record.client_protocols, (protocol) =>
    projectEnum(protocol, enabledDataProtocols),
  )
  if (
    new Set(paramFields.map(({ key }) => key)).size !== paramFields.length ||
    new Set(credentialFields.map(({ key }) => key)).size !== credentialFields.length ||
    new Set(projectedConnectionTypes.map(({ id }) => id)).size !==
      projectedConnectionTypes.length ||
    !projectedConnectionTypes.some(({ id }) => id === 'api_key') ||
    credentialFields.some(({ sensitive }) => !sensitive) ||
    new Set(clientProtocols).size !== clientProtocols.length
  ) {
    invalidResponse()
  }
  return {
    channel_id: projectChannelID(record.channel_id),
    name: projectString(record.name),
    mark: projectString(record.mark, { allowEmpty: true }),
    icon: projectString(record.icon, { allowEmpty: true }),
    search_terms: projectArray(record.search_terms, (term) => projectString(term)),
    description: projectString(record.description, { allowEmpty: true }),
    default_base_url: projectString(record.default_base_url, { allowEmpty: true }),
    param_fields: paramFields,
    credential_fields: credentialFields,
    connection_types: projectedConnectionTypes,
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
