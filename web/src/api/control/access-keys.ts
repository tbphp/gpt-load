import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { knownAccessProtocols } from './protocols'
import type {
  AccessKeyCreateResultDto,
  AccessKeyDto,
  AccessKeyFiltersDto,
  AccessKeyOptionDto,
  AccessKeyRevealDto,
  AccessProtocol,
} from './types'

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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isPositiveID(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function projectStatus(value: unknown): AccessKeyDto['status'] {
  if (value !== 'active' && value !== 'disabled') throw new InvalidResponseError()
  return value
}

function projectFilters(value: unknown): AccessKeyFiltersDto {
  if (
    !isRecord(value) ||
    !Array.isArray(value.groups) ||
    !Array.isArray(value.protocols) ||
    !Array.isArray(value.models) ||
    !value.groups.every(isPositiveID) ||
    !value.protocols.every((protocol) =>
      knownAccessProtocols.some((candidate) => candidate === protocol),
    ) ||
    !value.models.every((model) => typeof model === 'string')
  ) {
    throw new InvalidResponseError()
  }
  return {
    groups: [...value.groups],
    protocols: [...value.protocols] as AccessProtocol[],
    models: [...value.models],
  }
}

function projectTimestamp(value: unknown): string {
  if (typeof value !== 'string' || value === '' || !Number.isFinite(Date.parse(value))) {
    throw new InvalidResponseError()
  }
  return value
}

const canonicalMaskedAccessKey = /^sk-gl-••••••••[0-9a-f]{4}$/

export function projectAccessKeyMetadata(value: unknown): AccessKeyDto {
  if (
    !isRecord(value) ||
    !isPositiveID(value.id) ||
    typeof value.name !== 'string' ||
    typeof value.masked_key !== 'string' ||
    !canonicalMaskedAccessKey.test(value.masked_key) ||
    typeof value.rpm_limit !== 'number' ||
    !Number.isSafeInteger(value.rpm_limit) ||
    value.rpm_limit < 0
  ) {
    throw new InvalidResponseError()
  }
  return {
    id: value.id,
    name: value.name,
    masked_key: value.masked_key,
    status: projectStatus(value.status),
    filters: projectFilters(value.filters),
    rpm_limit: value.rpm_limit,
    created_at: projectTimestamp(value.created_at),
    updated_at: projectTimestamp(value.updated_at),
  }
}

function projectAccessKeyOption(value: unknown): AccessKeyOptionDto {
  if (!isRecord(value) || !isPositiveID(value.id) || typeof value.name !== 'string') {
    throw new InvalidResponseError()
  }
  return {
    id: value.id,
    name: value.name,
    status: projectStatus(value.status),
  }
}

export async function listAccessKeys(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<AccessKeyDto[]> {
  const value = await client.request<unknown>('/api/access-keys', { method: 'GET', signal })
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value.map(projectAccessKeyMetadata)
}

export async function listAccessKeyOptions(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<AccessKeyOptionDto[]> {
  const value = await client.request<unknown>('/api/access-keys/options', {
    method: 'GET',
    signal,
  })
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value.map(projectAccessKeyOption)
}

export async function createAccessKey(
  client: ApiClient,
  body: CreateAccessKeyRequest,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<AccessKeyCreateResultDto> {
  const value = await client.request<unknown>('/api/access-keys', {
    method: 'POST',
    headers: { 'Idempotency-Key': idempotencyKey },
    json: body,
    signal,
  })
  if (
    !isRecord(value) ||
    typeof value.replayed !== 'boolean' ||
    (value.key !== undefined && typeof value.key !== 'string')
  ) {
    throw new InvalidResponseError()
  }
  return {
    ...projectAccessKeyMetadata(value),
    ...(value.key === undefined ? {} : { key: value.key }),
    replayed: value.replayed,
  }
}

export async function revealAccessKey(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<AccessKeyRevealDto> {
  const value = await client.request<unknown>(`/api/access-keys/${id}/reveal`, {
    method: 'POST',
    signal,
  })
  if (!isRecord(value) || value.id !== id || typeof value.key !== 'string' || value.key === '') {
    throw new InvalidResponseError()
  }
  return {
    id,
    key: value.key,
    revealed_at: projectTimestamp(value.revealed_at),
  }
}

export async function updateAccessKey(
  client: ApiClient,
  id: number,
  body: UpdateAccessKeyRequest,
  signal?: AbortSignal,
): Promise<AccessKeyDto> {
  return projectAccessKeyMetadata(
    await client.request<unknown>(`/api/access-keys/${id}`, {
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
  return client.request<void>(`/api/access-keys/${id}`, { method: 'DELETE', signal })
}
