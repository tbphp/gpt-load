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
  description: string
  default_base_url: string
  param_fields: ChannelFieldDto[]
  credential_fields: ChannelFieldDto[]
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
  'description',
  'default_base_url',
  'param_fields',
  'credential_fields',
  'client_protocols',
] as const
const fieldFields = ['key', 'label', 'input_kind', 'required', 'sensitive'] as const
const listFields = ['items', 'total'] as const
const inputKinds = ['text', 'url', 'secret'] as const

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

function projectChannel(value: unknown): ChannelDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, channelFields)
  const paramFields = projectArray(record.param_fields, projectChannelField)
  const credentialFields = projectArray(record.credential_fields, projectChannelField)
  const clientProtocols = projectArray(record.client_protocols, (protocol) =>
    projectEnum(protocol, enabledDataProtocols),
  )
  if (
    new Set(paramFields.map(({ key }) => key)).size !== paramFields.length ||
    new Set(credentialFields.map(({ key }) => key)).size !== credentialFields.length ||
    credentialFields.some(({ sensitive }) => !sensitive) ||
    new Set(clientProtocols).size !== clientProtocols.length
  ) {
    invalidResponse()
  }
  return {
    channel_id: projectChannelID(record.channel_id),
    name: projectString(record.name),
    mark: projectString(record.mark, { allowEmpty: true }),
    description: projectString(record.description, { allowEmpty: true }),
    default_base_url: projectString(record.default_base_url, { allowEmpty: true }),
    param_fields: paramFields,
    credential_fields: credentialFields,
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
