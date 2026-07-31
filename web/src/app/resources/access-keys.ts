import { queryOptions } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import type {
  AccessKeyCreateResultDto,
  AccessKeyDto,
  AccessKeyFiltersDto,
  AccessKeyOptionDto,
  AccessKeyRevealDto,
} from '@/api/control/types'
import { knownAccessProtocols } from '@/api/control/protocols'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { isCanonicalMaskedAccessKey } from '@/lib/access-key-mask'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectEpochMilliseconds,
  projectEnum,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type {
  AccessKeyCreateResultDto,
  AccessKeyDto,
  AccessKeyFiltersDto,
  AccessKeyOptionDto,
  AccessKeyRevealDto,
  AccessProtocol,
} from '@/api/control/types'

export interface CreateAccessKeyRequest {
  name: string
  filters: AccessKeyFiltersDto
  rpm_limit: number
}

export type UpdateAccessKeyRequest = Partial<{
  name: string
  status: AccessKeyDto['status']
  filters: AccessKeyFiltersDto
  rpm_limit: number
}>

const metadataFields = [
  'id',
  'name',
  'masked_key',
  'status',
  'filters',
  'rpm_limit',
  'created_at_ms',
  'updated_at_ms',
] as const
const optionFields = ['id', 'name', 'status'] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankTrimmedString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0 || result !== result.trim()) invalidResponse()
  return result
}

function projectFilters(value: unknown): AccessKeyFiltersDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['groups', 'protocols', 'models'])
  const groups = projectArray(record.groups, (id) => projectSafeInteger(id, { minimum: 1 }))
  const protocols = projectArray(record.protocols, (protocol) =>
    projectEnum(protocol, knownAccessProtocols),
  )
  const models = projectArray(record.models, projectNonBlankTrimmedString)
  if (
    new Set(groups).size !== groups.length ||
    new Set(protocols).size !== protocols.length ||
    new Set(models).size !== models.length
  ) {
    invalidResponse()
  }
  return { groups, protocols, models }
}

export function projectAccessKeyMetadata(value: unknown): AccessKeyDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, metadataFields)
  const maskedKey = projectString(record.masked_key)
  if (!isCanonicalMaskedAccessKey(maskedKey)) invalidResponse()
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankTrimmedString(record.name),
    masked_key: maskedKey,
    status: projectEnum(record.status, ['active', 'disabled'] as const),
    filters: projectFilters(record.filters),
    rpm_limit: projectSafeInteger(record.rpm_limit, { minimum: 0 }),
    created_at_ms: projectEpochMilliseconds(record.created_at_ms),
    updated_at_ms: projectEpochMilliseconds(record.updated_at_ms),
  }
}

export function projectAccessKeyOption(value: unknown): AccessKeyOptionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, optionFields)
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankTrimmedString(record.name),
    status: projectEnum(record.status, ['active', 'disabled'] as const),
  }
}

export async function listAccessKeys(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<AccessKeyDto[]> {
  return projectArray(
    await client.request('/api/access-keys', { method: 'GET', signal }),
    projectAccessKeyMetadata,
  )
}

export async function listAccessKeyOptions(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<AccessKeyOptionDto[]> {
  return projectArray(
    await client.request('/api/access-keys/options', { method: 'GET', signal }),
    projectAccessKeyOption,
  )
}

export function accessKeyListQueryOptions(client: ApiClient) {
  return queryOptions({
    queryKey: controlQueryKeys.accessKeys.list(),
    queryFn: ({ signal }) => listAccessKeys(client, signal),
    gcTime: 0,
  })
}

export function accessKeyOptionsQueryOptions(client: ApiClient) {
  return queryOptions({
    queryKey: controlQueryKeys.accessKeys.options(),
    queryFn: ({ signal }) => listAccessKeyOptions(client, signal),
    gcTime: 0,
  })
}

export async function createAccessKey(
  client: ApiClient,
  body: CreateAccessKeyRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<AccessKeyCreateResultDto> {
  const record = projectRecord(
    await client.request('/api/access-keys', {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      json: body,
      signal,
    }),
  )
  assertNoSecretLikeFields(record, [...metadataFields, 'key', 'replayed'])
  const key = record.key === undefined ? undefined : projectString(record.key)
  if (typeof record.replayed !== 'boolean') invalidResponse()
  const metadata = Object.fromEntries(metadataFields.map((field) => [field, record[field]]))
  return {
    ...projectAccessKeyMetadata(metadata),
    ...(key === undefined ? {} : { key }),
    replayed: record.replayed,
  }
}

export async function revealAccessKey(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<AccessKeyRevealDto> {
  const record = projectRecord(
    await client.request(`/api/access-keys/${id}/reveal`, {
      method: 'POST',
      signal,
    }),
  )
  assertNoSecretLikeFields(record, ['id', 'key', 'revealed_at_ms'])
  if (projectSafeInteger(record.id, { minimum: 1 }) !== id) invalidResponse()
  return {
    id,
    key: projectString(record.key),
    revealed_at_ms: projectEpochMilliseconds(record.revealed_at_ms),
  }
}

export async function updateAccessKey(
  client: ApiClient,
  id: number,
  body: UpdateAccessKeyRequest,
  signal?: AbortSignal,
): Promise<AccessKeyDto> {
  return projectAccessKeyMetadata(
    await client.request(`/api/access-keys/${id}`, {
      method: 'PUT',
      json: body,
      signal,
    }),
  )
}

export function deleteAccessKey(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<void> {
  return client.request(`/api/access-keys/${id}`, { method: 'DELETE', signal })
}

export const accessKeyResources = {
  list: {
    queryKey: controlQueryKeys.accessKeys.list(),
    gcTime: 0,
    cleanup: 'authenticated-session',
    optimisticUpdates: false,
    allowedFields: metadataFields,
  },
  options: {
    queryKey: controlQueryKeys.accessKeys.options(),
    gcTime: 0,
    cleanup: 'authenticated-session',
    optimisticUpdates: false,
    allowedFields: optionFields,
  },
} as const
